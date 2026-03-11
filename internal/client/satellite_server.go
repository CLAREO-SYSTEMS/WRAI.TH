// Package client — satellite_server.go implements the HTTP API that runs on
// satellite machines. It receives spawn/stop/wake/status commands from the
// station and manages local Claude subprocesses. The satellite has no relay
// connection, no SSE, no agent configs — it's a dumb executor.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"agent-relay/internal/monitor"
)

// SpawnRequest is the payload the station sends to spawn an agent on a satellite.
type SpawnRequest struct {
	Name         string      `json:"name"`
	Config       AgentConfig `json:"config"`
	RelayURL     string      `json:"relay_url"`
	RelayProject string      `json:"relay_project"`
	Prompt       string      `json:"prompt"` // ready-made boot/wake prompt built by station
	Resume       bool        `json:"resume"` // true = --resume existing session
	Reason       string      `json:"reason"`
}

// SatelliteServer is the HTTP server that runs on satellite machines.
type SatelliteServer struct {
	machineName string
	sessions    map[string]*Session
	tracker     *monitor.Tracker
	mu          sync.RWMutex
	mux         *http.ServeMux
	ctx         context.Context
}

// NewSatelliteServer creates a satellite HTTP server.
func NewSatelliteServer(cfg *Config, tracker *monitor.Tracker, ctx context.Context) *SatelliteServer {
	s := &SatelliteServer{
		machineName: cfg.Machine.Name,
		sessions:    make(map[string]*Session),
		tracker:     tracker,
		mux:         http.NewServeMux(),
		ctx:         ctx,
	}
	s.registerRoutes()
	return s
}

// Handler returns the HTTP handler.
func (s *SatelliteServer) Handler() http.Handler {
	return s.mux
}

func (s *SatelliteServer) registerRoutes() {
	s.mux.HandleFunc("/api/spawn", s.handleSpawn)
	s.mux.HandleFunc("/api/stop/", s.handleStop)
	s.mux.HandleFunc("/api/wake/", s.handleWake)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/terminal/", s.handleTerminal)
	s.mux.HandleFunc("/api/health", s.handleHealth)
}

func (s *SatelliteServer) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req SpawnRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Name == "" {
		http.Error(w, "invalid spawn request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	sess, exists := s.sessions[req.Name]
	if !exists {
		sess = NewSession(req.Name, req.Config)
		sess.SetRelay(req.RelayURL, req.RelayProject)
		s.sessions[req.Name] = sess
	}
	s.mu.Unlock()

	state := sess.GetState()
	if state == StateWorking || state == StateSpawning {
		http.Error(w, fmt.Sprintf("agent %s is already %s", req.Name, state), http.StatusConflict)
		return
	}

	go s.runAgent(sess, req)

	writeJSONSat(w, map[string]string{"status": "spawning", "machine": s.machineName})
}

func (s *SatelliteServer) runAgent(sess *Session, req SpawnRequest) {
	var result *TurnResult
	var err error

	if req.Resume && sess.TurnCount > 0 {
		result, err = sess.Resume(s.ctx, req.Prompt)
	} else {
		result, err = sess.Spawn(s.ctx, req.Prompt)
	}

	if err != nil {
		log.Printf("[satellite] agent %s failed: %v", req.Name, err)
		return
	}

	if s.tracker != nil && result != nil {
		s.tracker.Record(sess.Name, result.Model, result.CostUSD, result.Duration, sess.TurnCount, result.InputTokens, result.OutputTokens)
	}
}

func (s *SatelliteServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agent := strings.TrimPrefix(r.URL.Path, "/api/stop/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	sess, ok := s.sessions[agent]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown agent: "+agent, http.StatusNotFound)
		return
	}

	sess.Kill()
	sess.SetState(StateSleeping)
	writeJSONSat(w, map[string]string{"status": "stopped"})
}

func (s *SatelliteServer) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agent := strings.TrimPrefix(r.URL.Path, "/api/wake/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	sess, ok := s.sessions[agent]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown agent: "+agent, http.StatusNotFound)
		return
	}

	state := sess.GetState()
	if state == StateWorking || state == StateSpawning {
		http.Error(w, fmt.Sprintf("agent %s is already %s", agent, state), http.StatusConflict)
		return
	}

	go func() {
		result, err := sess.Resume(s.ctx, req.Prompt)
		if err != nil {
			log.Printf("[satellite] wake %s failed: %v", agent, err)
			return
		}
		if s.tracker != nil && result != nil {
			s.tracker.Record(sess.Name, result.Model, result.CostUSD, result.Duration, sess.TurnCount, result.InputTokens, result.OutputTokens)
		}
	}()

	writeJSONSat(w, map[string]string{"status": "waking"})
}

func (s *SatelliteServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]map[string]interface{})
	for name, sess := range s.sessions {
		entry := map[string]interface{}{
			"state":       sess.GetState().String(),
			"turn_count":  sess.TurnCount,
			"last_cost":   sess.LastCost,
			"crash_count": sess.CrashCount,
			"session_id":  sess.SessionID,
			"machine":     s.machineName,
		}
		if s.tracker != nil {
			inTok, outTok := s.tracker.AgentTokens(name)
			entry["tokens_used"] = inTok + outTok
		}
		states[name] = entry
	}

	writeJSONSat(w, states)
}

func (s *SatelliteServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimPrefix(r.URL.Path, "/api/terminal/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	sess, ok := s.sessions[agent]
	s.mu.RUnlock()
	if !ok {
		writeJSONSat(w, []interface{}{})
		return
	}

	writeJSONSat(w, sess.GetTerminalLines())
}

func (s *SatelliteServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	count := len(s.sessions)
	s.mu.RUnlock()

	writeJSONSat(w, map[string]interface{}{
		"status":  "ok",
		"machine": s.machineName,
		"agents":  count,
	})
}

// Stop gracefully shuts down all satellite sessions.
func (s *SatelliteServer) Stop() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for name, sess := range s.sessions {
		if sess.IsRunning() {
			log.Printf("[satellite] killing %s", name)
			sess.Kill()
		}
	}
}

func writeJSONSat(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

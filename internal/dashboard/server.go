// Package dashboard serves the Mission Control web UI for the WRAI.TH client.
// Exposes fleet state, agent control, and token stats via HTTP + WebSocket.
package dashboard

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"agent-relay/internal/client"
	"agent-relay/internal/monitor"
)

//go:embed static
var staticFiles embed.FS

// Server serves the Mission Control dashboard.
type Server struct {
	manager *client.Manager
	config  *client.Config
	tracker *monitor.Tracker
	relay   *client.RelayClient
	mux     *http.ServeMux
}

// NewServer creates a dashboard server.
func NewServer(mgr *client.Manager, cfg *client.Config, tracker *monitor.Tracker, relay *client.RelayClient) *Server {
	s := &Server{
		manager: mgr,
		config:  cfg,
		tracker: tracker,
		relay:   relay,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Handler returns the HTTP handler for this server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	// API routes
	s.mux.HandleFunc("/api/fleet", s.handleFleet)
	s.mux.HandleFunc("/api/costs", s.handleCosts)
	s.mux.HandleFunc("/api/costs/daily", s.handleCostsDaily)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/chat/", s.handleChat)
	s.mux.HandleFunc("/api/spawn", s.handleSpawn)
	s.mux.HandleFunc("/api/agents/available", s.handleAvailableAgents)
	s.mux.HandleFunc("/api/terminal/", s.handleTerminal)

	// Serve embedded static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("[dashboard] failed to create sub filesystem: %v", err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(staticFS)))
}

// --- API Handlers ---

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	states := s.manager.GetAllStates()

	// Enrich with cost data
	costs := s.tracker.AgentSummaries()
	for name, state := range states {
		if c, ok := costs[name]; ok {
			state["total_cost_usd"] = c.TotalCostUSD
			state["avg_cost_usd"] = c.AvgCostUSD
		}
	}

	writeJSON(w, map[string]interface{}{
		"agents":     states,
		"total_cost": s.tracker.TotalCost(),
		"machine":    s.config.Machine.Name,
		"mode":       s.config.Mode,
	})
}

func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.tracker.AgentSummaries())
}

func (s *Server) handleCostsDaily(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.tracker.DailyBreakdown())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// Return safe subset of config (no tokens/secrets)
	writeJSON(w, map[string]interface{}{
		"mode":    s.config.Mode,
		"machine": s.config.Machine.Name,
		"pools":   s.config.Pools,
		"agents":  sanitizeAgentConfigs(s.config.Agents),
		"discord": map[string]interface{}{
			"enabled": s.config.Discord.Enabled,
		},
	})
}

func sanitizeAgentConfigs(agents map[string]client.AgentConfig) map[string]interface{} {
	safe := make(map[string]interface{})
	for name, a := range agents {
		safe[name] = map[string]interface{}{
			"pool":          a.Pool,
			"machine":       a.Machine,
			"model":         a.Model,
			"role":          a.Role,
			"reports_to":    a.ReportsTo,
			"is_executive":  a.IsExecutive,
			"max_budget":    a.MaxBudgetUSD,
			"auto_spawn":    a.AutoSpawn,
			"idle_timeout":  a.IdleTimeoutSec,
			"interest_tags": a.InterestTags,
		}
	}
	return safe
}

// handleChat handles GET (fetch messages) and POST (send message) for agent chat.
// GET /api/chat/{agent} — returns relay messages to/from this agent
// POST /api/chat/{agent} — sends a message from "user" to the agent via relay
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Extract agent name from path: /api/chat/{agent}
	agent := strings.TrimPrefix(r.URL.Path, "/api/chat/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		s.handleChatGet(w, r, agent)
	case "POST":
		s.handleChatPost(w, r, agent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleChatGet(w http.ResponseWriter, r *http.Request, agent string) {
	if s.relay == nil {
		writeJSON(w, []interface{}{})
		return
	}

	// Fetch messages from relay inbox for this agent
	messages, err := s.relay.GetInbox(agent, false)
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}

	// Convert to chat format
	chatMsgs := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		role := "agent"
		if msg.From == "user" || msg.From == "human" {
			role = "user"
		}
		chatMsgs = append(chatMsgs, map[string]interface{}{
			"role":    role,
			"sender":  msg.From,
			"content": msg.Content,
			"time":    msg.CreatedAt,
			"subject": msg.Subject,
			"id":      msg.ID,
		})
	}

	writeJSON(w, chatMsgs)
}

func (s *Server) handleChatPost(w http.ResponseWriter, r *http.Request, agent string) {
	if s.relay == nil {
		http.Error(w, "relay not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Content == "" {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}

	// Determine message type
	msgType := "notification"
	if strings.HasSuffix(strings.TrimSpace(req.Content), "?") {
		msgType = "question"
	}

	// Build subject from first few words
	words := strings.Fields(req.Content)
	subject := strings.Join(words, " ")
	if len(words) > 6 {
		subject = strings.Join(words[:6], " ") + "..."
	}

	// Send via relay: from "user" to the agent
	if err := s.relay.SendMessage("user", agent, subject, req.Content, msgType, "P1"); err != nil {
		log.Printf("[dashboard] send to %s failed: %v", agent, err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "sent"})
}

// handleSpawn handles POST /api/spawn — spawns or wakes an agent.
// Body: {"agent": "cto", "machine": "clareo-station"}
// If machine matches local, spawns directly. Otherwise returns error (remote spawn TBD).
func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req struct {
		Agent   string `json:"agent"`
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	// Check if machine is local
	localMachine := s.config.Machine.Name
	targetMachine := req.Machine
	if targetMachine == "" {
		// Default to wherever the agent is configured
		if agentCfg, ok := s.config.Agents[req.Agent]; ok {
			targetMachine = agentCfg.Machine
		} else {
			targetMachine = localMachine
		}
	}

	if targetMachine != localMachine {
		// Remote spawn — send a command via relay that the target manager will pick up
		if s.relay != nil {
			err := s.relay.SendMessage("user", req.Agent, "Spawn requested",
				"Manual spawn from Mission Control dashboard", "notification", "P0")
			if err != nil {
				log.Printf("[dashboard] remote spawn %s failed: %v", req.Agent, err)
				http.Error(w, "remote spawn failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]string{"status": "sent", "note": "spawn signal sent to remote machine"})
			return
		}
		http.Error(w, "cannot spawn on remote machine: relay not configured", http.StatusServiceUnavailable)
		return
	}

	// Local spawn
	if err := s.manager.SpawnAgent(req.Agent, "manual spawn from Mission Control"); err != nil {
		log.Printf("[dashboard] spawn %s failed: %v", req.Agent, err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	writeJSON(w, map[string]string{"status": "spawning"})
}

// handleAvailableAgents returns all configured agents with their assigned machines.
func (s *Server) handleAvailableAgents(w http.ResponseWriter, r *http.Request) {
	allAgents := s.manager.AllAgentConfigs()
	states := s.manager.GetAllStates()

	result := make([]map[string]interface{}, 0, len(allAgents))
	for name, cfg := range allAgents {
		entry := map[string]interface{}{
			"name":    name,
			"machine": cfg.Machine,
			"pool":    cfg.Pool,
			"model":   cfg.Model,
			"state":   "unconfigured",
		}
		if st, ok := states[name]; ok {
			entry["state"] = st["state"]
		}
		result = append(result, entry)
	}

	// Get unique machines
	machineSet := make(map[string]bool)
	machineSet[s.config.Machine.Name] = true
	for _, cfg := range allAgents {
		if cfg.Machine != "" {
			machineSet[cfg.Machine] = true
		}
	}
	machines := make([]string, 0, len(machineSet))
	for m := range machineSet {
		machines = append(machines, m)
	}

	writeJSON(w, map[string]interface{}{
		"agents":        result,
		"machines":      machines,
		"local_machine": s.config.Machine.Name,
	})
}

// handleTerminal serves the terminal output for an agent.
// GET /api/terminal/{agent} — returns buffered stream-json output as terminal lines
// POST /api/terminal/{agent} — sends user message to agent (appends to terminal + sends via relay)
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimPrefix(r.URL.Path, "/api/terminal/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		sess := s.manager.GetSession(agent)
		if sess == nil {
			writeJSON(w, []interface{}{})
			return
		}
		writeJSON(w, sess.GetTerminalLines())

	case "POST":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var req struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Content == "" {
			http.Error(w, "content required", http.StatusBadRequest)
			return
		}

		// Append to terminal buffer
		if sess := s.manager.GetSession(agent); sess != nil {
			sess.AppendUserMessage(req.Content)
		}

		// Also send via relay to wake the agent
		if s.relay != nil {
			words := strings.Fields(req.Content)
			subject := strings.Join(words, " ")
			if len(words) > 6 {
				subject = strings.Join(words[:6], " ") + "..."
			}
			msgType := "notification"
			if strings.HasSuffix(strings.TrimSpace(req.Content), "?") {
				msgType = "question"
			}
			_ = s.relay.SendMessage("user", agent, subject, req.Content, msgType, "P1")
		}

		writeJSON(w, map[string]string{"status": "sent"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	setCORS(w)
	json.NewEncoder(w).Encode(v)
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

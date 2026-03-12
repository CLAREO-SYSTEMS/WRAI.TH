// Package dashboard serves the Mission Control web UI for the WRAI.TH client.
// Exposes fleet state, agent control, and token stats via HTTP + WebSocket.
// Supports remote agents on satellites via HTTP proxy.
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
	manager   *client.Manager
	config    *client.Config
	tracker   *monitor.Tracker
	relay     *client.RelayClient
	mux       *http.ServeMux
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
	s.mux.HandleFunc("/api/satellites/register", s.handleSatelliteRegister)
	s.mux.HandleFunc("/api/satellites", s.handleListSatellites)

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

	// Merge satellite states (satellite has real-time subprocess info)
	for machineName, sat := range s.allSatellites() {
		remote, err := s.manager.SatClient().Status(sat)
		if err != nil {
			log.Printf("[dashboard] satellite %s unreachable: %v", machineName, err)
			continue
		}
		for name, state := range remote {
			states[name] = state
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
	satNames := make([]string, 0, len(s.allSatellites()))
	for name := range s.allSatellites() {
		satNames = append(satNames, name)
	}

	writeJSON(w, map[string]interface{}{
		"mode":       s.config.Mode,
		"machine":    s.config.Machine.Name,
		"pools":      s.config.Pools,
		"agents":     sanitizeAgentConfigs(s.config.Agents),
		"satellites": satNames,
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
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
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

	messages, err := s.relay.GetInbox(agent, false)
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}

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

	msgType := "notification"
	if strings.HasSuffix(strings.TrimSpace(req.Content), "?") {
		msgType = "question"
	}

	words := strings.Fields(req.Content)
	subject := strings.Join(words, " ")
	if len(words) > 6 {
		subject = strings.Join(words[:6], " ") + "..."
	}

	if err := s.relay.SendMessage("user", agent, subject, req.Content, msgType, "P1"); err != nil {
		log.Printf("[dashboard] send to %s failed: %v", agent, err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}

	// Explicitly wake the agent — don't rely solely on SSE event propagation
	if err := s.manager.SpawnAgent(agent, "chat message from user", ""); err != nil {
		log.Printf("[dashboard] wake %s after chat failed: %v (non-fatal)", agent, err)
	}

	writeJSON(w, map[string]string{"status": "sent"})
}

// handleSpawn handles POST /api/spawn — spawns or wakes an agent.
// For local agents: spawns directly via manager.
// For remote agents: sends spawn command to the satellite via HTTP.
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

	// Manager handles local vs remote routing — pass machine override from UI
	if err := s.manager.SpawnAgent(req.Agent, "manual spawn from Mission Control", req.Machine); err != nil {
		log.Printf("[dashboard] spawn %s failed: %v", req.Agent, err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	writeJSON(w, map[string]string{"status": "spawning"})
}

// handleAvailableAgents returns all configured agents with their assigned machines.
// Includes satellite machines from config.
func (s *Server) handleAvailableAgents(w http.ResponseWriter, r *http.Request) {
	allAgents := s.manager.AllAgentConfigs()
	states := s.manager.GetAllStates()

	// Merge satellite states for accurate status
	for machineName, sat := range s.allSatellites() {
		remote, err := s.manager.SatClient().Status(sat)
		if err != nil {
			log.Printf("[dashboard] satellite %s unreachable for agent status: %v", machineName, err)
			continue
		}
		for name, state := range remote {
			states[name] = state
		}
	}

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

	// Get unique machines: local + agent configs + satellites
	machineSet := make(map[string]bool)
	machineSet[s.config.Machine.Name] = true
	for _, cfg := range allAgents {
		if cfg.Machine != "" {
			machineSet[cfg.Machine] = true
		}
	}
	for name := range s.allSatellites() {
		machineSet[name] = true
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
// For local agents: returns from session buffer.
// For remote agents: proxies to the satellite.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimPrefix(r.URL.Path, "/api/terminal/")
	if agent == "" {
		http.Error(w, "agent name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		// For remote agents, proxy to satellite (they have the real subprocess)
		if agentCfg, ok := s.config.Agents[agent]; ok && agentCfg.Machine != s.config.Machine.Name {
			if satInfo, ok := s.allSatellites()[agentCfg.Machine]; ok {
				lines, err := s.manager.SatClient().Terminal(satInfo, agent)
				if err == nil {
					writeJSON(w, lines)
					return
				}
				log.Printf("[dashboard] satellite terminal %s failed: %v", agent, err)
			}
			writeJSON(w, []interface{}{})
			return
		}

		// Local agent — use session buffer
		sess := s.manager.GetSession(agent)
		if sess != nil {
			writeJSON(w, sess.GetTerminalLines())
			return
		}

		writeJSON(w, []interface{}{})

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

		// Append to terminal buffer (local only)
		if sess := s.manager.GetSession(agent); sess != nil {
			sess.AppendUserMessage(req.Content)
		}

		// Send via relay and wake the agent
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

		// Explicitly wake the agent
		if err := s.manager.SpawnAgent(agent, "terminal message from user", ""); err != nil {
			log.Printf("[dashboard] wake %s after terminal msg failed: %v (non-fatal)", agent, err)
		}

		writeJSON(w, map[string]string{"status": "sent"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// allSatellites delegates to the manager's satellite registry.
func (s *Server) allSatellites() map[string]client.SatelliteInfo {
	return s.manager.AllSatellites()
}

// handleSatelliteRegister handles POST /api/satellites/register.
// Satellites call this on startup to announce themselves.
func (s *Server) handleSatelliteRegister(w http.ResponseWriter, r *http.Request) {
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
		Name string `json:"name"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Name == "" || req.Port == 0 {
		http.Error(w, "name and port required", http.StatusBadRequest)
		return
	}

	// If host is empty, use the request's remote address
	if req.Host == "" {
		req.Host = strings.Split(r.RemoteAddr, ":")[0]
	}

	// Delegate to manager — single source of truth for satellites
	s.manager.RegisterSatellite(req.Name, client.SatelliteInfo{Host: req.Host, Port: req.Port})
	writeJSON(w, map[string]string{"status": "registered", "name": req.Name})
}

// handleListSatellites returns all known satellites (config + live).
func (s *Server) handleListSatellites(w http.ResponseWriter, r *http.Request) {
	all := s.allSatellites()
	result := make([]map[string]interface{}, 0, len(all))
	for name, sat := range all {
		entry := map[string]interface{}{
			"name": name,
			"host": sat.Host,
			"port": sat.Port,
		}
		// Check health
		if err := s.manager.SatClient().Health(sat); err == nil {
			entry["status"] = "online"
		} else {
			entry["status"] = "offline"
		}
		result = append(result, entry)
	}
	writeJSON(w, result)
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

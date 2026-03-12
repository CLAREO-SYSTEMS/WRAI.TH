package client

import (
	"agent-relay/internal/monitor"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxCrashRetries = 5
	backoffBase     = 2 * time.Second
	backoffMax      = 60 * time.Second
)

// Manager orchestrates all agent sessions across local and satellite machines.
// Listens to relay SSE events and spawns/wakes agents as needed.
// For remote agents, commands are forwarded to satellites via HTTP.
type Manager struct {
	config   *Config
	relay    *RelayClient
	sse      *SSEClient
	sessions map[string]*Session

	satClient      *SatelliteClient
	liveSatellites map[string]SatelliteInfo
	satMu          sync.RWMutex

	tracker *monitor.Tracker

	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewManager creates a fleet manager from config.
// Creates sessions for ALL agents (local + remote) so SSE events are handled uniformly.
func NewManager(cfg *Config) *Manager {
	relay := NewRelayClient(cfg.Relay)

	m := &Manager{
		config:         cfg,
		relay:          relay,
		sessions:       make(map[string]*Session),
		satClient:      NewSatelliteClient(),
		liveSatellites: make(map[string]SatelliteInfo),
	}

	// Create sessions for ALL agents — local and remote.
	// The manager tracks state for every agent; execution is routed
	// to the correct machine (local subprocess or satellite HTTP) at spawn time.
	for name, agentCfg := range cfg.Agents {
		s := NewSession(name, agentCfg)
		s.SetRelay(cfg.Relay.URL, cfg.Relay.Project)
		m.sessions[name] = s
		location := "local"
		if agentCfg.Machine != cfg.Machine.Name {
			location = "satellite:" + agentCfg.Machine
		}
		log.Printf("[manager] registered %s (%s)", name, location)
	}

	// Restore session IDs from previous run (enables --resume after redeploy)
	m.loadSessionState()

	return m
}

// SetTracker sets the cost/token tracker.
func (m *Manager) SetTracker(t *monitor.Tracker) {
	m.tracker = t
}

// Relay returns the relay client for shared use.
func (m *Manager) Relay() *RelayClient {
	return m.relay
}

// SSE returns the SSE client (available after Start).
func (m *Manager) SSE() *SSEClient {
	return m.sse
}

// Start begins the SSE listener and health checks.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Setup SSE
	reconnect := time.Duration(m.config.SSE.ReconnectDelaySec * float64(time.Second))
	m.sse = NewSSEClient(m.config.Relay.URL, m.config.Relay.Project, m.config.Relay.APIKey, reconnect)

	// Register handlers for all event types
	m.sse.On("message", m.onMessage)
	m.sse.On("task", m.onTask)
	m.sse.On("activity", m.onActivity)
	m.sse.On("agent_status", m.onAgentStatus)
	m.sse.On("memory", m.onMemory)
	m.sse.On("register", m.onRegister)

	// Start SSE in background
	go m.sse.Run(m.ctx)

	// Start health check loop
	go m.healthLoop(m.ctx)

	// Pre-register all local agents with the relay (V0.3.1 improvement)
	// This ensures the relay knows about agents before they spawn,
	// auto-creates admin teams for executives, and passes max_context_bytes
	// for proper budget pruning.
	go m.registerAllAgents()

	// Initial inbox check — catch messages missed while offline
	go m.initialInboxCheck()

	// Count local vs remote
	localCount, remoteCount := 0, 0
	m.mu.RLock()
	for _, s := range m.sessions {
		if m.isLocalAgent(s.Name) {
			localCount++
		} else {
			remoteCount++
		}
	}
	m.mu.RUnlock()
	log.Printf("[manager] started with %d local + %d remote agents", localCount, remoteCount)
	return nil
}

// Stop gracefully shuts down all sessions.
// Gives running subprocesses up to gracePeriod to finish, then kills them.
func (m *Manager) Stop() {
	log.Printf("[manager] shutting down...")

	// Cancel the manager context (stops SSE, health loop, new spawns)
	if m.cancel != nil {
		m.cancel()
	}

	// Collect running sessions
	m.mu.RLock()
	var running []*Session
	for _, s := range m.sessions {
		if s.IsRunning() {
			running = append(running, s)
		}
	}
	m.mu.RUnlock()

	if len(running) > 0 {
		log.Printf("[manager] waiting for %d running agent(s) to finish (max 30s)...", len(running))

		// Wait up to 30 seconds for in-progress turns
		deadline := time.After(30 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

	waitLoop:
		for {
			select {
			case <-deadline:
				log.Printf("[manager] grace period expired, killing remaining subprocesses")
				for _, s := range running {
					if s.IsRunning() {
						log.Printf("[manager] killing %s", s.Name)
						s.Kill()
					}
				}
				// Brief wait for kill to take effect
				time.Sleep(time.Second)
				break waitLoop
			case <-ticker.C:
				allDone := true
				for _, s := range running {
					if s.IsRunning() {
						allDone = false
						break
					}
				}
				if allDone {
					log.Printf("[manager] all agents finished cleanly")
					break waitLoop
				}
			}
		}
	}

	// Mark all agents as sleeping in the relay
	m.mu.RLock()
	for name, s := range m.sessions {
		state := s.GetState()
		if state == StateWorking || state == StateIdle {
			if err := m.relay.SleepAgent(name); err != nil {
				log.Printf("[manager] failed to sleep %s: %v", name, err)
			}
		}
	}
	m.mu.RUnlock()

	// Persist session IDs for resume after redeploy
	m.saveSessionState()

	if m.tracker != nil {
		m.tracker.Save()
	}

	log.Printf("[manager] stopped")
}

// GetSession returns a session by name.
func (m *Manager) GetSession(name string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// GetAllStates returns state summaries for all sessions.
func (m *Manager) GetAllStates() map[string]map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make(map[string]map[string]interface{})
	for name, s := range m.sessions {
		machine := s.Config.Machine
		if machine == "" {
			machine = m.config.Machine.Name
		}
		states[name] = map[string]interface{}{
			"state":       s.GetState().String(),
			"turn_count":  s.TurnCount,
			"last_cost":   s.LastCost,
			"crash_count": s.CrashCount,
			"session_id":  s.SessionID,
			"machine":     machine,
		}
		if m.tracker != nil {
			inTok, outTok := m.tracker.AgentTokens(name)
			states[name]["tokens_used"] = inTok + outTok
			states[name]["token_limit"] = 0 // no hard limit on Max plan
		}
	}
	return states
}

// SpawnAgent triggers a spawn/wake for a named agent from external callers (e.g. dashboard).
// Returns an error if the agent is unknown or already working.
func (m *Manager) SpawnAgent(name, reason string) error {
	m.mu.RLock()
	s, ok := m.sessions[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown agent: %s", name)
	}
	state := s.GetState()
	if state == StateWorking {
		return fmt.Errorf("agent %s is already working", name)
	}
	if state == StateDead {
		return fmt.Errorf("agent %s is dead", name)
	}
	go m.spawnOrWake(s, reason)
	return nil
}

// AllAgentConfigs returns the full agent config map (all machines).
func (m *Manager) AllAgentConfigs() map[string]AgentConfig {
	return m.config.Agents
}

// MachineName returns the local machine name.
func (m *Manager) MachineName() string {
	return m.config.Machine.Name
}

// isLocalAgent returns true if the agent runs on this machine.
func (m *Manager) isLocalAgent(name string) bool {
	cfg, ok := m.config.Agents[name]
	if !ok {
		return true // unknown agents default to local
	}
	return cfg.Machine == "" || cfg.Machine == m.config.Machine.Name
}

// RegisterSatellite adds or updates a live satellite in the registry.
func (m *Manager) RegisterSatellite(name string, info SatelliteInfo) {
	m.satMu.Lock()
	m.liveSatellites[name] = info
	m.satMu.Unlock()
	log.Printf("[manager] satellite registered: %s at %s:%d", name, info.Host, info.Port)
}

// AllSatellites returns merged config + live satellites.
func (m *Manager) AllSatellites() map[string]SatelliteInfo {
	merged := make(map[string]SatelliteInfo)
	for name, sat := range m.config.Satellites {
		merged[name] = sat
	}
	m.satMu.RLock()
	for name, sat := range m.liveSatellites {
		merged[name] = sat
	}
	m.satMu.RUnlock()
	return merged
}

// resolveSatellite returns the SatelliteInfo for a machine name.
func (m *Manager) resolveSatellite(machineName string) (SatelliteInfo, bool) {
	// Check config first
	if sat, ok := m.config.Satellites[machineName]; ok {
		return sat, true
	}
	// Check live registry
	m.satMu.RLock()
	sat, ok := m.liveSatellites[machineName]
	m.satMu.RUnlock()
	return sat, ok
}

// SatClient returns the satellite HTTP client.
func (m *Manager) SatClient() *SatelliteClient {
	return m.satClient
}

// --- SSE Event Handlers ---

func (m *Manager) onMessage(evt SSEEvent) {
	to, _ := evt.Data["to"].(string)
	from, _ := evt.Data["from"].(string)
	priority, _ := evt.Data["priority"].(string)
	msgType, _ := evt.Data["type"].(string)
	if priority == "" {
		priority = "P2"
	}

	// Skip messages FROM our agents (they sent it)
	m.mu.RLock()
	_, fromOurs := m.sessions[from]
	m.mu.RUnlock()
	if fromOurs {
		return
	}

	// Filter file lock broadcasts — never wake for these
	if isFileLockBroadcast(evt) {
		return
	}

	if to == "*" {
		// Broadcast — apply priority filter
		m.mu.RLock()
		defer m.mu.RUnlock()
		for _, s := range m.sessions {
			if shouldWakeBroadcast(s.GetState(), priority) {
				go m.spawnOrWake(s, "broadcast from "+from+" ("+priority+")")
			}
		}
		return
	}

	// Direct or conversation message
	if strings.HasPrefix(to, "team:") {
		// Team message — wake team members on this machine
		teamSlug := strings.TrimPrefix(to, "team:")
		m.wakeTeamMembers(teamSlug, from, priority, msgType)
		return
	}

	m.mu.RLock()
	s, ok := m.sessions[to]
	m.mu.RUnlock()
	if !ok {
		return
	}

	if shouldWakeDirect(s.GetState(), priority) {
		go m.spawnOrWake(s, "message from "+from+" ("+priority+")")
	}
}

func (m *Manager) onTask(evt SSEEvent) {
	action := evt.Action // dispatch, claim, start, complete, block
	target := evt.Target // profile slug

	if action != "dispatch" {
		return
	}

	// Task dispatched to a profile — wake any local agent with that profile
	m.mu.RLock()
	defer m.mu.RUnlock()

	priority, _ := evt.Data["priority"].(string)
	if priority == "" {
		priority = "P2"
	}

	for name, s := range m.sessions {
		if s.Config.ProfileSlug == target {
			if shouldWakeTask(s.GetState(), priority) {
				go m.spawnOrWake(s, "task dispatched to "+target+" ("+priority+")")
			}
			_ = name
			break
		}
	}
}

func (m *Manager) onActivity(evt SSEEvent) {
	agentName, _ := evt.Data["agent_name"].(string)
	activity, _ := evt.Data["activity"].(string)

	m.mu.RLock()
	s, ok := m.sessions[agentName]
	m.mu.RUnlock()
	if !ok {
		return
	}

	switch activity {
	case "terminal", "read", "write", "thinking":
		s.SetState(StateWorking)
	case "idle", "waiting":
		s.SetState(StateIdle)
	}
}

func (m *Manager) onAgentStatus(evt SSEEvent) {
	agentName, _ := evt.Data["agent_name"].(string)
	status, _ := evt.Data["status"].(string)

	m.mu.RLock()
	s, ok := m.sessions[agentName]
	m.mu.RUnlock()
	if !ok {
		return
	}

	if status == "sleeping" {
		s.SetState(StateSleeping)
	}
}

func (m *Manager) onMemory(evt SSEEvent) {
	if evt.Action == "conflict" {
		key, _ := evt.Data["key"].(string)
		log.Printf("[manager] memory conflict detected: key=%s (agents wrote different values)", key)
	}
}

func (m *Manager) onRegister(evt SSEEvent) {
	if evt.Action != "register" {
		return
	}

	agentName := evt.Agent
	m.mu.RLock()
	_, exists := m.sessions[agentName]
	m.mu.RUnlock()

	if !exists {
		// Dynamic agent discovery — add session for any configured agent (local or remote)
		if agentCfg, ok := m.config.Agents[agentName]; ok {
			m.mu.Lock()
			s := NewSession(agentName, agentCfg)
			s.SetRelay(m.config.Relay.URL, m.config.Relay.Project)
			m.sessions[agentName] = s
			m.mu.Unlock()
			location := "local"
			if agentCfg.Machine != m.config.Machine.Name {
				location = "satellite:" + agentCfg.Machine
			}
			log.Printf("[manager] discovered new agent %s (%s)", agentName, location)
		}
	}
}

// --- Spawn / Wake ---

func (m *Manager) spawnOrWake(s *Session, reason string) {
	state := s.GetState()

	if s.CrashCount >= maxCrashRetries {
		log.Printf("[manager] %s exceeded max retries (%d), skipping", s.Name, s.CrashCount)
		return
	}

	log.Printf("[manager] waking %s: %s (state=%s)", s.Name, reason, state)

	// Route to satellite for remote agents
	if !m.isLocalAgent(s.Name) {
		m.spawnOrWakeRemote(s, reason)
		return
	}

	if (state == StateIdle || state == StateSleeping) && s.TurnCount > 0 {
		// Already spawned before — resume
		inbox, _ := m.relay.GetInbox(s.Name, true)
		prompt := BuildWakePrompt(reason, inbox)
		result, err := s.Resume(m.ctx, prompt)
		if err != nil {
			log.Printf("[manager] resume %s failed: %v", s.Name, err)
			m.retryCrash(s, prompt, true)
		}
		if m.tracker != nil && result != nil {
			m.tracker.Record(s.Name, result.Model, result.CostUSD, result.Duration, s.TurnCount, result.InputTokens, result.OutputTokens)
		}
		return
	}

	// First spawn — full boot
	sessionCtx, _ := m.relay.GetSessionContext(s.Name)
	inbox, _ := m.relay.GetInbox(s.Name, true)
	prompt := BuildBootPrompt(s.Name, s.Config, sessionCtx, inbox)

	result, err := s.Spawn(m.ctx, prompt)
	if err != nil {
		log.Printf("[manager] spawn %s failed: %v", s.Name, err)
		m.retryCrash(s, prompt, false)
	}
	if m.tracker != nil && result != nil {
		m.tracker.Record(s.Name, result.Model, result.CostUSD, result.Duration, s.TurnCount, result.InputTokens, result.OutputTokens)
	}
}

// spawnOrWakeRemote sends spawn/wake commands to a satellite for a remote agent.
func (m *Manager) spawnOrWakeRemote(s *Session, reason string) {
	machineName := s.Config.Machine
	satInfo, ok := m.resolveSatellite(machineName)
	if !ok {
		log.Printf("[manager] no satellite found for machine %s (agent %s), skipping", machineName, s.Name)
		return
	}

	isResume := s.TurnCount > 0

	// Station builds the prompt (it has relay access, satellite doesn't)
	var prompt string
	if isResume {
		inbox, _ := m.relay.GetInbox(s.Name, true)
		prompt = BuildWakePrompt(reason, inbox)
	} else {
		sessionCtx, _ := m.relay.GetSessionContext(s.Name)
		inbox, _ := m.relay.GetInbox(s.Name, true)
		prompt = BuildBootPrompt(s.Name, s.Config, sessionCtx, inbox)
	}

	spawnReq := SpawnRequest{
		Name:         s.Name,
		Config:       s.Config,
		RelayURL:     m.config.Relay.URL,
		RelayProject: m.config.Relay.Project,
		Prompt:       prompt,
		Resume:       isResume,
		Reason:       reason,
	}

	s.SetState(StateSpawning)

	if err := m.satClient.Spawn(satInfo, spawnReq); err != nil {
		log.Printf("[manager] remote spawn %s on %s failed: %v", s.Name, machineName, err)
		s.SetState(StateSleeping)
		return
	}

	log.Printf("[manager] remote spawn %s sent to satellite %s (resume=%v)", s.Name, machineName, isResume)
}

func (m *Manager) retryCrash(s *Session, prompt string, isResume bool) {
	for attempt := 1; attempt < maxCrashRetries && s.CrashCount < maxCrashRetries; attempt++ {
		// Bail if manager is shutting down
		if m.ctx.Err() != nil {
			return
		}

		delay := backoffBase * time.Duration(1<<uint(attempt-1))
		if delay > backoffMax {
			delay = backoffMax
		}

		log.Printf("[manager] retry %s in %v (attempt %d/%d)", s.Name, delay, attempt, maxCrashRetries)

		select {
		case <-m.ctx.Done():
			return
		case <-time.After(delay):
		}

		var err error
		if isResume {
			_, err = s.Resume(m.ctx, prompt)
		} else {
			_, err = s.Spawn(m.ctx, prompt)
		}

		if err == nil {
			return
		}
		log.Printf("[manager] retry %s failed: %v", s.Name, err)
	}

	log.Printf("[manager] %s gave up after %d crashes", s.Name, s.CrashCount)
	s.SetState(StateDead)
}

func (m *Manager) wakeTeamMembers(teamSlug, from, priority, msgType string) {
	pool, ok := m.config.Pools[teamSlug]
	if !ok {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	members := append(pool.Members, pool.Lead)
	for _, member := range members {
		if member == from {
			continue
		}
		if s, ok := m.sessions[member]; ok {
			if shouldWakeDirect(s.GetState(), priority) {
				go m.spawnOrWake(s, "team:"+teamSlug+" from "+from+" ("+priority+")")
			}
		}
	}
}

// --- Health Check ---

func (m *Manager) healthLoop(ctx context.Context) {
	interval := time.Duration(m.config.SSE.HealthCheckIntervalSec * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			for name, s := range m.sessions {
				state := s.GetState()
				// Idle timeout → sleep
				if state == StateIdle && s.TurnCount > 0 {
					if time.Since(s.StartedAt) > time.Duration(s.Config.IdleTimeoutSec)*time.Second {
						log.Printf("[manager] %s idle timeout, sleeping", name)
						s.SetState(StateSleeping)
						go m.relay.SleepAgent(name)
					}
				}
			}
			m.mu.RUnlock()
		}
	}
}

func (m *Manager) initialInboxCheck() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, s := range m.sessions {
		if !s.Config.AutoSpawn {
			continue
		}

		messages, err := m.relay.GetInbox(name, false)
		if err != nil {
			log.Printf("[manager] inbox check %s failed: %v", name, err)
			continue
		}

		if len(messages) > 0 {
			log.Printf("[manager] %s has %d pending messages, spawning", name, len(messages))
			go m.spawnOrWake(s, "pending messages on startup")
		}
	}
}

// registerAllAgents pre-registers local agents with the relay.
// Benefits: relay knows agents exist before first spawn, executives auto-get
// admin team membership (broadcast permissions), and max_context_bytes is
// passed for proper budget pruning.
func (m *Manager) registerAllAgents() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, s := range m.sessions {
		cfg := s.Config
		_, err := m.relay.RegisterAgent(RegisterOpts{
			Name:            name,
			Role:            cfg.Role,
			ReportsTo:       cfg.ReportsTo,
			ProfileSlug:     cfg.ProfileSlug,
			IsExecutive:     cfg.IsExecutive,
			SessionID:       s.SessionID,
			InterestTags:    cfg.InterestTags,
			MaxContextBytes: cfg.MaxContextBytes,
		})
		if err != nil {
			log.Printf("[manager] pre-register %s failed: %v (will register on first spawn)", name, err)
			continue
		}
		log.Printf("[manager] pre-registered %s (profile=%s, executive=%v)", name, cfg.ProfileSlug, cfg.IsExecutive)
	}
}

// --- Wake Decision Matrix ---

// Priority-aware wake decisions.
//
// Direct messages:
//   P0 → always wake
//   P1 → wake if idle or sleeping
//   P2 → wake only if idle
//   P3 → never wake (agent picks up next turn)
//
// Broadcasts:
//   P0 → always wake
//   P1 → wake only if idle
//   P2/P3 → never wake
//
// Tasks:
//   P0/P1/P2 → always wake
//   P3 → never wake

func shouldWakeDirect(state SessionState, priority string) bool {
	switch priority {
	case "P0", "interrupt":
		return state != StateWorking && state != StateDead
	case "P1", "steering":
		return state == StateIdle || state == StateSleeping
	case "P2", "advisory":
		return state == StateIdle
	default: // P3, info
		return false
	}
}

func shouldWakeBroadcast(state SessionState, priority string) bool {
	switch priority {
	case "P0", "interrupt":
		return state != StateWorking && state != StateDead
	case "P1", "steering":
		return state == StateIdle
	default:
		return false
	}
}

func shouldWakeTask(state SessionState, priority string) bool {
	switch priority {
	case "P0", "interrupt", "P1", "steering", "P2", "advisory":
		return state != StateWorking && state != StateDead
	default:
		return false
	}
}

// --- Session State Persistence ---

type savedState struct {
	Sessions map[string]savedSession `json:"sessions"`
}

type savedSession struct {
	SessionID string `json:"session_id"`
	TurnCount int    `json:"turn_count"`
}

func (m *Manager) stateFilePath() string {
	return filepath.Join(m.config.Machine.DownloadDir, ".wraith-sessions.json")
}

func (m *Manager) saveSessionState() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state := savedState{Sessions: make(map[string]savedSession)}
	for name, s := range m.sessions {
		if s.TurnCount > 0 {
			state.Sessions[name] = savedSession{
				SessionID: s.SessionID,
				TurnCount: s.TurnCount,
			}
		}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[manager] failed to marshal session state: %v", err)
		return
	}

	os.MkdirAll(filepath.Dir(m.stateFilePath()), 0755)
	if err := os.WriteFile(m.stateFilePath(), data, 0644); err != nil {
		log.Printf("[manager] failed to save session state: %v", err)
		return
	}
	log.Printf("[manager] saved session state for %d agent(s)", len(state.Sessions))
}

func (m *Manager) loadSessionState() {
	data, err := os.ReadFile(m.stateFilePath())
	if err != nil {
		return // no state file — fresh start
	}

	var state savedState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[manager] failed to parse session state: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	restored := 0
	for name, saved := range state.Sessions {
		if s, ok := m.sessions[name]; ok {
			s.SessionID = saved.SessionID
			s.TurnCount = saved.TurnCount
			s.SetState(StateSleeping) // was running before shutdown
			restored++
			log.Printf("[manager] restored session %s (id=%s, turns=%d)", name, saved.SessionID, saved.TurnCount)
		}
	}
	if restored > 0 {
		log.Printf("[manager] restored %d session(s) from previous run", restored)
	}
}

func isFileLockBroadcast(evt SSEEvent) bool {
	subject, _ := evt.Data["subject"].(string)
	metadata, _ := evt.Data["metadata"].(string)
	return strings.Contains(subject, "claimed") || strings.Contains(subject, "released") ||
		strings.Contains(metadata, "file_lock")
}

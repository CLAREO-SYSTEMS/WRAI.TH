package client

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	maxCrashRetries = 5
	backoffBase     = 2 * time.Second
	backoffMax      = 60 * time.Second
)

// Manager orchestrates all agent sessions on this machine.
// Listens to relay SSE events and spawns/wakes agents as needed.
type Manager struct {
	config   *Config
	relay    *RelayClient
	sse      *SSEClient
	sessions map[string]*Session

	mu      sync.RWMutex
	cancel  context.CancelFunc
}

// NewManager creates a fleet manager from config.
func NewManager(cfg *Config) *Manager {
	relay := NewRelayClient(cfg.Relay)

	m := &Manager{
		config:   cfg,
		relay:    relay,
		sessions: make(map[string]*Session),
	}

	// Create sessions for local agents
	for name, agentCfg := range cfg.LocalAgents() {
		s := NewSession(name, agentCfg)
		s.SetRelay(cfg.Relay.URL, cfg.Relay.Project)
		m.sessions[name] = s
		log.Printf("[manager] registered %s (machine=%s)", name, cfg.Machine.Name)
	}

	return m
}

// Start begins the SSE listener and health checks.
func (m *Manager) Start(ctx context.Context) error {
	ctx, m.cancel = context.WithCancel(ctx)

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
	go m.sse.Run(ctx)

	// Start health check loop
	go m.healthLoop(ctx)

	// Initial inbox check — catch messages missed while offline
	go m.initialInboxCheck()

	log.Printf("[manager] started with %d local agents", len(m.sessions))
	return nil
}

// Stop gracefully shuts down all sessions.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, s := range m.sessions {
		if s.GetState() == StateWorking || s.GetState() == StateIdle {
			if err := m.relay.SleepAgent(name); err != nil {
				log.Printf("[manager] failed to sleep %s: %v", name, err)
			}
		}
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
		states[name] = map[string]interface{}{
			"state":       s.GetState().String(),
			"turn_count":  s.TurnCount,
			"last_cost":   s.LastCost,
			"crash_count": s.CrashCount,
			"session_id":  s.SessionID,
			"machine":     m.config.Machine.Name,
		}
	}
	return states
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
		// Dynamic agent discovery — check if this agent should run on our machine
		if agentCfg, ok := m.config.Agents[agentName]; ok && agentCfg.Machine == m.config.Machine.Name {
			m.mu.Lock()
			s := NewSession(agentName, agentCfg)
			s.SetRelay(m.config.Relay.URL, m.config.Relay.Project)
			m.sessions[agentName] = s
			m.mu.Unlock()
			log.Printf("[manager] discovered new agent %s for this machine", agentName)
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

	if state == StateIdle && s.TurnCount > 0 {
		// Already spawned before — resume
		inbox, _ := m.relay.GetInbox(s.Name, true)
		prompt := BuildWakePrompt(reason, inbox)
		_, err := s.Resume(context.Background(), prompt)
		if err != nil {
			log.Printf("[manager] resume %s failed: %v", s.Name, err)
			m.retryCrash(s, prompt, true)
		}
		return
	}

	// First spawn — full boot
	ctx, _ := m.relay.GetSessionContext(s.Name)
	inbox, _ := m.relay.GetInbox(s.Name, true)
	prompt := BuildBootPrompt(s.Name, s.Config, ctx, inbox)

	_, err := s.Spawn(context.Background(), prompt)
	if err != nil {
		log.Printf("[manager] spawn %s failed: %v", s.Name, err)
		m.retryCrash(s, prompt, false)
	}
}

func (m *Manager) retryCrash(s *Session, prompt string, isResume bool) {
	for attempt := 1; attempt < maxCrashRetries && s.CrashCount < maxCrashRetries; attempt++ {
		delay := backoffBase * time.Duration(1<<uint(attempt-1))
		if delay > backoffMax {
			delay = backoffMax
		}

		log.Printf("[manager] retry %s in %v (attempt %d/%d)", s.Name, delay, attempt, maxCrashRetries)
		time.Sleep(delay)

		var err error
		if isResume {
			_, err = s.Resume(context.Background(), prompt)
		} else {
			_, err = s.Spawn(context.Background(), prompt)
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

func isFileLockBroadcast(evt SSEEvent) bool {
	subject, _ := evt.Data["subject"].(string)
	metadata, _ := evt.Data["metadata"].(string)
	return strings.Contains(subject, "claimed") || strings.Contains(subject, "released") ||
		strings.Contains(metadata, "file_lock")
}

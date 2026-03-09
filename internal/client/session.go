package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionState represents the lifecycle state of an agent session.
type SessionState int

const (
	StateIdle     SessionState = iota
	StateSpawning
	StateWorking
	StateSleeping
	StateCrashed
	StateDead
)

func (s SessionState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateSpawning:
		return "spawning"
	case StateWorking:
		return "working"
	case StateSleeping:
		return "sleeping"
	case StateCrashed:
		return "crashed"
	case StateDead:
		return "dead"
	default:
		return "unknown"
	}
}

// TurnResult captures the output of a single Claude turn.
type TurnResult struct {
	Output   string
	CostUSD  float64
	Model    string
	ExitCode int
	Duration time.Duration
	Messages []map[string]interface{} // raw stream-json messages
}

// Session manages a single agent's Claude subprocess lifecycle.
type Session struct {
	Name      string
	SessionID string
	State     SessionState
	Config    AgentConfig

	TurnCount    int
	LastCost     float64
	CrashCount   int
	StartedAt    time.Time
	relayURL     string
	relayProject string

	mu sync.Mutex
}

// NewSession creates a new agent session.
func NewSession(name string, cfg AgentConfig) *Session {
	return &Session{
		Name:      name,
		SessionID: uuid.New().String(),
		State:     StateIdle,
		Config:    cfg,
	}
}

// Spawn runs the first turn for this agent with a boot prompt.
func (s *Session) Spawn(ctx context.Context, bootPrompt string) (*TurnResult, error) {
	s.mu.Lock()
	s.State = StateSpawning
	s.StartedAt = time.Now()
	s.mu.Unlock()

	args := s.buildArgs(false)
	result, err := s.runTurn(ctx, args, bootPrompt)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.State = StateCrashed
		s.CrashCount++
		return result, err
	}

	s.TurnCount++
	s.LastCost = result.CostUSD
	s.State = StateIdle
	return result, nil
}

// Resume runs a subsequent turn using --resume.
func (s *Session) Resume(ctx context.Context, prompt string) (*TurnResult, error) {
	s.mu.Lock()
	s.State = StateWorking
	s.mu.Unlock()

	args := s.buildArgs(true)
	result, err := s.runTurn(ctx, args, prompt)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.State = StateCrashed
		s.CrashCount++
		return result, err
	}

	s.TurnCount++
	s.LastCost = result.CostUSD
	s.State = StateIdle
	return result, nil
}

// SetState updates the session state (thread-safe).
func (s *Session) SetState(state SessionState) {
	s.mu.Lock()
	s.State = state
	s.mu.Unlock()
}

// GetState returns the current state (thread-safe).
func (s *Session) GetState() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.State
}

func (s *Session) buildArgs(resume bool) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", s.Config.Model,
		"--max-budget-usd", s.Config.MaxBudgetUSD,
		"--permission-mode", "bypassPermissions",
		"--dangerously-skip-permissions",
	}

	if resume {
		args = append(args, "--resume", s.SessionID)
	} else {
		args = append(args, "--session-id", s.SessionID)
	}

	return args
}

func (s *Session) runTurn(ctx context.Context, args []string, prompt string) (*TurnResult, error) {
	start := time.Now()

	// Build MCP config temp file
	mcpFile, err := s.writeMCPConfig()
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}
	defer os.Remove(mcpFile)
	args = append(args, "--mcp-config", mcpFile)

	// Build hooks settings
	hooksJSON := s.buildHooksSettings()
	if hooksJSON != "{}" {
		args = append(args, "--settings", hooksJSON)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = s.Config.WorkDir

	// Clean env: remove CLAUDECODE to allow nesting, inject session ID
	cmd.Env = cleanEnv()
	cmd.Env = append(cmd.Env, "CLAUDE_SESSION_ID="+s.SessionID)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	log.Printf("[session:%s] spawning turn %d (resume=%v)", s.Name, s.TurnCount+1, strings.Contains(fmt.Sprint(args), "--resume"))

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Send prompt as stream-json
	input := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%s}}`, jsonString(prompt))
	fmt.Fprintln(stdin, input)
	stdin.Close()

	// Read stream-json output
	var messages []map[string]interface{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("wait: %w", err)
		}
	}

	result := &TurnResult{
		Output:   extractTextContent(messages),
		CostUSD:  extractCost(messages),
		Model:    extractModel(messages),
		ExitCode: exitCode,
		Duration: time.Since(start),
		Messages: messages,
	}

	log.Printf("[session:%s] turn %d done: cost=$%.4f, exit=%d, duration=%v",
		s.Name, s.TurnCount+1, result.CostUSD, result.ExitCode, result.Duration.Round(time.Millisecond))

	if exitCode != 0 {
		return result, fmt.Errorf("claude exited with code %d", exitCode)
	}

	return result, nil
}

func (s *Session) writeMCPConfig() (string, error) {
	// Use the relay URL from the session's relay client
	// This will be set by the manager when creating the session
	mcpConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"agent-relay": map[string]interface{}{
				"type": "http",
				"url":  fmt.Sprintf("%s/mcp?project=%s", s.relayURL, s.relayProject),
			},
		},
	}

	data, err := json.Marshal(mcpConfig)
	if err != nil {
		return "", err
	}

	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, fmt.Sprintf("wraith-mcp-%s.json", s.Name))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// SetRelay configures the relay URL for MCP config generation.
// Called by the Manager after session construction.
func (s *Session) SetRelay(url, project string) {
	s.relayURL = url
	s.relayProject = project
}

func (s *Session) buildHooksSettings() string {
	// Look for hook scripts relative to the relay binary
	home, _ := os.UserHomeDir()

	// Check standard locations
	locations := []string{
		filepath.Join(home, ".claude", "hooks"),
		filepath.Join("skill", "hooks"),
	}

	var postToolHook, stopHook string
	for _, dir := range locations {
		pt := filepath.Join(dir, "ingest-post-tool.sh")
		st := filepath.Join(dir, "ingest-stop.sh")
		if _, err := os.Stat(pt); err == nil {
			postToolHook = pt
			stopHook = st
			break
		}
	}

	if postToolHook == "" {
		return "{}"
	}

	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": postToolHook,
							"timeout": 5,
						},
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": stopHook,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(settings)
	return string(data)
}

// --- Helpers ---

func cleanEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CLAUDECODE=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func extractTextContent(messages []map[string]interface{}) string {
	var parts []string
	for _, msg := range messages {
		if m, ok := msg["message"].(map[string]interface{}); ok {
			if arr, ok := m["content"].([]interface{}); ok {
				for _, item := range arr {
					if block, ok := item.(map[string]interface{}); ok {
						if text, ok := block["text"].(string); ok {
							parts = append(parts, text)
						}
					}
				}
			}
		}
		if r, ok := msg["result"].(string); ok {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, "\n")
}

func extractCost(messages []map[string]interface{}) float64 {
	for _, msg := range messages {
		if cost, ok := msg["total_cost_usd"].(float64); ok {
			return cost
		}
	}
	return 0
}

func extractModel(messages []map[string]interface{}) string {
	for _, msg := range messages {
		if m, ok := msg["message"].(map[string]interface{}); ok {
			if model, ok := m["model"].(string); ok {
				return model
			}
		}
	}
	return ""
}

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
	Output       string
	CostUSD      float64
	Model        string
	ExitCode     int
	Duration     time.Duration
	Messages     []map[string]interface{} // raw stream-json messages
	InputTokens  int
	OutputTokens int
}

// TerminalLine represents one line of output for the dashboard terminal viewer.
type TerminalLine struct {
	Type string `json:"type"` // system, assistant, tool-use, tool-result, error, user-msg
	Text string `json:"text"`
}

const maxTerminalLines = 500

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

	cmd    *exec.Cmd // currently running subprocess (nil if idle)
	cancel context.CancelFunc // cancel for current turn's context
	mu     sync.Mutex

	termBuf   []TerminalLine // ring buffer of terminal output
	termBufMu sync.Mutex
}

// NewSession creates a new agent session.
func NewSession(name string, cfg AgentConfig) *Session {
	return &Session{
		Name:      name,
		SessionID: uuid.New().String(),
		State:     StateSleeping,
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

// Kill cancels any in-progress turn, causing the subprocess to be terminated.
func (s *Session) Kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// IsRunning returns true if a subprocess is currently active.
func (s *Session) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
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

// GetTerminalLines returns a snapshot of the terminal output buffer.
func (s *Session) GetTerminalLines() []TerminalLine {
	s.termBufMu.Lock()
	defer s.termBufMu.Unlock()
	out := make([]TerminalLine, len(s.termBuf))
	copy(out, s.termBuf)
	return out
}

// appendTermLine adds a line to the terminal buffer, evicting old entries if full.
func (s *Session) appendTermLine(typ, text string) {
	s.termBufMu.Lock()
	defer s.termBufMu.Unlock()
	if len(s.termBuf) >= maxTerminalLines {
		s.termBuf = s.termBuf[1:]
	}
	s.termBuf = append(s.termBuf, TerminalLine{Type: typ, Text: text})
}

// AppendUserMessage adds a user-sent message to the terminal buffer.
func (s *Session) AppendUserMessage(text string) {
	s.appendTermLine("user-msg", "[you] "+text)
}

func (s *Session) classifyTermLine(msg map[string]interface{}) {
	msgType, _ := msg["type"].(string)

	switch msgType {
	case "system":
		if text, ok := msg["message"].(string); ok {
			s.appendTermLine("system", text)
		}

	case "assistant":
		// Assistant message with content blocks
		if m, ok := msg["message"].(map[string]interface{}); ok {
			if content, ok := m["content"].([]interface{}); ok {
				for _, block := range content {
					b, ok := block.(map[string]interface{})
					if !ok {
						continue
					}
					blockType, _ := b["type"].(string)
					switch blockType {
					case "text":
						if text, ok := b["text"].(string); ok && text != "" {
							s.appendTermLine("assistant", text)
						}
					case "tool_use":
						name, _ := b["name"].(string)
						inputJSON, _ := json.Marshal(b["input"])
						s.appendTermLine("tool-use", fmt.Sprintf("Tool: %s %s", name, string(inputJSON)))
					}
				}
			}
		}

	case "tool_result", "content_block_start":
		// Tool result
		if m, ok := msg["message"].(map[string]interface{}); ok {
			if content, ok := m["content"].([]interface{}); ok {
				for _, block := range content {
					b, ok := block.(map[string]interface{})
					if !ok {
						continue
					}
					if text, ok := b["text"].(string); ok && text != "" {
						s.appendTermLine("tool-result", text)
					}
				}
			}
		}

	case "error":
		if text, ok := msg["error"].(string); ok {
			s.appendTermLine("error", text)
		} else if m, ok := msg["message"].(string); ok {
			s.appendTermLine("error", m)
		}

	case "result":
		// Final result — extract token usage
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			if in, ok := usage["input_tokens"].(float64); ok {
				s.mu.Lock()
				// Store for later retrieval — we'll use a side channel
				s.mu.Unlock()
				_ = in
			}
		}
	}
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

	// Derive a cancelable context so Stop() can kill mid-turn
	turnCtx, turnCancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(turnCtx, "claude", args...)
	cmd.Dir = s.Config.WorkDir

	// Track running subprocess for graceful shutdown
	s.mu.Lock()
	s.cmd = cmd
	s.cancel = turnCancel
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.cmd = nil
		s.cancel = nil
		s.mu.Unlock()
	}()

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
	s.appendTermLine("system", fmt.Sprintf("[session:%s] spawning turn %d (resume=%v)", s.Name, s.TurnCount+1, strings.Contains(fmt.Sprint(args), "--resume")))

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

		// Classify and buffer for terminal viewer
		s.classifyTermLine(msg)
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

	inTok, outTok := extractTokens(messages)

	result := &TurnResult{
		Output:       extractTextContent(messages),
		CostUSD:      extractCost(messages),
		Model:        extractModel(messages),
		ExitCode:     exitCode,
		Duration:     time.Since(start),
		Messages:     messages,
		InputTokens:  inTok,
		OutputTokens: outTok,
	}

	log.Printf("[session:%s] turn %d done: cost=$%.4f, exit=%d, duration=%v",
		s.Name, s.TurnCount+1, result.CostUSD, result.ExitCode, result.Duration.Round(time.Millisecond))

	s.appendTermLine("system", fmt.Sprintf("[session:%s] turn %d done: exit=%d, duration=%v", s.Name, s.TurnCount+1, exitCode, time.Since(start).Round(time.Millisecond)))
	if exitCode == 0 {
		s.appendTermLine("system", fmt.Sprintf("[session:%s] state -> idle", s.Name))
	} else {
		s.appendTermLine("system", fmt.Sprintf("[session:%s] state -> crashed", s.Name))
	}

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

func extractTokens(messages []map[string]interface{}) (input, output int) {
	for _, msg := range messages {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			if in, ok := usage["input_tokens"].(float64); ok {
				input += int(in)
			}
			if out, ok := usage["output_tokens"].(float64); ok {
				output += int(out)
			}
		}
	}
	return
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

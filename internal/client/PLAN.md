# WRAI.TH Go Client — Implementation Plan

## What Is This

Go rewrite of the Python client. Embeds into the relay binary — single executable, no Python runtime.

Replaces: `client/src/` (Python, ~4000 lines, 23 files)

```
WRAI.TH/
├── main.go                    ← relay entry (add --station/--satellite flags)
├── internal/relay/            ← existing MCP server + API
├── internal/client/           ← NEW — Go client (this plan)
│   ├── PLAN.md                ← you are here
│   ├── testcases/             ← integration tests (done, 7/7 pass)
│   ├── config.go
│   ├── session.go
│   ├── manager.go
│   ├── sse.go
│   ├── relay.go
│   └── boot.go
├── internal/discord/          ← NEW — Discord bridge
├── internal/monitor/          ← NEW — token tracking
├── internal/dashboard/        ← NEW — Mission Control
└── skill/hooks/               ← existing hook scripts
```

---

## Key Architectural Difference from Python

### Python approach (broken)
```
spawn claude (interactive) → keep alive → pipe /relay talk to stdin → read stdout
```
Interactive TUI doesn't flush stdout to pipes. Multi-turn unreliable.

### Go approach (validated by tests)
```
spawn claude -p --session-id <uuid> → stdin prompt → structured JSON out → exit
next message → spawn claude -p --resume <uuid> → stdin prompt → JSON out → exit
```

Each turn is **atomic**. Process starts, does work, exits. Session state persists in Claude's storage. Validated in `testcases/session_test.go` (7/7 pass).

### Spawn flags (every turn)
```
claude -p
  --session-id <uuid>              # first turn (or --resume <uuid> for subsequent)
  --output-format stream-json
  --verbose
  --model sonnet
  --max-budget-usd <per-turn-cap>
  --permission-mode bypassPermissions
  --dangerously-skip-permissions
  --mcp-config <relay.json>        # injected per-agent
  --settings <hooks.json>          # activity tracking hooks
  --no-session-persistence         # optional: don't save to ~/.claude/
```

### Stream-JSON protocol
```
Input:  {"type":"user","message":{"role":"user","content":"..."}}
Output: line-delimited JSON:
  [0] init      — config, MCP servers, version
  [1] message   — msg.message.content[].text
  [2] rate_limit — rate limit info
  [3] result    — msg.result, msg.total_cost_usd, msg.modelUsage
```

---

## V0.3 Smart Messaging Integration

The relay now has priority messaging, TTL, budget pruning, delivery tracking, and file locks. The client must leverage these:

### Boot sequence (per agent)
```
1. Load agent config (interest_tags, max_context_bytes, profile_slug)
2. Call relay: register_agent(name, role, interest_tags, max_context_bytes, session_id)
3. Call relay: get_session_context → profile + tasks + inbox + memories
4. Call relay: get_inbox(as=name, apply_budget=true) → curated messages
5. Build boot prompt with context
6. Spawn claude -p --session-id <uuid> with boot prompt
7. Parse output, extract cost
8. Agent is now registered and has processed its inbox
```

### Priority-aware wake
```
P0 (interrupt)  → wake immediately, even from sleeping
P1 (steering)   → wake if idle or sleeping
P2 (advisory)   → wake only if idle
P3 (info)       → don't wake, agent picks it up next turn
```

### Message filtering
```
file_lock broadcasts → never wake (informational only)
TTL-expired messages → relay filters these server-side
Budget pruning       → relay scores by priority × tag relevance × freshness
```

### Delivery tracking
```
Discord bridge sends message → call ack_delivery(message_id, as=human_slug)
```

---

## Phase 1 — Core (fixes 13/18 gaps)

### config.go

Config loader with YAML + env var interpolation. Extends Python config with V0.3 fields.

```go
type Config struct {
    Mode        string                    // "station" or "satellite"
    Relay       RelayConfig
    Machine     MachineConfig
    Web         WebConfig
    StdoutAPI   StdoutAPIConfig
    Discord     DiscordConfig
    Satellites  map[string]SatelliteInfo
    Pools       map[string]PoolConfig
    Humans      map[string]HumanConfig
    Agents      map[string]AgentConfig
    SSE         SSEConfig
    Tokens      TokenConfig
}

type AgentConfig struct {
    ProfileSlug      string
    WorkDir          string
    Machine          string
    Pool             string
    IdleTimeoutSec   int
    AutoSpawn        bool
    // V0.3 additions
    InterestTags     []string              // for budget pruning
    MaxContextBytes  int                   // for get_inbox(apply_budget=true)
    MaxBudgetUSD     string                // per-turn cost cap
    Model            string                // default: "sonnet"
}
```

**Gaps fixed:** Foundation for G3 (interest_tags, max_context_bytes), G4 (auth key in RelayConfig)

### relay.go

HTTP client wrapping relay REST + MCP calls. Every request includes auth header.

```go
type RelayClient struct {
    baseURL    string
    project    string
    apiKey     string              // G4: always sent as Bearer token
    httpClient *http.Client
}

// Key methods:
func (r *RelayClient) RegisterAgent(opts RegisterOpts) (*SessionContext, error)
func (r *RelayClient) GetSessionContext(agent string) (*SessionContext, error)   // G3
func (r *RelayClient) GetInbox(agent string, applyBudget bool) ([]Message, error)
func (r *RelayClient) SleepAgent(agent string) error                            // E1
func (r *RelayClient) AckDelivery(agent, messageID string) error                // V0.3
func (r *RelayClient) SendMessage(opts SendOpts) error
func (r *RelayClient) QueryContext(agent, query string) (*Context, error)       // G9
```

**Gaps fixed:** G4 (auth header), G3 (session context), E1 (sleep_agent), G9 (query_context)

### sse.go

SSE consumer on `/api/activity/stream`. Handles ALL event types.

```go
type SSEClient struct {
    url      string
    handlers map[string][]EventHandler
}

// Event types handled:
// - "message"        → wake logic (priority-aware)
// - "task"           → wake on dispatch/claim (G2)
// - "activity"       → update session state (busy/idle)
// - "agent_status"   → sleeping/deactivated
// - "memory_conflict" → log warning, optionally notify (G13)
// - "register"       → dynamic agent discovery (E5)
```

**Gaps fixed:** G2 (task-driven wake), G13 (memory conflict), E5 (dynamic discovery)

### session.go

Claude subprocess lifecycle. One session per agent.

```go
type Session struct {
    Name       string
    SessionID  string              // UUID, persists across turns
    State      SessionState        // idle, spawning, working, sleeping, crashed, dead
    Config     AgentConfig
    TurnCount  int
    LastCost   float64
    CrashCount int
}

func (s *Session) Spawn(ctx context.Context, bootPrompt string) (*TurnResult, error)
func (s *Session) Resume(ctx context.Context, prompt string) (*TurnResult, error)
func (s *Session) BuildMCPConfig(relayURL string) string    // temp file with relay MCP
func (s *Session) BuildHooksSettings() string                // G1: hook injection
func (s *Session) BuildBootPrompt(context *SessionContext) string  // G3, E6: rich boot

type TurnResult struct {
    Output    string
    CostUSD   float64
    Model     string
    ExitCode  int
    Duration  time.Duration
}
```

Spawn flow:
```
1. Write temp MCP config file (relay URL + project)
2. Write temp hooks settings JSON (PostToolUse, Stop scripts)
3. Build command: claude -p --session-id/--resume ...
4. Set env: CLAUDE_SESSION_ID=<uuid>, unset CLAUDECODE
5. Pipe prompt via stdin (stream-json format)
6. Parse stdout line by line (stream-json)
7. Collect TurnResult (content, cost, exit code)
8. Clean up temp files
```

**Gaps fixed:** G1 (hooks), G3 (boot context), E6 (rich boot prompt)

### manager.go

Fleet orchestrator. SSE → spawn/wake decisions.

```go
type Manager struct {
    config   Config
    relay    *RelayClient
    sse      *SSEClient
    sessions map[string]*Session
}

func (m *Manager) Start(ctx context.Context) error
func (m *Manager) Stop() error
func (m *Manager) OnMessage(evt SSEEvent)          // priority-aware wake
func (m *Manager) OnTask(evt SSEEvent)              // G2: task-driven wake
func (m *Manager) OnRegister(evt SSEEvent)          // E5: dynamic discovery
func (m *Manager) OnMemoryConflict(evt SSEEvent)    // G13: log/notify
```

Wake decision matrix:
```
┌──────────────┬────────┬──────────┬─────────┬──────┐
│ Event        │ P0     │ P1       │ P2      │ P3   │
├──────────────┼────────┼──────────┼─────────┼──────┤
│ Direct msg   │ WAKE   │ WAKE     │ if idle │ skip │
│ Broadcast    │ WAKE   │ if idle  │ skip    │ skip │
│ Task dispatch│ WAKE   │ WAKE     │ WAKE    │ skip │
│ File lock    │ skip   │ skip     │ skip    │ skip │ ← G18
│ Conversation │ WAKE   │ WAKE     │ if idle │ skip │
└──────────────┴────────┴──────────┴─────────┴──────┘
```

Crash recovery (iterative, not recursive — E3):
```go
for attempt := 0; attempt < maxRetries; attempt++ {
    err := session.Spawn(ctx, prompt)
    if err == nil { break }
    delay := min(baseDelay * (1 << attempt), maxDelay)
    time.Sleep(delay)
}
```

**Gaps fixed:** G2, G18, E2, E3, E5

### boot.go

Constructs the boot prompt from session context.

```go
func BuildBootPrompt(agent string, ctx *SessionContext, inbox []Message) string
```

Template:
```
You are agent "{name}" ({role}).

## Session Context
Profile: {profile_slug}
Reports to: {reports_to}

## Pending Tasks ({count})
{task_list}

## Unread Messages ({count}, budget-pruned)
{message_summaries}

## Relevant Memories
{memories}

Register with the relay, then process your inbox with /relay talk.
When done, stay ready — you'll be resumed with --resume for the next turn.
```

**Gaps fixed:** G3, E6

---

## Phase 2 — Discord Bridge (`internal/discord/`)

Uses [discordgo](https://github.com/bwmarrin/discordgo) instead of discord.py.

### bot.go
- Connect to Discord gateway
- Route messages to command handler
- SSE listener for outbound relay→Discord

### commands.go
- Parse `/agentname message` format
- Map Discord user → relay profile
- Validate team access via relay teams (G6), not client-side pools
- File attachment download

### router.go
- SSE-driven outbound (E4: no more polling)
- Route by conversation → Discord thread (G11)
- Cross-team conversations → #ops-pool thread (Option A)
- Call ack_delivery on successful Discord send (V0.3)
- Filter: only forward messages TO humans, not agent-to-agent

### onboarding.go
- Unknown Discord user → notify CTO agent
- CTO asks Charles → creates profile

**Gaps fixed:** G6, G11, G12, E4, E7

---

## Phase 3 — Monitor + Dashboard

### internal/monitor/
- Token tracker: parse `total_cost_usd` from stream-json result messages
- Store in relay's existing SQLite (merge into `internal/db/`)
- Per-agent, per-turn, daily aggregation

### internal/dashboard/
- Embed Mission Control as `go:embed` static files
- HTTP handlers for fleet state, agent control, token stats
- WebSocket for live stdout streaming
- Satellite stdout proxy

---

## Phase 4 — Integration

### main.go changes
```go
// Add flags
--station       // run relay + client + discord + dashboard
--satellite     // run relay client + session controller only
--config        // path to config.yaml

// Boot order (station):
1. Load config
2. Start relay (existing)
3. Start relay HTTP client
4. Start SSE listener
5. Start fleet manager
6. Register humans in relay
7. Start Discord bridge
8. Start dashboard server
9. Initial inbox check → spawn agents with pending work
```

### install.sh / install.ps1
- `--station`: build with all tags, deploy config template
- `--satellite`: build with client tags only, connect to remote relay

---

## Config Changes from Python

```yaml
# NEW fields (V0.3)
agents:
  cto:
    profile_slug: "cto"
    work_dir: "/path/to/monorepo"
    machine: "server"
    pool: "ops"
    idle_timeout_seconds: 300
    auto_spawn: true
    interest_tags: ["architecture", "team", "ops", "process"]     # NEW
    max_context_bytes: 12288                                       # NEW
    max_budget_usd: "0.50"                                         # NEW: per-turn cap
    model: "sonnet"                                                # NEW: per-agent model

relay:
  url: "http://localhost:8090"
  project: "default"
  api_key: "${RELAY_API_KEY}"                                      # NEW: auth header
```

---

## Gap Coverage Summary

| Gap | Phase | How |
|-----|-------|-----|
| G1 hooks | 1 | session.go injects --settings with hook scripts |
| G2 task wake | 1 | sse.go + manager.go handle task events |
| G3 boot context | 1 | boot.go + relay.go get_session_context |
| G4 auth | 1 | relay.go sends Bearer token |
| G6 teams | 2 | commands.go validates via relay teams |
| G9 query_context | 1 | relay.go preloads context |
| G11 conversations | 2 | router.go maps to Discord threads |
| G12 notify channels | 2 | wired when teams are configured |
| G13 memory conflict | 1 | sse.go handles memory_conflict events |
| G18 file locks | 1 | manager.go filters lock broadcasts |
| E1 sleep_agent | 1 | session idle → relay.SleepAgent() |
| E2 smart broadcast | 1 | priority matrix + server-side budget |
| E3 crash loop | 1 | manager.go iterative backoff |
| E4 SSE not poll | 2 | router.go uses SSE stream |
| E5 dynamic agents | 1 | sse.go handles register events |
| E6 boot prompt | 1 | boot.go rich prompt |
| E7 conversation wake | 2 | manager.go resolves conversation members |

Phase 1: 13/18 gaps
Phase 2: 5/18 gaps
Phase 3-4: 0 gaps (polish + integration)

---

## Test Strategy

### Existing (done)
- `testcases/session_test.go` — 7 integration tests validating subprocess control

### Phase 1 additions
- `config_test.go` — YAML parsing, env interpolation, local_agents filter
- `session_test.go` (unit) — boot prompt construction, MCP config generation, hooks settings
- `manager_test.go` — wake decision matrix (mock SSE events → assert wake/skip)
- `sse_test.go` — event parsing, handler dispatch
- `relay_test.go` — HTTP client with httptest mock server

### Phase 2 additions
- `discord/router_test.go` — conversation→thread routing, cross-team→ops rule
- `discord/commands_test.go` — command parsing, team access validation

---

## What This Does NOT Do

- Does NOT replace the relay — it's the orchestration layer on top
- Does NOT replace the relay web UI — Mission Control is for agent fleet management
- Does NOT run LLM for routing — pure Go forwarding (zero tokens)
- Does NOT manage infrastructure (Docker, servers, deployments)
- Does NOT handle trading operations (that's the bots)
- Does NOT replace the Python client immediately — coexists until fully validated

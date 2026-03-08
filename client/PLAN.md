# WRAI.TH Client — Architecture Plan

## What Is This

The Python client layer of WRAI.TH. Integrated into the same repo as the Go relay.
Adds agent session management, Discord bridge, and Mission Control UI on top of the relay.

```
WRAI.TH/
├── main.go, internal/     ← Go relay (existing)
├── client/                ← Python client (this)
│   ├── src/               ← source code
│   ├── config.example.yaml
│   └── PLAN.md            ← you are here
├── skill/                 ← /relay skill (existing)
└── install.sh             ← deploys both relay + client
```

---

## Deployment Modes

### Station Mode (relay + client)

The mothership. Runs the full stack on one machine.

```bash
./install.sh --station
```

Deploys:
- Go relay binary (:8090)
- Python client (:8091) with ALL components:
  - Session controller (manages local agents)
  - Discord bridge (one globally, runs here)
  - Mission Control UI (full dashboard)
  - Token monitor

Config: `relay.url: "http://localhost:8090"`

### Satellite Mode (client only)

Remote outpost. Manages agents on a secondary machine, connects back to the station.

```bash
./install.sh --satellite --station-url http://192.168.0.42:8090
```

Deploys:
- Python client only (:8092) with:
  - Session controller (manages THIS machine's agents)
  - Stdout API (so station UI can stream remote agent output)
  - Token monitor (local tracking)
  - Discord bridge: OFF
  - Mission Control UI: OFF (station UI handles it)

Config: `relay.url: "http://192.168.0.42:8090"` (remote station)

### How They Connect

```
Station (server)                        Satellite (charles-pc)
+-------------------------------+       +---------------------------+
| Relay (:8090)                 |       |                           |
| Client (:8091)                |       | Client (:8092)            |
|   Session Ctrl (local agents) |       |   Session Ctrl (local)    |
|   Discord Bridge              |  SSE  |   Stdout API              |
|   Mission Control UI ◄--------+-------+   Token Monitor           |
|   Token Monitor               |       |                           |
+-------------------------------+       +---------------------------+
        │                                        │
        │         ┌──────────────┐               │
        └────────►│  Agent Relay │◄──────────────┘
                  │  (Go, :8090) │
                  └──────────────┘
```

The relay is the rendezvous point. Satellites don't know about each other,
only about the station's relay URL. The station UI reads fleet state from
the relay (all agents, all machines) and connects to satellite stdout APIs
for remote agent output.

### Multi-Machine Agent Awareness

Each client instance registers its agents with `machine` metadata:
- `register_agent(name: "cto", ..., description: "machine:charles-pc")`
- The relay stores this, the station UI reads it and groups agents by machine
- Config defines which agents run where — no overlap allowed

```
Mission Control UI
+================================================================+
| server (station) ● connected                                    |
|   [cto ●]  [quant-lead ○]                                     |
|                                                                 |
| charles-pc (satellite) ● connected                              |
|   [backend-dev ●]  [quant-researcher ○]                        |
|                                                                 |
| gpu-box (satellite) ✗ offline                                   |
|   [ml-trainer ✗]                                               |
+================================================================+
```

---

## System Architecture

```
                    +------------------+
                    |  Discord Server  |
                    |  #quant-pool     |
                    |  #dev-pool       |
                    |  #ops-pool       |
                    +--------+---------+
                             |
                      discord.py bot
                             |
+----------------------------v-----------------------------+
|                  WRAI.TH CLIENT (Python)                  |
|                                                          |
|  +---------------+  +---------------+  +---------------+ |
|  | Discord       |  | Session       |  | Mission       | |
|  | Bridge        |  | Controller    |  | Control UI    | |
|  | (forwarder)   |  | (subprocess)  |  | (FastAPI)     | |
|  +-------+-------+  +-------+-------+  +-------+-------+ |
|          |                  |                   |         |
|          +------------------+-------------------+         |
|                             |                             |
|                    +--------v--------+                    |
|                    | Relay Client    |                    |
|                    | (httpx async)   |                    |
|                    +--------+--------+                    |
|                             |                             |
|                    +--------v--------+                    |
|                    | Token Monitor   |                    |
|                    | (per-agent)     |                    |
|                    +-----------------+                    |
+----------------------------+-----------------------------+
                             |
                      HTTP REST + MCP
                             |
                    +--------v--------+
                    | WRAI.TH Relay   |
                    | (Go, :8090)     |
                    +-----------------+
                             |
                    Claude Code sessions
```

Station mode runs all components. Satellite mode runs only Session Controller,
Stdout API, and Token Monitor.

---

## Component Details

### 1. Relay Client (`relay/client.py`)

Single point of contact with the relay. Wraps all HTTP calls.

**Relay API used:**

| Endpoint / Tool              | Used By      | Purpose                            |
|-----------------------------|--------------|------------------------------------|
| `register_agent`            | Bridge, Ctrl | Register humans + wake agents      |
| `send_message`              | Bridge, UI   | Forward messages in both directions |
| `get_inbox`                 | Bridge, Ctrl | Fallback polling (if SSE misses)   |
| `mark_read`                 | Bridge, Ctrl | Mark forwarded/processed messages  |
| `list_agents`               | UI, Bridge   | Fleet status, validate targets     |
| `sleep_agent`               | Ctrl         | Mark agent sleeping                |
| `register_profile`          | Bridge       | Create profiles for new humans     |
| `get_profile`               | Ctrl         | Load agent soul on spawn           |
| `list_conversations`        | UI           | Show conversations in dashboard    |
| `GET /api/activity/stream`  | Ctrl, UI     | SSE — primary real-time event source |

**Design**: REST for reads, MCP `tools/call` for writes (supports `as` parameter).

**Event-driven, not polling:**
The client maintains ONE persistent SSE connection to `/api/activity/stream`.
This stream emits events for all state changes: new messages, agent activity
(tool calls, typing, idle), status changes. The client uses this as the
primary trigger for all actions:

- New message for agent X → wake/spawn agent X
- New message for human Y → forward to Discord
- Agent X calling tools → status = busy
- Agent X idle (no tool calls for N seconds) → status = idle, ready for next command
- Agent X disconnected → status = offline

Polling `get_inbox` is only used as a fallback on SSE reconnect to catch
any events missed during disconnection.

### 2. Session Controller (`controller/`)

Manages Claude Code CLI processes. Runs in both station and satellite modes.

**Lifecycle:**
```
IDLE ──(unread detected)──> SPAWNING ──> WORKING ──(no msgs)──> IDLE
                               ^              |
                               └──(crash)─────┘  (backoff, max 5 retries)
```

**Key behaviors:**
- Listens to relay SSE stream for new message events
- On new message for agent: if agent idle → pipe `/relay talk` to stdin. If no process → spawn first.
- Captures stdout/stderr in ring buffer → streams to web UI via WebSocket
- Agent process stays alive between tasks (no kill/respawn cycle)
- On idle timeout: marks `sleep_agent` in relay, keeps process warm or kills based on config
- On crash: auto-restart with exponential backoff (2s, 4s, 8s... max 60s, max 5 retries)
- Only manages agents configured for THIS machine

**Agent state detection (via relay, not stdout parsing):**
The relay tracks agent activity through its MCP session hooks. The client
reads state from the SSE stream:

- Agent calling MCP tools (read, edit, bash, etc.) → **busy**
- Agent calling `get_inbox` → **in talk loop, checking for work**
- No tool calls for N seconds → **idle, ready for next command**
- Process exited → **offline**

The client does NOT parse Claude's stdout to determine state. The relay
is the single source of truth for agent activity.

**Spawn command:**
```
claude (interactive mode)
  cwd: agent.work_dir
  stdin: piped (for sending commands like /relay talk)
  stdout/stderr: captured in ring buffer
  env: CLAUDE_SESSION_ID
```

**Boot prompt (piped to stdin on first spawn):**
```
You are agent "{profile_slug}". Register with the relay using your profile,
then run /relay talk to process your inbox. When done, stay ready for
the next /relay talk command.
```

**Wake-up (piped to stdin when new message arrives and agent is idle):**
```
/relay talk
```

The agent's soul (from profile) handles everything else: role, reports_to, behavior.

### 3. Discord Bridge (`discord_bridge/`)

Pure forwarder. No LLM. No intelligence. **Station mode only.**

**Inbound (Discord → Relay):**
```
Human types: /cto fix the auth bug
  → bot parses: target="cto", content="fix the auth bug"
  → lookup sender discord_id → relay profile slug (e.g. "charles")
  → send_message(as: "charles", to: "cto", content: "fix the auth bug")
```

**Outbound (Relay → Discord):**
```
SSE event: new message from "cto" to "charles"
  → bridge receives event
  → routes to #ops-pool (cto's configured channel)
  → queues Discord embed (sequential send queue)
  → marks read in relay
```

**Discord send queue:**
All outbound Discord messages go through a single sequential queue.
One message sent at a time, respects Discord rate limits.
Prevents burst issues when multiple agents post simultaneously.

**Command format:**
```
/agentname message content here
```
Simple. No subcommands. The human is either answering or requesting —
doesn't matter, it's just a message.

**File uploads:**
- Humans can attach `.txt` or `.md` files in Discord
- Bridge downloads them to the station machine (configurable dir)
- Includes file path in the relay message content so the agent can read it

**Question styling:**
- When an agent sends a `user_question` type message → Discord embed with distinct color/border
- Makes it visually clear this needs a response
- Human replies in the same channel → bridge catches it and routes back

**Pool channels:**
```
#quant-pool  — quant-lead, quant-researcher, quant-backtester post here
#dev-pool    — backend-dev, (future: frontend, devops) post here
#ops-pool    — cto posts here, operational decisions, leadership comms
```

**Posting rules:**
- Leads post freely to their pool channel
- Employees (researchers, backtesters) post updates ONLY when lead requests it
- This is enforced by the agent's soul/profile, not by the client
- CTO posts to #ops-pool

**Human onboarding flow:**
```
New Discord user types /cto hello
  → bridge checks: does profile exist for this discord_id?
  → NO → bridge notifies CTO agent:
      "New user [username] (discord_id: X) wants to interact.
       Please register them and ask charles which pool they can access."
  → CTO sends user_question to charles:
      "New user [username] wants access. Which pool? (quant/dev/ops)"
  → Charles replies in Discord: "quant"
  → CTO creates profile with allowed pools
  → CTO welcomes new user
```

### 4. Mission Control Web UI (`web/`)

Local dashboard. Retro/pixel-art aesthetic matching relay. **Station mode only.**

**Fleet overview:**
- All agents at a glance: name, status indicator, machine, uptime
- Grouped by machine with connection status
- Humans shown with distinct marker (star vs circle)
- Click agent → opens tab

**Per-agent tab:**
- Left panel: relay messages (sent/received)
- Right panel: live stdout from Claude session (WebSocket stream)
- Status bar: state, PID, machine, uptime, token usage
- Controls: [Start] [Stop] [Restart]
- Input field: type message to send through this agent manually

**Remote agent stdout:**
- Local agents → stdout streamed directly from local ring buffer
- Remote agents (on satellite) → UI connects to satellite's stdout API
- If satellite unreachable → shows "stdout unavailable" but still shows relay messages

**Tech:**
- FastAPI backend + WebSocket for live streaming
- Vanilla JS + Canvas 2D frontend (no framework)
- ES modules, no build step

### 5. Token Monitor (`monitor/`)

Tracks Claude API credit usage per agent. Runs in both station and satellite modes.

**What it tracks:**
- Tokens consumed per agent per session
- Tokens consumed per agent per day
- Running total per agent
- Cost estimates

**How:**
- Parses Claude CLI output for token usage info
- Or reads from Claude API usage endpoint if available
- Stores in local SQLite

**Dashboard integration (station mode):**
- Per-agent token badge in fleet overview
- Token usage chart in agent tab
- Daily/weekly aggregation
- Satellite reports token data to station via relay metadata

**Active path — what Python handles without Claude (zero tokens):**
- Message routing: Discord ↔ relay forwarding
- Inbox detection: SSE stream listening, wake triggers
- Human onboarding check: profile exists? (yes/no lookup)
- Status queries: "is agent X alive?" → check local process state
- File downloads: Discord attachments → local disk
- Token usage tracking and aggregation
- Health checks: process alive? relay reachable?
- Discord send queue management
- Rate limiting and backoff logic

**What requires Claude (costs tokens):**
- Actual work: code, analysis, research, debugging
- Responding to questions with substance
- Onboarding conversation (CTO deciding pool assignment)
- Any task requiring judgment, creativity, or codebase knowledge
- Relay memory/soul operations (agent manages its own memory)

Monitor tracks "messages handled by Python vs Claude" ratio.

---

## Configuration

```yaml
# config.yaml

mode: "station"  # "station" or "satellite"

relay:
  url: "http://localhost:8090"       # localhost for station, remote for satellite
  project: "default"

machine:
  name: "server"                     # this instance's identity
  download_dir: "./data/downloads"

# --- Station-only settings ---

web:
  port: 8091
  host: "0.0.0.0"

discord:
  enabled: true                      # false for satellite
  token: "${DISCORD_TOKEN}"
  guild_id: "123456789"
  channels:
    quant-pool: "channel_id_1"
    dev-pool: "channel_id_2"
    ops-pool: "channel_id_3"

satellites:                           # known satellites for stdout streaming
  charles-pc:
    host: "192.168.0.10"
    port: 8092
  gpu-box:
    host: "192.168.0.50"
    port: 8092

# --- Satellite-only settings ---

stdout_api:
  port: 8092                         # exposes stdout WebSocket for station UI

# --- Shared settings ---

pools:
  quant:
    channel: "quant-pool"
    lead: "quant-lead"
    members: ["quant-researcher", "quant-backtester"]
  dev:
    channel: "dev-pool"
    lead: "backend-dev"
    members: []
  ops:
    channel: "ops-pool"
    lead: "cto"
    members: []

humans:
  charles:
    name: "Charles"
    role: "Founder"
    discord_id: "123456789"
    is_executive: true
    pools: ["quant", "dev", "ops"]

agents:
  cto:
    profile_slug: "cto"
    work_dir: "C:/Users/Charles/Desktop/projet2025/CLAREO_ecosystem/monorepo"
    machine: "server"
    pool: "ops"
    idle_timeout_seconds: 300
    auto_spawn: true

  quant-lead:
    profile_slug: "quant-lead"
    work_dir: "C:/Users/Charles/Desktop/projet2025/CLAREO_ecosystem/monorepo"
    machine: "server"
    pool: "quant"
    idle_timeout_seconds: 120
    auto_spawn: true

  backend-dev:
    profile_slug: "backend-dev"
    work_dir: "C:/Users/Charles/Desktop/projet2025/CLAREO_ecosystem/monorepo"
    machine: "charles-pc"
    pool: "dev"
    idle_timeout_seconds: 60
    auto_spawn: true

sse:
  reconnect_delay_seconds: 3
  fallback_poll_seconds: 10
  health_check_interval_seconds: 30

tokens:
  daily_limit_per_agent: null        # null = no limit
  warning_threshold: 0.8             # warn at 80% of limit
```

---

## Project Structure

```
WRAI.TH/
├── main.go                          # Go relay (existing)
├── internal/                        # Go backend (existing)
├── skill/                           # /relay skill (existing)
│
├── client/                          # Python client (NEW)
│   ├── PLAN.md                      # this file
│   ├── pyproject.toml
│   ├── config.example.yaml
│   ├── .env.example                 # DISCORD_TOKEN=
│   │
│   ├── src/
│   │   ├── __init__.py
│   │   ├── main.py                  # entry point, boots components based on mode
│   │   ├── config.py                # pydantic models for config.yaml
│   │   │
│   │   ├── relay/
│   │   │   ├── __init__.py
│   │   │   ├── client.py            # async HTTP/MCP wrapper
│   │   │   ├── models.py            # Agent, Message, Profile, etc.
│   │   │   └── sse.py               # SSE stream handler
│   │   │
│   │   ├── controller/
│   │   │   ├── __init__.py
│   │   │   ├── manager.py           # fleet manager: listens SSE, spawns, monitors
│   │   │   ├── session.py           # single agent subprocess lifecycle
│   │   │   └── output_buffer.py     # ring buffer for stdout capture
│   │   │
│   │   ├── discord_bridge/
│   │   │   ├── __init__.py
│   │   │   ├── bot.py               # discord.py setup, event handlers
│   │   │   ├── commands.py          # /agentname command handling
│   │   │   ├── router.py            # pool/channel routing logic
│   │   │   ├── formatter.py         # Discord embed formatting
│   │   │   └── onboarding.py        # new user detection + CTO flow
│   │   │
│   │   ├── monitor/
│   │   │   ├── __init__.py
│   │   │   ├── tracker.py           # token usage tracking per agent
│   │   │   └── db.py                # SQLite storage for usage data
│   │   │
│   │   └── web/
│   │       ├── __init__.py
│   │       ├── server.py            # FastAPI app
│   │       ├── api.py               # REST endpoints
│   │       ├── ws.py                # WebSocket manager
│   │       └── static/
│   │           ├── index.html
│   │           ├── css/
│   │           │   └── mission-control.css
│   │           └── js/
│   │               ├── app.js       # main SPA logic
│   │               ├── fleet.js     # fleet overview
│   │               ├── agent-tab.js # per-agent tab
│   │               └── ws-client.js # WebSocket client
│   │
│   └── data/                        # gitignored
│       ├── tokens.db                # SQLite for token tracking
│       └── downloads/               # files uploaded via Discord
│
├── install.sh                       # updated: --station or --satellite
└── install.ps1                      # updated: Windows equivalent
```

---

## Edge Cases & Error Handling

| Scenario | Handling |
|----------|---------|
| Relay down | Queue messages locally, retry with backoff, show warning in UI |
| Discord down | Agents keep working via relay, messages pile up, deliver on reconnect |
| Claude crashes | Auto-restart with backoff (2s→4s→8s, max 60s, max 5 retries) |
| Unknown Discord user | Trigger onboarding flow via CTO |
| Agent target doesn't exist | Reply in Discord: "Agent not found. Available: ..." |
| Message too long for Discord | Split into chunks or upload as .md file attachment |
| Multiple machines, same agent | Config prevents this — each agent has one machine |
| Satellite unreachable | Station UI shows "offline" for that machine, relay msgs still work |
| SSE disconnects | Auto-reconnect with delay, fallback poll to catch missed events |

---

## Implementation Order

1. **relay/client.py + sse.py** — foundation, everything depends on it
2. **controller/** — spawn one agent, verify lifecycle works
3. **web/ (basic)** — fleet view + one agent tab with stdout
4. **discord_bridge/** — connect humans
5. **monitor/** — token tracking
6. **satellite mode** — stdout API, remote agent support
7. **web/ (full)** — polish UI, multi-machine view
8. **install scripts** — --station and --satellite deployment
9. **onboarding flow** — new user registration via CTO

---

## CTO Soul Addition (onboarding procedure)

Add to CTO profile context_pack:

```markdown
## Human Onboarding

When a new human wants to interact with the agent system:
1. You receive a notification: "New user [name] (discord: [id]) wants access"
2. Ask charles (founder) via user_question: "New user [name] wants access. Which pool? (quant/dev/ops/all)"
3. Wait for charles's response
4. Register the human profile: register_profile(slug, name, role)
5. Set their allowed pools in relay memory: set_memory(key: "human-[slug]-pools", value: pools)
6. Welcome the new user via relay message
```

---

## What This Does NOT Do

- Does NOT replace the relay — it's a complementary layer
- Does NOT replace the relay web UI — Mission Control is for agent management, relay UI is for communication visualization
- Does NOT run LLM for routing — pure Python forwarding
- Does NOT manage infrastructure (Docker, servers)
- Does NOT handle trading operations (that's the bots)

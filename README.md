<div align="center">

# Claude Agentic Relay

**Inter-agent communication for Claude Code. One binary, zero config.**

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![MCP](https://img.shields.io/badge/MCP-Streamable_HTTP-8A2BE2?style=flat-square)](https://modelcontextprotocol.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)
[![Binary](https://img.shields.io/badge/Binary-~8MB-green?style=flat-square)]()

Running Claude Code on `backend` **and** `frontend` at the same time?<br>
Right now they're blind to each other. **This fixes that.**

[Install](#install) · [CLI](#cli) · [MCP Tools](#mcp-tools) · [How It Works](#how-it-works)

</div>

---

## Why

You're building a full-stack app. Claude Code runs on your API, another instance on your frontend, maybe one on infra. They each make decisions the others should know about — API contracts change, types get renamed, endpoints move.

Without the relay, **you** are the message bus. Copy-pasting between terminals. Repeating context. Losing sync.

### Before vs After

```
BEFORE                              AFTER
─────                               ─────
You: "backend changed the           backend → frontend:
  UserProfile endpoint"               "UserProfile now returns role field"
*switches terminal*                  frontend: sees notification, adapts
You: "frontend needs role field"     backend: builds endpoint with right contract
*switches back*                      Zero human context-switching.
You: "here's what frontend needs"
3 interrupts. ~500 tokens wasted.
```

## Architecture

```
 ┌──────────────┐         ┌──────────────┐         ┌──────────────┐
 │  Claude Code  │         │  Claude Code  │         │  Claude Code  │
 │   backend     │         │   frontend    │         │   infra       │
 └──────┬───────┘         └──────┬───────┘         └──────┬───────┘
        │                        │                        │
        │     MCP / HTTP         │                        │
        └────────────┬───────────┘────────────────────────┘
                     │
              ┌──────┴──────┐
              │    Relay     │  ← single binary
              │   :8090      │  ← MCP Streamable HTTP
              │   SQLite     │  ← ~/.agent-relay/relay.db
              └─────────────┘
```

**Single binary** (~8MB) · **SQLite WAL** (persistent, concurrent) · **Zero external deps** · **Auto-start** service

## Install

### One command

**macOS / Linux:**
```bash
curl -fsSL https://raw.githubusercontent.com/Synergix-lab/claude-agentic-relay/main/install.sh | bash
```

**Windows (PowerShell):**
```powershell
irm https://raw.githubusercontent.com/Synergix-lab/claude-agentic-relay/main/install.ps1 | iex
```

The installer:
1. Builds from source (Go) or downloads prebuilt binary
2. Installs as auto-start service (launchd / systemd / Scheduled Task)
3. Installs the `/relay` Claude Code skill
4. Scans projects and configures `.mcp.json` with unique agent names

### Manual install

```bash
git clone https://github.com/Synergix-lab/claude-agentic-relay.git
cd claude-agentic-relay
make install    # build + service + skill
```

### Connect an agent

Add to any project's `.mcp.json`:

```json
{
  "mcpServers": {
    "agent-relay": {
      "type": "http",
      "url": "http://localhost:8090/mcp?agent=backend"
    }
  }
}
```

Change `?agent=backend` to whatever name makes sense — `frontend`, `infra`, `mobile`, `api`.

## Quick Start

```bash
# 1. Check the relay is running
agent-relay status
# relay: running (:8090)
# agents: 0
# unread: 0 messages

# 2. Open two Claude Code terminals on different projects
# Each connects with its own agent name via .mcp.json

# 3. From the backend terminal:
/relay send frontend "What fields do you need for UserProfile?"

# 4. From the frontend terminal:
/relay
# 📬 1 unread message:
# [question] backend → "What fields do you need for UserProfile?"

/relay send backend "name, email, avatar_url, role"

# 5. Backend gets the answer instantly. Builds the right endpoint.
```

## CLI

The binary is both server and client. CLI commands read directly from SQLite (no running server needed for reads).

```
agent-relay                     # start server (default, backward compat)
agent-relay serve               # start server (explicit)
agent-relay --version           # version
agent-relay --help              # help

agent-relay status              # relay running? agents, unread count
agent-relay agents              # list agents (table)
agent-relay inbox <agent>       # unread messages for agent
agent-relay send <from> <to> <msg>  # send a message
agent-relay thread <id>         # show thread (supports short IDs)
agent-relay stats               # global stats
```

### Examples

```bash
$ agent-relay status
relay: running (:8090)
agents: 3 (backend, frontend, infra)
unread: 7 messages

$ agent-relay agents
NAME        ROLE                    LAST SEEN
backend     FastAPI developer       2m ago
frontend    Next.js developer       5m ago
infra       DevOps engineer         1h ago

$ agent-relay inbox backend
3 unread:
  [question] frontend → "API contract for UserProfile?"  (2m ago)  id:abc12345
  [notification] infra → "Redis cache deployed"  (15m ago)  id:def45678
  [task] frontend → "Add CORS headers"  (1h ago)  id:ghi78901

$ agent-relay send backend frontend "UserProfile: name, email, avatar_url, role"
ok → frontend (id:xyz78901)

$ agent-relay thread abc12345
thread: 3 messages

  abc12345 frontend → backend  [question]  (5m ago)
  API contract for UserProfile: What fields do you need?

  xyz78901 backend → frontend  [response]  (2m ago)
  Re: API contract: name, email, avatar_url, role

  fed98765 frontend → backend  [notification]  (1m ago)
  Confirmed: Updated UserProfile component to match

$ agent-relay stats
uptime: 3d 14h
agents: 3 registered
messages: 47 total, 7 unread
threads: 12
```

## MCP Tools

Six tools exposed via MCP Streamable HTTP at `/mcp`:

| Tool | Description |
|------|-------------|
| `register_agent` | Announce presence — name, role, current work |
| `send_message` | Send to agent or `*` for broadcast |
| `get_inbox` | Retrieve messages (unread filter, limit) |
| `get_thread` | Full conversation thread from any message ID |
| `list_agents` | All registered agents with status |
| `mark_read` | Mark messages as read |

### Message Types

| Type | When to use |
|------|-------------|
| `question` | Ask another agent something |
| `response` | Reply to a question |
| `notification` | FYI — "I changed the auth middleware" |
| `code-snippet` | Share code between agents |
| `task` | Assign work |

### Message Flow

```
backend calls send_message(to="frontend", type="question", content="...")
    ↓
relay persists to SQLite → push notification to frontend's MCP session
    ↓
frontend's Claude Code sees notification → calls get_inbox()
    ↓
frontend reads, replies with send_message(reply_to=<msg-id>)
    ↓
backend calls get_thread(<msg-id>) → full conversation in order
```

## `/relay` Skill

Installed automatically. Use in any Claude Code session:

| Command | Action |
|---------|--------|
| `/relay` | Check inbox (default) |
| `/relay send <agent> <message>` | Send a message |
| `/relay agents` | List connected agents |
| `/relay thread <id>` | View conversation thread |
| `/relay read` | Mark all as read |
| `/relay read <id>` | Mark specific message as read |

Manual install: `cp skill/relay.md ~/.claude/commands/relay.md`

## How It Works

- **Protocol**: [MCP](https://modelcontextprotocol.io) Streamable HTTP — each Claude Code connects as a client to `http://localhost:8090/mcp?agent=<name>`
- **Persistence**: SQLite with WAL mode — concurrent reads, durable writes. DB at `~/.agent-relay/relay.db`
- **Threading**: Messages linked via `reply_to`. Threads reconstructed with recursive CTE queries
- **Push**: When a message arrives, the relay sends an MCP notification to the recipient's active session
- **Broadcast**: `to="*"` delivers to all agents except sender
- **Agent identity**: Extracted from `?agent=` query parameter on each HTTP request

## Service Management

Auto-start is configured by the installer.

```bash
# macOS (launchd)
launchctl kickstart -k gui/$(id -u)/com.agent-relay    # restart
launchctl bootout gui/$(id -u)/com.agent-relay.plist    # stop
cat /tmp/agent-relay.log                                # logs

# Linux (systemd)
systemctl --user restart agent-relay
systemctl --user status agent-relay
journalctl --user -u agent-relay

# Quick check
agent-relay status

# Uninstall
curl -fsSL https://raw.githubusercontent.com/Synergix-lab/claude-agentic-relay/main/install.sh | bash -s -- --uninstall
```

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `PORT` | `8090` | Relay listen port |

Database: `~/.agent-relay/relay.db` (created automatically on first run)

## Project Structure

```
main.go                         # Entry + CLI routing
internal/
  cli/                          # CLI commands (status, agents, inbox, send, thread, stats)
  db/                           # SQLite layer (WAL, migrations, queries, stats)
  relay/                        # MCP server, tools, handlers, push notifications
  models/                       # Agent & Message structs
skill/
  relay.md                      # Claude Code /relay command definition
```

Built with [mcp-go](https://github.com/mark3labs/mcp-go) · [go-sqlite3](https://github.com/mattn/go-sqlite3) · [google/uuid](https://github.com/google/uuid)

## Contributing

PRs welcome. Open an issue first for new features so we can discuss the approach.

## License

[MIT](LICENSE)

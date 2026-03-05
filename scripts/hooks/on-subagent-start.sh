#!/bin/bash
# Claude Code hook: on subagent start
# Writes an event JSON to ~/.pixel-office/events/ for the relay ingester.

EVENTS_DIR="${HOME}/.pixel-office/events"
mkdir -p "$EVENTS_DIR"

SESSION_ID="${CLAUDE_SESSION_ID:-unknown}"
PARENT_ID="${1:-unknown}"
TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
FILENAME="${EVENTS_DIR}/agent-spawn-${SESSION_ID}-$(date +%s%N).json"

TMP="${FILENAME}.tmp"
cat > "$TMP" <<EOF
{"type":"agent_spawn","session_id":"${SESSION_ID}","parent_id":"${PARENT_ID}","ts":"${TS}"}
EOF
mv "$TMP" "$FILENAME"

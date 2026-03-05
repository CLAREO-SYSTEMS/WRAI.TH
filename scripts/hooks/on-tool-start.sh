#!/bin/bash
# Claude Code hook: on tool start
# Writes an event JSON to ~/.pixel-office/events/ for the relay ingester.

EVENTS_DIR="${HOME}/.pixel-office/events"
mkdir -p "$EVENTS_DIR"

SESSION_ID="${CLAUDE_SESSION_ID:-unknown}"
TOOL_NAME="${1:-unknown}"
FILE_PATH="${2:-}"
TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
FILENAME="${EVENTS_DIR}/tool-start-${SESSION_ID}-$(date +%s%N).json"

TMP="${FILENAME}.tmp"
cat > "$TMP" <<EOF
{"type":"tool_start","session_id":"${SESSION_ID}","tool":"${TOOL_NAME}","file":"${FILE_PATH}","ts":"${TS}"}
EOF
mv "$TMP" "$FILENAME"

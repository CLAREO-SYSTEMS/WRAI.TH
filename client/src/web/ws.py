"""WebSocket manager for broadcasting agent stdout and status to UI clients."""

from __future__ import annotations

import json

from fastapi import WebSocket
import structlog

log = structlog.get_logger()


class WebSocketManager:
    """Manages WebSocket connections and broadcasts."""

    def __init__(self):
        self._connections: list[WebSocket] = []

    async def connect(self, ws: WebSocket):
        await ws.accept()
        self._connections.append(ws)
        log.info("ws.connected", clients=len(self._connections))

    def disconnect(self, ws: WebSocket):
        if ws in self._connections:
            self._connections.remove(ws)
        log.info("ws.disconnected", clients=len(self._connections))

    async def broadcast_stdout(self, agent: str, line: str):
        """Broadcast a stdout line from an agent to all connected clients."""
        msg = json.dumps({"type": "stdout", "agent": agent, "line": line})
        await self._broadcast(msg)

    async def broadcast_status(self, agent: str, state: str, **extra):
        """Broadcast agent status change."""
        data = {"type": "status", "agent": agent, "state": state, **extra}
        await self._broadcast(json.dumps(data))

    async def _broadcast(self, message: str):
        dead: list[WebSocket] = []
        for ws in self._connections:
            try:
                await ws.send_text(message)
            except Exception:
                dead.append(ws)
        for ws in dead:
            self.disconnect(ws)

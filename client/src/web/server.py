"""FastAPI web server for Mission Control UI and satellite stdout API."""

from __future__ import annotations

from pathlib import Path
from typing import Any

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles

from ..config import Config
from ..controller.manager import FleetManager
from ..monitor.tracker import TokenTracker
from ..relay.client import RelayClient
from .ws import WebSocketManager

STATIC_DIR = Path(__file__).parent / "static"


def create_app(config: Config, fleet: FleetManager, relay: RelayClient, tracker: TokenTracker | None = None) -> FastAPI:
    """Create the full Mission Control app (station mode)."""
    app = FastAPI(title="WRAI.TH Mission Control")
    ws_manager = WebSocketManager()

    # Wire up stdout listeners for all sessions
    def _make_stdout_listener(agent_name: str, mgr: WebSocketManager):
        async def _on_line(line: str):
            await mgr.broadcast_stdout(agent_name, line)
        return _on_line

    for name, session in fleet.sessions.items():
        session.output.add_listener(_make_stdout_listener(name, ws_manager))

    # ── REST API ──────────────────────────────────────────────────

    @app.get("/api/fleet")
    async def get_fleet():
        """Fleet overview: all agent states on all machines."""
        local = fleet.get_all_states()

        # TODO: fetch remote satellite states
        return {"machines": {config.machine.name: local}}

    @app.get("/api/agent/{name}")
    async def get_agent(name: str):
        """Single agent detail."""
        session = fleet.get_session(name)
        if not session:
            return {"error": "Agent not found on this machine"}
        return {
            "name": name,
            "state": session.state.value,
            "pid": session.pid,
            "uptime": session.uptime,
            "crash_count": session.crash_count,
            "machine": config.machine.name,
            "output": session.output.get_lines(100),
        }

    @app.post("/api/agent/{name}/start")
    async def start_agent(name: str):
        session = fleet.get_session(name)
        if not session:
            return {"error": "Agent not found"}
        await session.spawn()
        return {"ok": True, "state": session.state.value}

    @app.post("/api/agent/{name}/stop")
    async def stop_agent(name: str):
        session = fleet.get_session(name)
        if not session:
            return {"error": "Agent not found"}
        await session.stop()
        return {"ok": True, "state": session.state.value}

    @app.post("/api/agent/{name}/wake")
    async def wake_agent(name: str):
        session = fleet.get_session(name)
        if not session:
            return {"error": "Agent not found"}
        await session.wake()
        return {"ok": True, "state": session.state.value}

    @app.post("/api/send")
    async def send_message(body: dict[str, Any]):
        """Send a message through the relay."""
        result = await relay.send_message(
            as_=body.get("as", "controller"),
            to=body["to"],
            subject=body.get("subject", "Manual message"),
            content=body["content"],
            type=body.get("type", "notification"),
        )
        return result

    # ── Token Usage ───────────────────────────────────────────────

    @app.get("/api/tokens")
    async def get_tokens():
        """Token usage for all agents."""
        if not tracker:
            return {"agents": []}
        return {"agents": await tracker.get_all_usage()}

    @app.get("/api/tokens/{name}")
    async def get_agent_tokens(name: str):
        """Token usage for a specific agent today."""
        if not tracker:
            return {"agent": name, "usage": {}}
        return {"agent": name, "usage": await tracker.get_daily(name)}

    # ── WebSocket ─────────────────────────────────────────────────

    @app.websocket("/ws")
    async def websocket_endpoint(ws: WebSocket):
        await ws_manager.connect(ws)
        try:
            while True:
                data = await ws.receive_text()
                # Client can send commands via WS if needed
        except WebSocketDisconnect:
            ws_manager.disconnect(ws)

    # ── Static files ──────────────────────────────────────────────

    if STATIC_DIR.exists():
        app.mount("/", StaticFiles(directory=str(STATIC_DIR), html=True), name="static")
    else:

        @app.get("/")
        async def index():
            return HTMLResponse(
                "<h1>WRAI.TH Mission Control</h1>"
                "<p>Static files not found. Run from the client/ directory.</p>"
            )

    return app


def create_stdout_app(config: Config, fleet: FleetManager) -> FastAPI:
    """Create the satellite stdout API (exposes agent output for remote UI)."""
    app = FastAPI(title="WRAI.TH Satellite")
    ws_manager = WebSocketManager()

    def _make_stdout_listener_sat(agent_name: str, mgr: WebSocketManager):
        async def _on_line(line: str):
            await mgr.broadcast_stdout(agent_name, line)
        return _on_line

    for name, session in fleet.sessions.items():
        session.output.add_listener(_make_stdout_listener_sat(name, ws_manager))

    @app.get("/api/fleet")
    async def get_fleet():
        return {"machine": config.machine.name, "agents": fleet.get_all_states()}

    @app.get("/api/agent/{name}")
    async def get_agent(name: str):
        session = fleet.get_session(name)
        if not session:
            return {"error": "Agent not found"}
        return {
            "name": name,
            "state": session.state.value,
            "pid": session.pid,
            "uptime": session.uptime,
            "output": session.output.get_lines(100),
        }

    @app.websocket("/ws")
    async def websocket_endpoint(ws: WebSocket):
        await ws_manager.connect(ws)
        try:
            while True:
                await ws.receive_text()
        except WebSocketDisconnect:
            ws_manager.disconnect(ws)

    return app

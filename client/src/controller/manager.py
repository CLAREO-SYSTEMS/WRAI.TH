"""Fleet manager — orchestrates all agent sessions on this machine.

Listens to relay SSE for events, spawns/wakes agents as needed,
handles crash recovery with backoff.
"""

from __future__ import annotations

import asyncio

import structlog

from ..config import Config
from ..relay.client import RelayClient
from ..relay.models import SSEEvent
from ..relay.sse import SSEClient
from .session import AgentSession, SessionState

log = structlog.get_logger()

MAX_CRASH_RETRIES = 5
BACKOFF_BASE = 2.0
BACKOFF_MAX = 60.0


class FleetManager:
    """Manages all agent sessions on this machine."""

    def __init__(self, config: Config, relay: RelayClient):
        self.config = config
        self.relay = relay
        self.sessions: dict[str, AgentSession] = {}
        self.sse: SSEClient | None = None
        self._health_task: asyncio.Task | None = None
        self._running = False

        # Create sessions for local agents only
        for name, agent_cfg in config.local_agents().items():
            self.sessions[name] = AgentSession(name, agent_cfg)
            log.info("fleet.registered", agent=name, machine=config.machine.name)

    async def start(self):
        """Start the fleet manager: SSE listener + health checks."""
        self._running = True

        # Connect SSE
        self.sse = SSEClient(
            self.config.relay.url,
            project=self.config.relay.project,
            reconnect_delay=self.config.sse.reconnect_delay_seconds,
        )
        self.sse.on("message", self._on_message)
        self.sse.on("activity", self._on_activity)
        self.sse.on("agent_status", self._on_agent_status)
        await self.sse.start()

        # Start health check loop
        self._health_task = asyncio.create_task(self._health_loop())

        # Do initial inbox check for all local agents (catch anything missed)
        await self._initial_inbox_check()

        log.info("fleet.started", agents=list(self.sessions.keys()))

    async def stop(self):
        """Gracefully stop all sessions and SSE."""
        self._running = False

        if self.sse:
            await self.sse.stop()

        if self._health_task:
            self._health_task.cancel()

        # Stop all sessions
        for session in self.sessions.values():
            if session.is_alive:
                try:
                    await self.relay.sleep_agent(as_=session.name)
                except Exception:
                    pass
                await session.stop()

        log.info("fleet.stopped")

    def get_session(self, name: str) -> AgentSession | None:
        return self.sessions.get(name)

    def get_all_states(self) -> dict[str, dict]:
        """Return state summary for all sessions (for the UI)."""
        return {
            name: {
                "state": session.state.value,
                "pid": session.pid,
                "uptime": session.uptime,
                "crash_count": session.crash_count,
                "machine": self.config.machine.name,
            }
            for name, session in self.sessions.items()
        }

    # ── SSE Event Handlers ────────────────────────────────────────────

    async def _on_message(self, event: SSEEvent):
        """New message event from relay. Wake target agent if it's ours."""
        to = event.data.get("to", "")
        from_ = event.data.get("from", "")

        # Skip if message is FROM one of our agents (they sent it, no action needed)
        if from_ in self.sessions:
            return

        # Wake target agent if it's on this machine
        if to in self.sessions:
            session = self.sessions[to]
            if session.state in (SessionState.IDLE, SessionState.SLEEPING, SessionState.DEAD):
                log.info("fleet.waking_agent", agent=to, from_=from_)
                await self._spawn_or_wake(session)
        elif to == "*":
            # Broadcast — wake all idle agents
            for session in self.sessions.values():
                if session.state in (SessionState.IDLE, SessionState.SLEEPING):
                    await self._spawn_or_wake(session)

    async def _on_activity(self, event: SSEEvent):
        """Agent activity event. Update session state."""
        agent_name = event.data.get("agent_name", "")
        activity = event.data.get("activity", "")

        session = self.sessions.get(agent_name)
        if not session:
            return

        if activity in ("terminal", "read", "write", "thinking"):
            session.mark_busy()
        elif activity in ("idle", "waiting"):
            session.mark_idle()

    async def _on_agent_status(self, event: SSEEvent):
        """Agent status change event."""
        agent_name = event.data.get("agent_name", "")
        status = event.data.get("status", "")

        session = self.sessions.get(agent_name)
        if not session:
            return

        if status == "sleeping":
            session.state = SessionState.SLEEPING

    # ── Spawn / Wake ──────────────────────────────────────────────────

    async def _spawn_or_wake(self, session: AgentSession):
        """Spawn a new process or wake an existing one."""
        if session.crash_count >= MAX_CRASH_RETRIES:
            log.error("fleet.max_retries", agent=session.name, crashes=session.crash_count)
            return

        try:
            if session.is_alive:
                await session.wake()
            else:
                await session.spawn()
        except Exception as e:
            log.error("fleet.spawn_failed", agent=session.name, error=str(e))
            if session.crash_count < MAX_CRASH_RETRIES:
                delay = min(BACKOFF_BASE ** session.crash_count, BACKOFF_MAX)
                log.info("fleet.retry_scheduled", agent=session.name, delay=delay)
                await asyncio.sleep(delay)
                await self._spawn_or_wake(session)

    # ── Initial Inbox Check ───────────────────────────────────────────

    async def _initial_inbox_check(self):
        """Check inbox for all local agents on startup (catch missed messages)."""
        for name, session in self.sessions.items():
            if not session.config.auto_spawn:
                continue
            try:
                messages = await self.relay.get_inbox(as_=name, unread_only=True, limit=1)
                if messages:
                    log.info("fleet.pending_messages", agent=name, count=len(messages))
                    await self._spawn_or_wake(session)
            except Exception as e:
                log.warning("fleet.inbox_check_failed", agent=name, error=str(e))

    # ── Health Check Loop ─────────────────────────────────────────────

    async def _health_loop(self):
        """Periodic health checks for all sessions."""
        interval = self.config.sse.health_check_interval_seconds
        while self._running:
            try:
                await asyncio.sleep(interval)
                for name, session in self.sessions.items():
                    # Detect crashed processes
                    if session.process and session.process.returncode is not None:
                        if session.state not in (SessionState.DEAD, SessionState.CRASHED):
                            session.state = SessionState.CRASHED
                            session.crash_count += 1
                            log.warning("fleet.process_died", agent=name)

                    # Auto-restart crashed agents with pending messages
                    if session.state == SessionState.CRASHED and session.config.auto_spawn:
                        if session.crash_count < MAX_CRASH_RETRIES:
                            try:
                                msgs = await self.relay.get_inbox(as_=name, unread_only=True, limit=1)
                                if msgs:
                                    await self._spawn_or_wake(session)
                            except Exception:
                                pass

            except asyncio.CancelledError:
                return
            except Exception as e:
                log.error("fleet.health_error", error=str(e))

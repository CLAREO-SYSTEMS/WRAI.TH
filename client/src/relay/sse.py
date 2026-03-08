"""SSE (Server-Sent Events) client for the WRAI.TH relay activity stream.

Maintains a persistent connection to /api/activity/stream and dispatches
events to registered handlers. Auto-reconnects with backoff on disconnect.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Callable, Coroutine
from typing import Any

import httpx
import structlog

from .models import SSEEvent

log = structlog.get_logger()

EventHandler = Callable[[SSEEvent], Coroutine[Any, Any, None]]


class SSEClient:
    """Persistent SSE connection to the relay activity stream."""

    def __init__(
        self,
        base_url: str,
        project: str = "default",
        reconnect_delay: float = 3.0,
    ):
        self.base_url = base_url.rstrip("/")
        self.project = project
        self.reconnect_delay = reconnect_delay
        self._handlers: dict[str, list[EventHandler]] = {}
        self._running = False
        self._task: asyncio.Task | None = None

    def on(self, event_type: str, handler: EventHandler):
        """Register a handler for an event type.

        Event types: message, activity, agent_status, task_update, memory_conflict
        Use "*" to catch all events.
        """
        self._handlers.setdefault(event_type, []).append(handler)

    async def start(self):
        """Start the SSE connection loop."""
        self._running = True
        self._task = asyncio.create_task(self._loop())
        log.info("sse.started", url=self.base_url, project=self.project)

    async def stop(self):
        """Stop the SSE connection."""
        self._running = False
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
        log.info("sse.stopped")

    async def _loop(self):
        """Main connection loop with auto-reconnect."""
        delay = self.reconnect_delay

        while self._running:
            try:
                await self._connect()
                delay = self.reconnect_delay  # reset on successful connection
            except httpx.ConnectError:
                log.warning("sse.connect_failed", delay=delay)
            except httpx.ReadError:
                log.warning("sse.read_error", delay=delay)
            except Exception as e:
                log.error("sse.error", error=str(e), delay=delay)

            if self._running:
                await asyncio.sleep(delay)
                delay = min(delay * 1.5, 60.0)  # backoff up to 60s

    async def _connect(self):
        """Connect to SSE stream and process events."""
        url = f"{self.base_url}/api/activity/stream?project={self.project}"
        log.info("sse.connecting", url=url)

        async with httpx.AsyncClient(timeout=None) as client:
            async with client.stream("GET", url) as resp:
                resp.raise_for_status()
                log.info("sse.connected")

                event_type = ""
                data_lines: list[str] = []

                async for line in resp.aiter_lines():
                    if not self._running:
                        return

                    line = line.strip()

                    if line.startswith("event:"):
                        event_type = line[6:].strip()
                    elif line.startswith("data:"):
                        data_lines.append(line[5:].strip())
                    elif line == "" and data_lines:
                        # End of event — dispatch
                        raw = "\n".join(data_lines)
                        data_lines = []
                        try:
                            data = json.loads(raw)
                            event = SSEEvent(
                                type=event_type or data.get("type", ""),
                                data=data,
                            )
                            await self._dispatch(event)
                        except json.JSONDecodeError:
                            log.warning("sse.invalid_json", raw=raw[:200])
                        event_type = ""

    async def _dispatch(self, event: SSEEvent):
        """Dispatch event to registered handlers."""
        handlers = self._handlers.get(event.type, []) + self._handlers.get("*", [])
        for handler in handlers:
            try:
                await handler(event)
            except Exception as e:
                log.error("sse.handler_error", type=event.type, error=str(e))

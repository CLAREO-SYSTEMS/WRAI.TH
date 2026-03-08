"""Single agent subprocess lifecycle management."""

from __future__ import annotations

import asyncio
import enum
import time

import structlog

from ..config import AgentConfig
from .output_buffer import OutputBuffer

log = structlog.get_logger()


class SessionState(enum.Enum):
    IDLE = "idle"
    SPAWNING = "spawning"
    WORKING = "working"
    SLEEPING = "sleeping"
    CRASHED = "crashed"
    DEAD = "dead"


class AgentSession:
    """Manages a single Claude Code subprocess for one agent."""

    def __init__(self, name: str, config: AgentConfig):
        self.name = name
        self.config = config
        self.state = SessionState.IDLE
        self.process: asyncio.subprocess.Process | None = None
        self.output = OutputBuffer()
        self.pid: int | None = None
        self.started_at: float | None = None
        self.crash_count: int = 0
        self._reader_task: asyncio.Task | None = None
        self._idle_timer: asyncio.Task | None = None

    @property
    def uptime(self) -> float:
        if self.started_at is None:
            return 0.0
        return time.time() - self.started_at

    @property
    def is_alive(self) -> bool:
        return self.process is not None and self.process.returncode is None

    async def spawn(self):
        """Spawn the Claude Code subprocess."""
        if self.is_alive:
            log.warning("session.already_alive", agent=self.name)
            return

        self.state = SessionState.SPAWNING
        log.info("session.spawning", agent=self.name, cwd=self.config.work_dir)

        boot_prompt = (
            f'You are agent "{self.config.profile_slug}". '
            f"Register with the relay using your profile, "
            f"then run /relay talk to process your inbox. "
            f"When done, stay ready for the next /relay talk command."
        )

        try:
            self.process = await asyncio.create_subprocess_exec(
                "claude",
                cwd=self.config.work_dir,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.STDOUT,
            )
            self.pid = self.process.pid
            self.started_at = time.time()
            self.crash_count = 0

            # Start reading stdout
            self._reader_task = asyncio.create_task(self._read_output())

            # Send boot prompt
            await self._send(boot_prompt)
            self.state = SessionState.WORKING

            log.info("session.spawned", agent=self.name, pid=self.pid)

        except FileNotFoundError:
            log.error("session.claude_not_found", agent=self.name)
            self.state = SessionState.DEAD
            raise
        except Exception as e:
            log.error("session.spawn_failed", agent=self.name, error=str(e))
            self.state = SessionState.CRASHED
            raise

    async def wake(self):
        """Send /relay talk to an idle agent."""
        if not self.is_alive:
            await self.spawn()
            return

        if self.state in (SessionState.IDLE, SessionState.SLEEPING):
            log.info("session.waking", agent=self.name)
            self._cancel_idle_timer()
            await self._send("/relay talk")
            self.state = SessionState.WORKING

    async def stop(self):
        """Gracefully stop the subprocess."""
        if not self.is_alive:
            return

        log.info("session.stopping", agent=self.name, pid=self.pid)
        self._cancel_idle_timer()

        try:
            self.process.terminate()
            try:
                await asyncio.wait_for(self.process.wait(), timeout=5.0)
            except asyncio.TimeoutError:
                self.process.kill()
                await self.process.wait()
        except ProcessLookupError:
            pass

        self.state = SessionState.DEAD
        self.process = None
        self.pid = None

        if self._reader_task:
            self._reader_task.cancel()
            try:
                await self._reader_task
            except asyncio.CancelledError:
                pass

        log.info("session.stopped", agent=self.name)

    def mark_busy(self):
        """Called when SSE reports agent is calling tools."""
        if self.state != SessionState.WORKING:
            self.state = SessionState.WORKING
            self._cancel_idle_timer()

    def mark_idle(self):
        """Called when SSE reports agent has gone quiet."""
        if self.state == SessionState.WORKING:
            self.state = SessionState.IDLE
            self._start_idle_timer()

    async def _send(self, text: str):
        """Send text to stdin."""
        if self.process and self.process.stdin:
            self.process.stdin.write(f"{text}\n".encode())
            await self.process.stdin.drain()

    async def _read_output(self):
        """Read stdout line by line into the ring buffer."""
        try:
            while self.is_alive and self.process.stdout:
                line = await self.process.stdout.readline()
                if not line:
                    break
                decoded = line.decode("utf-8", errors="replace").rstrip("\n\r")
                await self.output.write(decoded)
        except asyncio.CancelledError:
            return
        except Exception as e:
            log.error("session.read_error", agent=self.name, error=str(e))

        # Process exited
        if self.process and self.process.returncode != 0:
            self.state = SessionState.CRASHED
            self.crash_count += 1
            log.warning(
                "session.crashed",
                agent=self.name,
                return_code=self.process.returncode,
                crash_count=self.crash_count,
            )
        else:
            self.state = SessionState.DEAD
            log.info("session.exited", agent=self.name)

    def _start_idle_timer(self):
        """Start countdown to sleep after idle timeout."""
        self._cancel_idle_timer()
        self._idle_timer = asyncio.create_task(self._idle_countdown())

    def _cancel_idle_timer(self):
        if self._idle_timer and not self._idle_timer.done():
            self._idle_timer.cancel()
            self._idle_timer = None

    async def _idle_countdown(self):
        """Wait for idle timeout, then transition to sleeping."""
        try:
            await asyncio.sleep(self.config.idle_timeout_seconds)
            if self.state == SessionState.IDLE:
                log.info("session.idle_timeout", agent=self.name)
                self.state = SessionState.SLEEPING
        except asyncio.CancelledError:
            pass

"""Token usage tracker — parses Claude CLI output for usage info."""

from __future__ import annotations

import asyncio
import re
from datetime import datetime, timezone

import structlog

from ..config import Config, TokenConfig
from ..controller.session import AgentSession
from .db import TokenDB

log = structlog.get_logger()

# Claude CLI outputs token usage in various formats, e.g.:
# "Total tokens: 1234 (input: 800, output: 434)"
# "Usage: 800 input + 434 output = 1234 total"
TOKEN_PATTERNS = [
    re.compile(r"(?:total[_ ]?tokens|usage).*?(\d[\d,]+)\s*(?:total|tokens)", re.IGNORECASE),
    re.compile(r"input[:\s]+(\d[\d,]+).*?output[:\s]+(\d[\d,]+)", re.IGNORECASE),
]


class TokenTracker:
    """Monitors agent sessions for token usage."""

    def __init__(self, config: Config):
        self.config = config
        self.db = TokenDB()
        self._running = False
        # Per-agent accumulators for current session
        self._session_tokens: dict[str, dict] = {}

    async def start(self):
        self._running = True
        await self.db.init()
        log.info("tracker.started")

    async def stop(self):
        self._running = False
        # Flush any pending data
        for agent, data in self._session_tokens.items():
            if data["total"] > 0:
                await self._flush(agent, data)
        await self.db.close()

    def attach_to_session(self, session: AgentSession):
        """Attach a stdout listener to an agent session to capture token usage."""
        self._session_tokens[session.name] = {
            "input": 0,
            "output": 0,
            "total": 0,
            "start": datetime.now(timezone.utc).isoformat(),
        }

        async def _on_line(line: str):
            await self._parse_line(session.name, line)

        session.output.add_listener(_on_line)

    async def _parse_line(self, agent: str, line: str):
        """Try to extract token counts from a stdout line."""
        for pattern in TOKEN_PATTERNS:
            match = pattern.search(line)
            if match:
                groups = match.groups()
                data = self._session_tokens.get(agent)
                if not data:
                    return

                if len(groups) == 2:
                    # input + output
                    data["input"] = int(groups[0].replace(",", ""))
                    data["output"] = int(groups[1].replace(",", ""))
                    data["total"] = data["input"] + data["output"]
                elif len(groups) == 1:
                    data["total"] = int(groups[0].replace(",", ""))

                log.debug("tracker.tokens", agent=agent, total=data["total"])
                await self._check_limits(agent, data)
                break

    async def _check_limits(self, agent: str, data: dict):
        """Check if agent is approaching daily token limit."""
        limit = self.config.tokens.daily_limit_per_agent
        if limit is None:
            return

        today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        daily = await self.db.get_daily_usage(agent, today)
        current_total = daily["total_tokens"] + data["total"]

        threshold = self.config.tokens.warning_threshold
        if current_total >= limit:
            log.warning("tracker.limit_reached", agent=agent, total=current_total, limit=limit)
        elif current_total >= limit * threshold:
            log.warning("tracker.limit_approaching", agent=agent, total=current_total, limit=limit)

    async def _flush(self, agent: str, data: dict):
        """Write accumulated token data to DB."""
        if data["total"] <= 0:
            return

        # Rough cost estimate (Claude Opus pricing)
        cost = (data["input"] * 15.0 + data["output"] * 75.0) / 1_000_000

        await self.db.record_usage(
            agent=agent,
            session_start=data["start"],
            input_tokens=data["input"],
            output_tokens=data["output"],
            estimated_cost=cost,
        )

        today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        await self.db.update_daily_summary(agent, today)

        log.info(
            "tracker.flushed",
            agent=agent,
            tokens=data["total"],
            cost_usd=f"${cost:.4f}",
        )

        # Reset accumulator
        data["input"] = 0
        data["output"] = 0
        data["total"] = 0
        data["start"] = datetime.now(timezone.utc).isoformat()

    async def get_all_usage(self) -> list[dict]:
        """Get usage summary for all agents."""
        return await self.db.get_agent_totals()

    async def get_daily(self, agent: str) -> dict:
        today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        return await self.db.get_daily_usage(agent, today)

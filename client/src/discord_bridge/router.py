"""Outbound message routing: relay → Discord channels.

Polls relay inbox for human profiles and forwards messages
to the appropriate Discord pool channel. Sequential send queue.
"""

from __future__ import annotations

import asyncio

import discord
import structlog

from ..config import Config
from ..relay.client import RelayClient
from .formatter import format_agent_message, format_question

log = structlog.get_logger()


class MessageRouter:
    """Routes relay messages to Discord channels."""

    def __init__(self, config: Config, relay: RelayClient):
        self.config = config
        self.relay = relay
        self._send_queue: asyncio.Queue[tuple[int, discord.Embed]] = asyncio.Queue()
        self._channel_cache: dict[str, discord.TextChannel] = {}

    async def start_outbound_loop(self, bot: discord.Client):
        """Start both the inbox poller and the send queue processor."""
        asyncio.create_task(self._send_loop(bot))
        asyncio.create_task(self._poll_loop(bot))

    async def _poll_loop(self, bot: discord.Client):
        """Poll relay inbox for each human, forward new messages to Discord."""
        interval = self.config.sse.fallback_poll_seconds

        while True:
            try:
                for slug, human in self.config.humans.items():
                    messages = await self.relay.get_inbox(
                        as_=slug,
                        unread_only=True,
                        limit=10,
                        full_content=True,
                    )

                    if not messages:
                        continue

                    msg_ids = []
                    for msg in messages:
                        channel_id = self._resolve_channel(msg.from_)
                        if channel_id:
                            embed = self._format_message(msg)
                            await self._send_queue.put((channel_id, embed))
                        msg_ids.append(msg.id)

                    # Mark all as read
                    if msg_ids:
                        await self.relay.mark_read(as_=slug, message_ids=msg_ids)
                        log.info("router.forwarded", human=slug, count=len(msg_ids))

            except Exception as e:
                log.error("router.poll_error", error=str(e))

            await asyncio.sleep(interval)

    async def _send_loop(self, bot: discord.Client):
        """Sequential send queue — one message at a time to respect rate limits."""
        while True:
            channel_id, embed = await self._send_queue.get()
            try:
                channel = self._get_channel(bot, channel_id)
                if channel:
                    await channel.send(embed=embed)
                else:
                    log.warning("router.channel_not_found", channel_id=channel_id)
            except discord.HTTPException as e:
                log.error("router.send_failed", channel_id=channel_id, error=str(e))
            except Exception as e:
                log.error("router.send_error", error=str(e))

            # Small delay between sends
            await asyncio.sleep(0.5)

    def _resolve_channel(self, from_agent: str) -> int | None:
        """Resolve which Discord channel an agent's message should go to."""
        # Find the agent's pool
        agent_cfg = self.config.agents.get(from_agent)
        if not agent_cfg:
            # Default to ops-pool for unknown agents
            pool_name = "ops"
        else:
            pool_name = agent_cfg.pool

        # Get pool config
        pool = self.config.pools.get(pool_name)
        if not pool:
            return None

        # Get channel ID from discord config
        channel_id_str = self.config.discord.channels.get(pool.channel)
        if not channel_id_str:
            return None

        try:
            return int(channel_id_str)
        except ValueError:
            log.error("router.invalid_channel_id", channel=pool.channel, value=channel_id_str)
            return None

    def _get_channel(self, bot: discord.Client, channel_id: int) -> discord.TextChannel | None:
        """Get a Discord channel by ID, with caching."""
        return bot.get_channel(channel_id)

    def _format_message(self, msg) -> discord.Embed:
        """Format a relay message as a Discord embed."""
        if msg.type == "user_question":
            return format_question(msg)
        return format_agent_message(msg)

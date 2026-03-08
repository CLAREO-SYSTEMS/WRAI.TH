"""Discord bot setup and main event loop.

Pure forwarder — no LLM. Routes messages between Discord and the relay.
"""

from __future__ import annotations

import asyncio

import discord
import structlog

from ..config import Config
from ..relay.client import RelayClient
from .commands import CommandHandler
from .router import MessageRouter

log = structlog.get_logger()


async def start_discord_bot(config: Config, relay: RelayClient):
    """Start the Discord bot. Runs until cancelled."""
    intents = discord.Intents.default()
    intents.message_content = True
    intents.members = True

    bot = discord.Client(intents=intents)
    command_handler = CommandHandler(config, relay)
    router = MessageRouter(config, relay)

    @bot.event
    async def on_ready():
        log.info("discord.ready", user=str(bot.user), guilds=len(bot.guilds))
        # Start outbound relay → Discord forwarding loop
        asyncio.create_task(router.start_outbound_loop(bot))

    @bot.event
    async def on_message(message: discord.Message):
        # Ignore bot's own messages
        if message.author == bot.user:
            return

        # Ignore DMs for now
        if not message.guild:
            return

        # Check if it's a command (starts with /)
        if message.content.startswith("/"):
            await command_handler.handle(message)
            return

        # Check if bot is mentioned
        if bot.user in message.mentions:
            # Strip mention and treat rest as command
            content = message.content.replace(f"<@{bot.user.id}>", "").strip()
            if content:
                # Try to parse as "agentname message"
                parts = content.split(None, 1)
                if len(parts) >= 2:
                    await command_handler.handle_raw(message, parts[0], parts[1])
                elif len(parts) == 1:
                    await command_handler.handle_raw(message, parts[0], "")

    try:
        await bot.start(config.discord.token)
    except discord.LoginFailure:
        log.error("discord.login_failed", hint="Check DISCORD_TOKEN in .env")
    except asyncio.CancelledError:
        await bot.close()

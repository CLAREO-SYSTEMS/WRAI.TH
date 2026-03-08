"""Discord command handling.

Parses /agentname message and routes to relay.
Handles file uploads and unknown user detection.
"""

from __future__ import annotations

import os
from pathlib import Path

import discord
import structlog

from ..config import Config
from ..relay.client import RelayClient
from .onboarding import OnboardingHandler
from .formatter import format_confirmation

log = structlog.get_logger()


class CommandHandler:
    """Parses Discord commands and forwards to relay."""

    def __init__(self, config: Config, relay: RelayClient):
        self.config = config
        self.relay = relay
        self.onboarding = OnboardingHandler(config, relay)
        self._agent_names: set[str] | None = None

    async def _get_agent_names(self) -> set[str]:
        """Cache known agent names from relay."""
        if self._agent_names is None:
            try:
                agents = await self.relay.list_agents()
                self._agent_names = {a.name for a in agents}
            except Exception:
                self._agent_names = set(self.config.agents.keys())
        return self._agent_names

    def _resolve_sender(self, author: discord.Member | discord.User) -> str | None:
        """Map Discord user to relay profile slug."""
        discord_id = str(author.id)
        for slug, human in self.config.humans.items():
            if human.discord_id == discord_id:
                return slug
        return None

    async def handle(self, message: discord.Message):
        """Handle a /command message."""
        content = message.content.lstrip("/")
        if not content:
            return

        parts = content.split(None, 1)
        target = parts[0].lower()
        body = parts[1] if len(parts) > 1 else ""

        await self.handle_raw(message, target, body)

    async def handle_raw(self, message: discord.Message, target: str, body: str):
        """Handle a parsed command: target agent + message body."""
        # Resolve sender
        sender = self._resolve_sender(message.author)
        if sender is None:
            # Unknown user → trigger onboarding
            await self.onboarding.handle_unknown_user(message)
            return

        # Check sender has access to target's pool
        if not self._check_pool_access(sender, target):
            await message.reply(
                f"You don't have access to talk to **{target}**.",
                mention_author=False,
            )
            return

        # Validate target agent exists
        agent_names = await self._get_agent_names()
        if target not in agent_names:
            available = ", ".join(sorted(agent_names))
            await message.reply(
                f"Agent **{target}** not found. Available: {available}",
                mention_author=False,
            )
            return

        # Handle file attachments
        file_paths = await self._download_attachments(message)
        if file_paths:
            file_info = "\n".join(f"[Attached: {p}]" for p in file_paths)
            body = f"{body}\n\n{file_info}" if body else file_info

        if not body:
            await message.reply(
                f"Usage: `/{target} your message here`",
                mention_author=False,
            )
            return

        # Determine message type
        msg_type = "question" if body.rstrip().endswith("?") else "notification"

        # Derive subject from first ~8 words
        words = body.split()[:8]
        subject = " ".join(words)
        if len(body.split()) > 8:
            subject += "..."

        # Send to relay
        try:
            await self.relay.send_message(
                as_=sender,
                to=target,
                subject=subject,
                content=body,
                type=msg_type,
            )
            # Confirm with a reaction
            await message.add_reaction("\u2705")
            log.info("discord.forwarded", sender=sender, target=target, chars=len(body))
        except Exception as e:
            log.error("discord.forward_failed", error=str(e))
            await message.reply(
                f"Failed to send message: {e}",
                mention_author=False,
            )

    def _check_pool_access(self, sender_slug: str, target_agent: str) -> bool:
        """Check if sender has access to the target agent's pool."""
        human = self.config.humans.get(sender_slug)
        if not human:
            return False

        # Find which pool the target belongs to
        target_pool = None
        agent_cfg = self.config.agents.get(target_agent)
        if agent_cfg:
            target_pool = agent_cfg.pool

        if target_pool is None:
            # Agent not in config, allow (might be dynamically registered)
            return True

        return target_pool in human.pools

    async def _download_attachments(self, message: discord.Message) -> list[str]:
        """Download .txt and .md attachments to local disk."""
        paths = []
        download_dir = Path(self.config.machine.download_dir)
        download_dir.mkdir(parents=True, exist_ok=True)

        for attachment in message.attachments:
            if not attachment.filename.endswith((".txt", ".md")):
                continue

            dest = download_dir / attachment.filename
            # Avoid overwriting
            if dest.exists():
                stem = dest.stem
                suffix = dest.suffix
                counter = 1
                while dest.exists():
                    dest = download_dir / f"{stem}_{counter}{suffix}"
                    counter += 1

            try:
                await attachment.save(dest)
                paths.append(str(dest))
                log.info("discord.file_downloaded", file=str(dest), size=attachment.size)
            except Exception as e:
                log.error("discord.download_failed", file=attachment.filename, error=str(e))

        return paths

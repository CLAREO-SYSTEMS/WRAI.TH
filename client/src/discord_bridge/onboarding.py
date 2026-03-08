"""New user detection and onboarding flow.

When an unknown Discord user tries to interact with an agent,
notifies the CTO agent who asks the founder for pool access.
"""

from __future__ import annotations

import discord
import structlog

from ..config import Config
from ..relay.client import RelayClient

log = structlog.get_logger()


class OnboardingHandler:
    """Handles unknown Discord users trying to use the agent system."""

    def __init__(self, config: Config, relay: RelayClient):
        self.config = config
        self.relay = relay
        self._pending: set[str] = set()  # discord IDs currently being onboarded

    async def handle_unknown_user(self, message: discord.Message):
        """Handle a message from an unregistered Discord user."""
        discord_id = str(message.author.id)
        username = str(message.author)
        display_name = message.author.display_name

        # Don't spam if already onboarding this user
        if discord_id in self._pending:
            await message.reply(
                "Your access request is being processed. Please wait.",
                mention_author=False,
            )
            return

        self._pending.add(discord_id)
        log.info("onboarding.new_user", username=username, discord_id=discord_id)

        # Reply to user
        await message.reply(
            f"Hi **{display_name}**! You're not registered in the agent system yet. "
            f"I've notified the CTO to set up your access. Please wait.",
            mention_author=False,
        )

        # Notify CTO agent
        try:
            await self.relay.send_message(
                as_="system",
                to="cto",
                subject=f"New user: {display_name}",
                content=(
                    f"New Discord user wants to interact with the agent system.\n\n"
                    f"- **Display name**: {display_name}\n"
                    f"- **Username**: {username}\n"
                    f"- **Discord ID**: {discord_id}\n\n"
                    f"Please ask charles (founder) which pool they should have access to "
                    f"(quant/dev/ops/all), then register their profile."
                ),
                type="notification",
            )
            log.info("onboarding.cto_notified", user=username)
        except Exception as e:
            log.error("onboarding.notify_failed", error=str(e))
            self._pending.discard(discord_id)

    def mark_onboarded(self, discord_id: str):
        """Called after CTO completes onboarding."""
        self._pending.discard(discord_id)

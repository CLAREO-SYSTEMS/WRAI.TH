"""Discord embed formatting for relay messages."""

from __future__ import annotations

import discord


# Colors for different message types
COLORS = {
    "notification": 0x5865F2,   # blurple
    "question": 0xFEE75C,       # yellow
    "user_question": 0xED4245,  # red — needs attention
    "response": 0x57F287,       # green
    "code-snippet": 0x99AAB5,   # grey
    "task": 0xEB459E,           # pink
}


def format_agent_message(msg) -> discord.Embed:
    """Format a standard relay message as a Discord embed."""
    color = COLORS.get(msg.type, COLORS["notification"])

    embed = discord.Embed(
        title=msg.subject or "Message",
        description=_truncate(msg.content, 4000),
        color=color,
        timestamp=msg.created_at,
    )
    embed.set_author(name=f"{msg.from_}")
    embed.set_footer(text=f"type: {msg.type} | id: {msg.id[:8]}")

    return embed


def format_question(msg) -> discord.Embed:
    """Format a user_question with distinct styling."""
    embed = discord.Embed(
        title=f"\u2753 {msg.subject or 'Question'}",
        description=_truncate(msg.content, 4000),
        color=COLORS["user_question"],
        timestamp=msg.created_at,
    )
    embed.set_author(name=f"{msg.from_} needs your input")
    embed.set_footer(text=f"Reply with: /{msg.from_} your answer")

    return embed


def format_confirmation(target: str, content: str) -> discord.Embed:
    """Format a send confirmation."""
    return discord.Embed(
        description=f"Message sent to **{target}**",
        color=0x57F287,
    )


def _truncate(text: str, max_len: int) -> str:
    """Truncate text for Discord embed limits."""
    if len(text) <= max_len:
        return text
    return text[: max_len - 3] + "..."

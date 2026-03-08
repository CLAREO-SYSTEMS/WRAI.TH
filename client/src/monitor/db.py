"""SQLite storage for token usage tracking."""

from __future__ import annotations

from pathlib import Path

import aiosqlite
import structlog

log = structlog.get_logger()

DB_PATH = Path("data/tokens.db")

SCHEMA = """
CREATE TABLE IF NOT EXISTS token_usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent TEXT NOT NULL,
    session_start TEXT NOT NULL,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    estimated_cost_usd REAL DEFAULT 0.0,
    recorded_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_token_agent ON token_usage(agent);
CREATE INDEX IF NOT EXISTS idx_token_recorded ON token_usage(recorded_at);

CREATE TABLE IF NOT EXISTS daily_summary (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent TEXT NOT NULL,
    date TEXT NOT NULL,
    total_input INTEGER DEFAULT 0,
    total_output INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    total_cost_usd REAL DEFAULT 0.0,
    session_count INTEGER DEFAULT 0,
    UNIQUE(agent, date)
);
"""


class TokenDB:
    """Async SQLite wrapper for token tracking."""

    def __init__(self, db_path: Path | None = None):
        self.db_path = db_path or DB_PATH
        self._db: aiosqlite.Connection | None = None

    async def init(self):
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._db = await aiosqlite.connect(str(self.db_path))
        await self._db.executescript(SCHEMA)
        await self._db.commit()
        log.info("token_db.initialized", path=str(self.db_path))

    async def close(self):
        if self._db:
            await self._db.close()

    async def record_usage(
        self,
        agent: str,
        session_start: str,
        input_tokens: int,
        output_tokens: int,
        estimated_cost: float = 0.0,
    ):
        total = input_tokens + output_tokens
        await self._db.execute(
            "INSERT INTO token_usage (agent, session_start, input_tokens, output_tokens, total_tokens, estimated_cost_usd) "
            "VALUES (?, ?, ?, ?, ?, ?)",
            (agent, session_start, input_tokens, output_tokens, total, estimated_cost),
        )
        await self._db.commit()

    async def update_daily_summary(self, agent: str, date: str):
        """Aggregate token_usage into daily_summary for an agent."""
        row = await self._db.execute_fetchall(
            "SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), "
            "COALESCE(SUM(total_tokens),0), COALESCE(SUM(estimated_cost_usd),0), COUNT(*) "
            "FROM token_usage WHERE agent=? AND date(recorded_at)=?",
            (agent, date),
        )
        if row:
            inp, out, total, cost, count = row[0]
            await self._db.execute(
                "INSERT INTO daily_summary (agent, date, total_input, total_output, total_tokens, total_cost_usd, session_count) "
                "VALUES (?, ?, ?, ?, ?, ?, ?) "
                "ON CONFLICT(agent, date) DO UPDATE SET "
                "total_input=excluded.total_input, total_output=excluded.total_output, "
                "total_tokens=excluded.total_tokens, total_cost_usd=excluded.total_cost_usd, "
                "session_count=excluded.session_count",
                (agent, date, inp, out, total, cost, count),
            )
            await self._db.commit()

    async def get_daily_usage(self, agent: str, date: str) -> dict:
        rows = await self._db.execute_fetchall(
            "SELECT total_input, total_output, total_tokens, total_cost_usd, session_count "
            "FROM daily_summary WHERE agent=? AND date=?",
            (agent, date),
        )
        if rows:
            return {
                "input_tokens": rows[0][0],
                "output_tokens": rows[0][1],
                "total_tokens": rows[0][2],
                "cost_usd": rows[0][3],
                "session_count": rows[0][4],
            }
        return {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "cost_usd": 0.0, "session_count": 0}

    async def get_agent_totals(self) -> list[dict]:
        """Get total usage per agent, all time."""
        rows = await self._db.execute_fetchall(
            "SELECT agent, SUM(total_tokens), SUM(estimated_cost_usd), COUNT(*) "
            "FROM token_usage GROUP BY agent ORDER BY SUM(total_tokens) DESC"
        )
        return [
            {"agent": r[0], "total_tokens": r[1], "cost_usd": r[2], "sessions": r[3]}
            for r in rows
        ]

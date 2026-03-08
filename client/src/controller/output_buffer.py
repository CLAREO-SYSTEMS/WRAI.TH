"""Ring buffer for capturing agent stdout/stderr."""

from __future__ import annotations

import asyncio
from collections import deque
from collections.abc import Callable, Coroutine
from typing import Any


class OutputBuffer:
    """Fixed-size ring buffer that notifies listeners on new lines."""

    def __init__(self, max_lines: int = 1000):
        self._lines: deque[str] = deque(maxlen=max_lines)
        self._listeners: list[Callable[[str], Coroutine[Any, Any, None]]] = []

    def add_listener(self, callback: Callable[[str], Coroutine[Any, Any, None]]):
        self._listeners.append(callback)

    def remove_listener(self, callback: Callable[[str], Coroutine[Any, Any, None]]):
        self._listeners.remove(callback)

    async def write(self, line: str):
        self._lines.append(line)
        for listener in self._listeners:
            try:
                await listener(line)
            except Exception:
                pass

    def get_lines(self, n: int | None = None) -> list[str]:
        if n is None:
            return list(self._lines)
        return list(self._lines)[-n:]

    def clear(self):
        self._lines.clear()

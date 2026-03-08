"""Async HTTP client for the WRAI.TH relay API.

Uses REST for reads, MCP tools/call for writes (supports `as` parameter).
The relay's MCP endpoint is stateless — no session negotiation needed.
"""

from __future__ import annotations

import json
import uuid
from typing import Any

import httpx
import structlog

from .models import Agent, Board, Conversation, Goal, Memory, Message, Profile, SessionContext, Task

log = structlog.get_logger()


class RelayClient:
    """Wraps the WRAI.TH relay HTTP API."""

    def __init__(self, base_url: str, project: str = "default"):
        self.base_url = base_url.rstrip("/")
        self.project = project
        self._http = httpx.AsyncClient(base_url=self.base_url, timeout=30.0)

    async def close(self):
        await self._http.aclose()

    # ── MCP tool call (JSON-RPC) ──────────────────────────────────────

    async def _mcp_call(self, tool: str, arguments: dict[str, Any]) -> dict[str, Any]:
        """Call an MCP tool via POST /mcp?project=X."""
        payload = {
            "jsonrpc": "2.0",
            "id": str(uuid.uuid4()),
            "method": "tools/call",
            "params": {
                "name": tool,
                "arguments": arguments,
            },
        }
        resp = await self._http.post(f"/mcp?project={self.project}", json=payload)
        resp.raise_for_status()
        result = resp.json()

        if "error" in result:
            raise RelayError(result["error"])

        # MCP returns result.content[0].text as JSON string
        content = result.get("result", {}).get("content", [])
        if content and content[0].get("type") == "text":
            return json.loads(content[0]["text"])
        return result.get("result", {})

    # ── REST helpers ──────────────────────────────────────────────────

    async def _get(self, path: str, params: dict[str, Any] | None = None) -> Any:
        """GET request to REST API."""
        params = params or {}
        params.setdefault("project", self.project)
        resp = await self._http.get(f"/api{path}", params=params)
        resp.raise_for_status()
        return resp.json()

    async def _post(self, path: str, body: dict[str, Any]) -> Any:
        body.setdefault("project", self.project)
        resp = await self._http.post(f"/api{path}", json=body)
        resp.raise_for_status()
        return resp.json()

    # ── Agent Management ──────────────────────────────────────────────

    async def register_agent(
        self,
        name: str,
        *,
        role: str = "",
        description: str = "",
        reports_to: str | None = None,
        is_executive: bool = False,
        profile_slug: str | None = None,
        session_id: str | None = None,
    ) -> SessionContext:
        args: dict[str, Any] = {"name": name}
        if role:
            args["role"] = role
        if description:
            args["description"] = description
        if reports_to:
            args["reports_to"] = reports_to
        if is_executive:
            args["is_executive"] = True
        if profile_slug:
            args["profile_slug"] = profile_slug
        if session_id:
            args["session_id"] = session_id

        data = await self._mcp_call("register_agent", args)
        return SessionContext(
            agent=Agent(**data.get("agent", {})),
            profile=Profile(**data["profile"]) if data.get("profile") else None,
            pending_tasks=data.get("pending_tasks", {}),
            unread_messages=[Message(**m) for m in data.get("unread_messages", [])],
            active_conversations=[Conversation(**c) for c in data.get("active_conversations", [])],
            relevant_memories=[Memory(**m) for m in data.get("relevant_memories", [])],
            is_respawn=data.get("is_respawn", False),
        )

    async def list_agents(self) -> list[Agent]:
        data = await self._get("/agents")
        return [Agent(**a) for a in data]

    async def sleep_agent(self, *, as_: str) -> dict:
        return await self._mcp_call("sleep_agent", {"as": as_})

    async def deactivate_agent(self, name: str) -> dict:
        return await self._mcp_call("deactivate_agent", {"name": name})

    async def delete_agent(self, name: str) -> dict:
        return await self._mcp_call("delete_agent", {"name": name})

    # ── Messaging ─────────────────────────────────────────────────────

    async def send_message(
        self,
        *,
        as_: str,
        to: str,
        subject: str,
        content: str,
        type: str = "notification",
        reply_to: str | None = None,
        conversation_id: str | None = None,
        metadata: str | None = None,
    ) -> dict:
        args: dict[str, Any] = {
            "as": as_,
            "to": to,
            "subject": subject,
            "content": content,
            "type": type,
        }
        if reply_to:
            args["reply_to"] = reply_to
        if conversation_id:
            args["conversation_id"] = conversation_id
        if metadata:
            args["metadata"] = metadata
        return await self._mcp_call("send_message", args)

    async def get_inbox(
        self,
        *,
        as_: str,
        unread_only: bool = True,
        limit: int = 10,
        full_content: bool = False,
    ) -> list[Message]:
        data = await self._mcp_call("get_inbox", {
            "as": as_,
            "unread_only": unread_only,
            "limit": limit,
            "full_content": full_content,
        })
        return [Message(**m) for m in data.get("messages", [])]

    async def mark_read(self, *, as_: str, message_ids: list[str] | None = None, conversation_id: str | None = None) -> dict:
        args: dict[str, Any] = {"as": as_}
        if message_ids:
            args["message_ids"] = message_ids
        if conversation_id:
            args["conversation_id"] = conversation_id
        return await self._mcp_call("mark_read", args)

    async def get_thread(self, message_id: str) -> list[Message]:
        data = await self._mcp_call("get_thread", {"message_id": message_id})
        return [Message(**m) for m in data.get("messages", [])]

    # ── Conversations ─────────────────────────────────────────────────

    async def create_conversation(self, *, as_: str, title: str, members: list[str]) -> Conversation:
        data = await self._mcp_call("create_conversation", {
            "as": as_,
            "title": title,
            "members": members,
        })
        return Conversation(**data)

    async def list_conversations(self, *, as_: str) -> list[Conversation]:
        data = await self._mcp_call("list_conversations", {"as": as_})
        return [Conversation(**c) for c in data.get("conversations", [])]

    async def get_conversation_messages(
        self,
        conversation_id: str,
        *,
        as_: str,
        limit: int = 50,
        full_content: bool = True,
    ) -> list[Message]:
        data = await self._mcp_call("get_conversation_messages", {
            "conversation_id": conversation_id,
            "as": as_,
            "limit": limit,
            "full_content": full_content,
        })
        return [Message(**m) for m in data.get("messages", [])]

    async def invite_to_conversation(self, conversation_id: str, agent_name: str, *, as_: str) -> dict:
        return await self._mcp_call("invite_to_conversation", {
            "conversation_id": conversation_id,
            "agent_name": agent_name,
            "as": as_,
        })

    # ── Memory ────────────────────────────────────────────────────────

    async def set_memory(
        self,
        *,
        as_: str,
        key: str,
        value: str,
        scope: str = "project",
        tags: list[str] | None = None,
        confidence: str = "stated",
        layer: str = "behavior",
    ) -> Memory:
        args: dict[str, Any] = {
            "as": as_,
            "key": key,
            "value": value,
            "scope": scope,
            "confidence": confidence,
            "layer": layer,
        }
        if tags:
            args["tags"] = tags
        data = await self._mcp_call("set_memory", args)
        return Memory(**data)

    async def get_memory(self, key: str, *, as_: str, scope: str | None = None) -> Memory | None:
        args: dict[str, Any] = {"key": key, "as": as_}
        if scope:
            args["scope"] = scope
        data = await self._mcp_call("get_memory", args)
        if not data or "error" in data:
            return None
        return Memory(**data)

    async def search_memory(self, query: str, *, as_: str, limit: int = 20) -> list[Memory]:
        data = await self._mcp_call("search_memory", {"query": query, "as": as_, "limit": limit})
        return [Memory(**m) for m in data.get("memories", [])]

    async def list_memories(self, *, as_: str, scope: str | None = None) -> list[Memory]:
        args: dict[str, Any] = {"as": as_}
        if scope:
            args["scope"] = scope
        data = await self._mcp_call("list_memories", args)
        return [Memory(**m) for m in data.get("memories", [])]

    async def delete_memory(self, key: str, *, as_: str, scope: str = "project") -> dict:
        return await self._mcp_call("delete_memory", {"key": key, "as": as_, "scope": scope})

    async def resolve_conflict(self, key: str, chosen_value: str, *, as_: str, scope: str = "project") -> Memory:
        data = await self._mcp_call("resolve_conflict", {
            "key": key,
            "chosen_value": chosen_value,
            "as": as_,
            "scope": scope,
        })
        return Memory(**data)

    # ── Profiles ──────────────────────────────────────────────────────

    async def register_profile(
        self,
        slug: str,
        name: str,
        *,
        role: str = "",
        context_pack: str = "",
        soul_keys: list[str] | None = None,
        skills: list[dict] | None = None,
    ) -> Profile:
        args: dict[str, Any] = {"slug": slug, "name": name}
        if role:
            args["role"] = role
        if context_pack:
            args["context_pack"] = context_pack
        if soul_keys:
            args["soul_keys"] = json.dumps(soul_keys)
        if skills:
            args["skills"] = json.dumps(skills)
        data = await self._mcp_call("register_profile", args)
        return Profile(**data)

    async def get_profile(self, slug: str) -> Profile | None:
        data = await self._mcp_call("get_profile", {"slug": slug})
        if not data or "error" in data:
            return None
        return Profile(**data)

    async def list_profiles(self) -> list[Profile]:
        data = await self._mcp_call("list_profiles", {})
        return [Profile(**p) for p in data.get("profiles", [])]

    # ── Tasks ─────────────────────────────────────────────────────────

    async def dispatch_task(
        self,
        *,
        as_: str,
        profile: str,
        title: str,
        description: str = "",
        priority: str = "P2",
        parent_task_id: str | None = None,
        board_id: str | None = None,
        goal_id: str | None = None,
    ) -> Task:
        args: dict[str, Any] = {
            "as": as_,
            "profile": profile,
            "title": title,
            "priority": priority,
        }
        if description:
            args["description"] = description
        if parent_task_id:
            args["parent_task_id"] = parent_task_id
        if board_id:
            args["board_id"] = board_id
        if goal_id:
            args["goal_id"] = goal_id
        data = await self._mcp_call("dispatch_task", args)
        return Task(**data)

    async def list_tasks(
        self,
        *,
        status: str | None = None,
        assigned_to: str | None = None,
        board_id: str | None = None,
        limit: int = 50,
    ) -> list[Task]:
        params: dict[str, Any] = {}
        if status:
            params["status"] = status
        if assigned_to:
            params["assigned_to"] = assigned_to
        if board_id:
            params["board_id"] = board_id
        params["limit"] = limit
        data = await self._get("/tasks", params)
        return [Task(**t) for t in data]

    async def get_task(self, task_id: str, *, include_subtasks: bool = False) -> Task:
        data = await self._mcp_call("get_task", {
            "task_id": task_id,
            "include_subtasks": include_subtasks,
        })
        return Task(**data)

    async def transition_task(self, task_id: str, *, as_: str, status: str, result: str | None = None, reason: str | None = None) -> Task:
        tool_map = {
            "accepted": "claim_task",
            "in-progress": "start_task",
            "done": "complete_task",
            "blocked": "block_task",
            "cancelled": "cancel_task",
        }
        tool = tool_map.get(status)
        if not tool:
            raise ValueError(f"Unknown task status: {status}")

        args: dict[str, Any] = {"task_id": task_id, "as": as_}
        if result and status == "done":
            args["result"] = result
        if reason and status in ("blocked", "cancelled"):
            args["reason"] = reason
        data = await self._mcp_call(tool, args)
        return Task(**data)

    # ── Boards ────────────────────────────────────────────────────────

    async def list_boards(self) -> list[Board]:
        data = await self._get("/boards")
        return [Board(**b) for b in data]

    # ── Goals ─────────────────────────────────────────────────────────

    async def list_goals(self, *, type: str | None = None, status: str | None = None) -> list[Goal]:
        params: dict[str, Any] = {}
        if type:
            params["type"] = type
        if status:
            params["status"] = status
        data = await self._get("/goals", params)
        return [Goal(**g) for g in data]

    async def get_goal_cascade(self) -> list[Goal]:
        data = await self._get("/goals/cascade")
        return [Goal(**g) for g in data]

    # ── Activity (REST) ───────────────────────────────────────────────

    async def get_activity(self) -> list[dict]:
        return await self._get("/activity")

    async def get_messages_since(self, since: str) -> list[Message]:
        data = await self._get("/messages/latest", {"since": since})
        return [Message(**m) for m in data]


class RelayError(Exception):
    """Error from the relay API."""
    pass

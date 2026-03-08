"""Data models matching the WRAI.TH relay API."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class Agent(BaseModel):
    id: str = ""
    name: str
    role: str = ""
    description: str = ""
    registered_at: datetime | None = None
    last_seen: datetime | None = None
    project: str = "default"
    reports_to: str | None = None
    profile_slug: str | None = None
    status: str = "active"  # active, sleeping, deactivated, offline
    deactivated_at: datetime | None = None
    is_executive: bool = False
    session_id: str | None = None
    # REST-only fields
    online: bool = False
    activity: str = ""  # terminal, browser, read, write, thinking, waiting, idle
    activity_tool: str = ""
    teams: list[dict[str, Any]] = Field(default_factory=list)


class Message(BaseModel):
    id: str
    from_: str = Field(alias="from", default="")
    to: str = ""
    reply_to: str | None = None
    type: str = "notification"  # question, response, notification, code-snippet, task, user_question
    subject: str = ""
    content: str = ""
    metadata: str = "{}"
    created_at: datetime | None = None
    read_at: datetime | None = None
    conversation_id: str | None = None
    project: str = "default"
    task_id: str | None = None

    model_config = {"populate_by_name": True}


class Memory(BaseModel):
    id: str = ""
    key: str
    value: str = ""
    tags: str = "[]"  # JSON array
    scope: str = "project"  # agent, project, global
    project: str = "default"
    agent_name: str = ""
    confidence: str = "stated"  # stated, inferred, observed
    version: int = 1
    supersedes: str | None = None
    conflict_with: str | None = None
    created_at: datetime | None = None
    updated_at: datetime | None = None
    archived_at: datetime | None = None
    archived_by: str | None = None
    layer: str = "behavior"  # constraints, behavior, context


class Profile(BaseModel):
    id: str = ""
    slug: str
    name: str = ""
    role: str = ""
    context_pack: str = ""
    soul_keys: str = "[]"  # JSON array
    skills: str = "[]"  # JSON array
    vault_paths: str = "[]"  # JSON array
    project: str = "default"
    org_id: str | None = None
    created_at: datetime | None = None
    updated_at: datetime | None = None


class Task(BaseModel):
    id: str = ""
    profile_slug: str = ""
    assigned_to: str | None = None
    dispatched_by: str = ""
    title: str = ""
    description: str = ""
    priority: str = "P2"  # P0, P1, P2, P3
    status: str = "pending"  # pending, accepted, in-progress, done, blocked, cancelled
    result: str | None = None
    blocked_reason: str | None = None
    project: str = "default"
    dispatched_at: datetime | None = None
    accepted_at: datetime | None = None
    started_at: datetime | None = None
    completed_at: datetime | None = None
    parent_task_id: str | None = None
    board_id: str | None = None
    goal_id: str | None = None
    archived_at: datetime | None = None
    subtasks: list[Task] = Field(default_factory=list)


class Conversation(BaseModel):
    id: str = ""
    title: str = ""
    created_by: str = ""
    created_at: datetime | None = None
    archived_at: datetime | None = None
    project: str = "default"
    members: list[dict[str, Any]] = Field(default_factory=list)


class Goal(BaseModel):
    id: str = ""
    project: str = "default"
    type: str = "project_goal"  # mission, project_goal, agent_goal
    title: str = ""
    description: str = ""
    owner_agent: str | None = None
    parent_goal_id: str | None = None
    status: str = "active"  # active, completed, paused
    created_by: str = ""
    created_at: datetime | None = None
    updated_at: datetime | None = None
    completed_at: datetime | None = None
    total_tasks: int = 0
    done_tasks: int = 0
    progress: float = 0.0
    children: list[Goal] = Field(default_factory=list)
    ancestry: list[dict[str, Any]] = Field(default_factory=list)


class Board(BaseModel):
    id: str = ""
    project: str = "default"
    name: str = ""
    slug: str = ""
    description: str = ""
    created_by: str = ""
    created_at: datetime | None = None
    archived_at: datetime | None = None


class SessionContext(BaseModel):
    agent: Agent | None = None
    profile: Profile | None = None
    pending_tasks: dict[str, list[Task]] = Field(default_factory=dict)
    unread_messages: list[Message] = Field(default_factory=list)
    active_conversations: list[Conversation] = Field(default_factory=list)
    relevant_memories: list[Memory] = Field(default_factory=list)
    is_respawn: bool = False


class SSEEvent(BaseModel):
    """Parsed SSE event from /api/activity/stream."""
    type: str = ""  # message, activity, agent_status, task_update, memory_conflict
    data: dict[str, Any] = Field(default_factory=dict)

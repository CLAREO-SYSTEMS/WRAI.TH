"""Configuration models for the WRAI.TH client."""

from __future__ import annotations

import os
from pathlib import Path

import yaml
from pydantic import BaseModel, Field


class RelayConfig(BaseModel):
    url: str = "http://localhost:8090"
    project: str = "default"


class WebConfig(BaseModel):
    port: int = 8091
    host: str = "0.0.0.0"


class StdoutApiConfig(BaseModel):
    port: int = 8092


class DiscordConfig(BaseModel):
    enabled: bool = True
    token: str = ""
    guild_id: str = ""
    channels: dict[str, str] = Field(default_factory=dict)  # pool_name -> channel_id


class PoolConfig(BaseModel):
    channel: str
    lead: str
    members: list[str] = Field(default_factory=list)


class HumanConfig(BaseModel):
    name: str
    role: str = ""
    discord_id: str = ""
    is_executive: bool = False
    pools: list[str] = Field(default_factory=list)


class AgentConfig(BaseModel):
    profile_slug: str
    work_dir: str
    machine: str
    pool: str
    idle_timeout_seconds: int = 300
    auto_spawn: bool = True


class MachineConfig(BaseModel):
    name: str = "localhost"
    download_dir: str = "./data/downloads"


class SatelliteInfo(BaseModel):
    host: str
    port: int = 8092


class SSEConfig(BaseModel):
    reconnect_delay_seconds: float = 3.0
    fallback_poll_seconds: float = 10.0
    health_check_interval_seconds: float = 30.0


class TokenConfig(BaseModel):
    daily_limit_per_agent: int | None = None
    warning_threshold: float = 0.8


class Config(BaseModel):
    mode: str = "station"  # "station" or "satellite"
    relay: RelayConfig = Field(default_factory=RelayConfig)
    machine: MachineConfig = Field(default_factory=MachineConfig)
    web: WebConfig = Field(default_factory=WebConfig)
    stdout_api: StdoutApiConfig = Field(default_factory=StdoutApiConfig)
    discord: DiscordConfig = Field(default_factory=DiscordConfig)
    satellites: dict[str, SatelliteInfo] = Field(default_factory=dict)
    pools: dict[str, PoolConfig] = Field(default_factory=dict)
    humans: dict[str, HumanConfig] = Field(default_factory=dict)
    agents: dict[str, AgentConfig] = Field(default_factory=dict)
    sse: SSEConfig = Field(default_factory=SSEConfig)
    tokens: TokenConfig = Field(default_factory=TokenConfig)

    @property
    def is_station(self) -> bool:
        return self.mode == "station"

    @property
    def is_satellite(self) -> bool:
        return self.mode == "satellite"

    def local_agents(self) -> dict[str, AgentConfig]:
        """Return only agents configured for this machine."""
        return {
            name: agent
            for name, agent in self.agents.items()
            if agent.machine == self.machine.name
        }


def load_config(path: str | Path = "config.yaml") -> Config:
    """Load config from YAML file with env var interpolation."""
    path = Path(path)
    if not path.exists():
        raise FileNotFoundError(f"Config file not found: {path}")

    with open(path) as f:
        raw = f.read()

    # Interpolate ${ENV_VAR} patterns
    for key, value in os.environ.items():
        raw = raw.replace(f"${{{key}}}", value)

    data = yaml.safe_load(raw)
    return Config(**data)

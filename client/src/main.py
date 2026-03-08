"""WRAI.TH Client — entry point.

Boots components based on deployment mode:
  Station:   relay client + SSE + controller + Discord bridge + web UI + token monitor
  Satellite: relay client + SSE + controller + stdout API + token monitor
"""

from __future__ import annotations

import asyncio
import signal
import sys
from pathlib import Path

import structlog

from .config import load_config
from .controller.manager import FleetManager
from .monitor.tracker import TokenTracker
from .relay.client import RelayClient

structlog.configure(
    processors=[
        structlog.processors.TimeStamper(fmt="iso"),
        structlog.processors.add_log_level,
        structlog.dev.ConsoleRenderer(),
    ],
)

log = structlog.get_logger()


async def run():
    # Load config
    config_path = Path("config.yaml")
    if not config_path.exists():
        # Check in client/ subdir (when running from repo root)
        config_path = Path("client/config.yaml")

    config = load_config(config_path)
    log.info(
        "config.loaded",
        mode=config.mode,
        machine=config.machine.name,
        relay=config.relay.url,
        local_agents=list(config.local_agents().keys()),
    )

    # Create relay client
    relay = RelayClient(config.relay.url, config.relay.project)

    # Create fleet manager (runs in both modes)
    fleet = FleetManager(config, relay)

    # Register humans in relay (station only)
    if config.is_station:
        for slug, human in config.humans.items():
            try:
                await relay.register_agent(
                    name=slug,
                    role=human.role,
                    is_executive=human.is_executive,
                    description=f"human:discord:{human.discord_id}",
                )
                log.info("human.registered", slug=slug, role=human.role)
            except Exception as e:
                log.warning("human.register_failed", slug=slug, error=str(e))

    # Start token tracker (runs in both modes)
    tracker = TokenTracker(config)
    await tracker.start()
    for session in fleet.sessions.values():
        tracker.attach_to_session(session)

    # Start fleet manager (SSE + session control)
    await fleet.start()

    # Start web server (station: full UI, satellite: stdout API only)
    web_task = None
    if config.is_station:
        try:
            from .web.server import create_app
            import uvicorn

            app = create_app(config, fleet, relay, tracker)
            web_config = uvicorn.Config(
                app,
                host=config.web.host,
                port=config.web.port,
                log_level="warning",
            )
            server = uvicorn.Server(web_config)
            web_task = asyncio.create_task(server.serve())
            log.info("web.started", port=config.web.port, mode="station")
        except ImportError:
            log.warning("web.not_available", reason="FastAPI/uvicorn not installed")
    else:
        try:
            from .web.server import create_stdout_app
            import uvicorn

            app = create_stdout_app(config, fleet)
            web_config = uvicorn.Config(
                app,
                host="0.0.0.0",
                port=config.stdout_api.port,
                log_level="warning",
            )
            server = uvicorn.Server(web_config)
            web_task = asyncio.create_task(server.serve())
            log.info("web.started", port=config.stdout_api.port, mode="satellite")
        except ImportError:
            log.warning("web.not_available")

    # Start Discord bridge (station only)
    discord_task = None
    if config.is_station and config.discord.enabled and config.discord.token:
        try:
            from .discord_bridge.bot import start_discord_bot

            discord_task = asyncio.create_task(start_discord_bot(config, relay))
            log.info("discord.started")
        except ImportError:
            log.warning("discord.not_available", reason="discord.py not installed")

    log.info("client.ready", mode=config.mode, machine=config.machine.name)

    # Wait for shutdown signal
    stop_event = asyncio.Event()

    def _signal_handler():
        log.info("client.shutting_down")
        stop_event.set()

    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, _signal_handler)
        except NotImplementedError:
            # Windows doesn't support add_signal_handler
            pass

    await stop_event.wait()

    # Graceful shutdown
    await tracker.stop()
    await fleet.stop()
    if discord_task:
        discord_task.cancel()
    if web_task:
        web_task.cancel()
    await relay.close()

    log.info("client.stopped")


def main():
    try:
        asyncio.run(run())
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()

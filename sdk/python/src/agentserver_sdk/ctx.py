"""Ctx — the workspace-scoped SDK handle bundled into every notebook kernel.

`Ctx.from_env()` constructs a lazy handle (no I/O). Every `await ctx.envs()`
hits the gateway's REST endpoint — there is no client-side caching, because
executors come and go and the right answer is whatever the gateway sees
right now.
"""

from __future__ import annotations

import os

from .client import HTTPClient
from .env import Env
from .errors import SdkConfigError
from .types import ToolMetadata


class Ctx:
    def __init__(self, client: HTTPClient) -> None:
        self._client = client

    @classmethod
    def from_env(cls) -> "Ctx":
        url = os.environ.get("AGENTSERVER_GATEWAY_URL")
        token = os.environ.get("AGENTSERVER_WORKSPACE_TOKEN", "")
        if not url:
            raise SdkConfigError("AGENTSERVER_GATEWAY_URL is required")
        if not token:
            raise SdkConfigError("AGENTSERVER_WORKSPACE_TOKEN is required")
        return cls(HTTPClient(url, token))

    async def envs(self) -> list[Env]:
        """List envs currently connected to the workspace. Hits the gateway
        on every call — executors connect/disconnect, and a stale list is
        usually worse than a fresh HTTP round-trip."""
        listing = await self._client.post("/api/connectors/envs/list", {})
        envs: list[Env] = []
        for e in listing.get("envs", []):
            tools = [ToolMetadata.from_dict(t) for t in e.get("tools", [])]
            envs.append(Env(
                name=e["name"],
                type=e.get("type", "executor"),
                tools=tools,
                _client=self._client,
            ))
        return envs

    async def env(self, name: str) -> Env:
        for e in await self.envs():
            if e.name == name:
                return e
        raise KeyError(f"env not found: {name}")

    async def close(self) -> None:
        await self._client.close()

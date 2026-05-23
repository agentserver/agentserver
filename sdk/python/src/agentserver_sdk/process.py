"""Process — async context manager wrapping exec_command / stdin / output / terminate."""

from __future__ import annotations

import base64
import json as _json
from typing import TYPE_CHECKING, Any

from .errors import ToolError

if TYPE_CHECKING:
    from .env import Env


class Process:
    def __init__(self, env: "Env", command: str) -> None:
        self.env = env
        self.command = command
        self.session_id: str | None = None
        self._terminated = False
        self._read_seq = 0

    async def __aenter__(self) -> "Process":
        raw = await self.env.call("exec_command", {"command": self.command})
        sc = raw.get("structuredContent") or {}
        sid = sc.get("session_id")
        if not sid:
            # exec_command's text content carries the session_id when the
            # gateway didn't populate structuredContent (older response shape).
            content = raw.get("content") or []
            if content and content[0].get("type") == "text":
                try:
                    body = _json.loads(content[0]["text"])
                    sid = body.get("session_id")
                except Exception:
                    sid = None
        if not sid:
            raise ToolError(
                tool="exec_command",
                env=self.env.name,
                message="exec_command did not return session_id",
                raw=raw,
            )
        self.session_id = sid
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        await self.terminate()

    async def write_stdin(self, data: bytes) -> None:
        await self.env._client.post(
            f"/api/connectors/processes/{self.session_id}/stdin",
            {"data_b64": base64.b64encode(data).decode("ascii")},
        )

    async def read_output(self, since: int | None = None) -> dict:
        params = {"since": str(since if since is not None else self._read_seq)}
        resp = await self.env._client.get(
            f"/api/connectors/processes/{self.session_id}/output",
            params=params,
        )
        for c in resp.get("chunks", []):
            self._read_seq = max(self._read_seq, c["seq"])
        return resp

    async def terminate(self) -> None:
        if self._terminated:
            return
        try:
            await self.env._client.post(
                f"/api/connectors/processes/{self.session_id}/terminate",
                {},
            )
        finally:
            self._terminated = True

from __future__ import annotations

from typing import Any

import httpx

from .models import Identity, SearchHit


class GatewayError(RuntimeError):
    pass


class KnowledgeGatewayClient:
    """Thin HTTP adapter; authorization is always forwarded to Go for verification."""

    def __init__(self, base_url: str, timeout_seconds: float = 45.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = httpx.Timeout(timeout_seconds, connect=5.0)

    async def _request(self, method: str, path: str, authorization: str, **kwargs: Any) -> Any:
        headers = dict(kwargs.pop("headers", {}))
        headers["Authorization"] = authorization
        headers["Accept"] = "application/json"
        try:
            async with httpx.AsyncClient(base_url=self.base_url, timeout=self.timeout) as client:
                response = await client.request(method, path, headers=headers, **kwargs)
        except httpx.HTTPError as exc:
            raise GatewayError(f"knowledge gateway unavailable: {exc}") from exc
        if response.status_code >= 400:
            detail = response.text[:500]
            raise GatewayError(f"knowledge gateway returned {response.status_code}: {detail}")
        try:
            return response.json()
        except ValueError as exc:
            raise GatewayError("knowledge gateway returned invalid JSON") from exc

    async def identity(self, authorization: str) -> Identity:
        payload = await self._request("GET", "/api/v1/auth/me", authorization)
        return Identity.model_validate(payload)

    async def search(self, app_id: str, environment_id: str, query: str, authorization: str) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/api/v1/apps/{app_id}/query",
            authorization,
            json={"environment_id": environment_id, "query": query, "top_k": 6},
        )

    @staticmethod
    def hits(payload: dict[str, Any]) -> list[SearchHit]:
        result = payload.get("result") or {}
        raw_hits = result.get("hits") if isinstance(result, dict) else []
        return [SearchHit.model_validate(hit) for hit in (raw_hits or [])]


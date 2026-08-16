from __future__ import annotations

import asyncio
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
        response: httpx.Response | None = None
        # Evaluation and interactive traffic share the same production limiter.
        # Respect Retry-After instead of bypassing the limiter or turning a 429
        # into a false RAG/Agent regression.
        for attempt in range(4):
            try:
                async with httpx.AsyncClient(base_url=self.base_url, timeout=self.timeout) as client:
                    response = await client.request(method, path, headers=headers, **kwargs)
            except httpx.HTTPError as exc:
                if attempt == 3:
                    raise GatewayError(f"knowledge gateway unavailable: {exc}") from exc
                await asyncio.sleep(0.25 * (2 ** attempt))
                continue
            if response.status_code != 429 or attempt == 3:
                break
            raw_retry = response.headers.get("Retry-After", "1")
            try:
                retry_after = max(0.1, min(float(raw_retry), 30.0))
            except ValueError:
                retry_after = 1.0
            await asyncio.sleep(retry_after)
        assert response is not None
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

    async def search(self, app_id: str, environment_id: str, query: str, authorization: str, device_context: dict[str, Any] | None = None) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/api/v1/apps/{app_id}/query",
            authorization,
            json={"environment_id": environment_id, "query": query, "top_k": 6, "device_context": device_context or {}},
        )

    @staticmethod
    def hits(payload: dict[str, Any]) -> list[SearchHit]:
        result = payload.get("result") or {}
        raw_hits = result.get("hits") if isinstance(result, dict) else []
        return [SearchHit.model_validate(hit) for hit in (raw_hits or [])]

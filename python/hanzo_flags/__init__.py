"""hanzo-flags — Python client for the Hanzo native flags engine.

Evaluates against cloud's /v1/flags (stateless Rust evaluator, per-org/project
SQLite definitions, PostHog-compatible semantics). Zero dependencies (stdlib).

    from hanzo_flags import Flags

    flags = Flags("https://api.hanzo.ai", token=os.environ["HANZO_API_KEY"])
    res = flags.evaluate("user-42", person_properties={"plan": "pro"})

    if res.is_enabled("new-nav"): ...
    if res.variant("checkout-exp") == "treatment": ...
    cfg = res.payload("checkout-exp")
"""

from __future__ import annotations

import json
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Optional

__all__ = ["Flags", "Result"]
__version__ = "0.1.0"


@dataclass
class Result:
    """PostHog-shaped evaluation response."""

    feature_flags: dict[str, Any] = field(default_factory=dict)
    feature_flag_payloads: dict[str, Any] = field(default_factory=dict)
    errors_while_computing_flags: bool = False

    def is_enabled(self, key: str) -> bool:
        v = self.feature_flags.get(key)
        return v is True or (isinstance(v, str) and v != "")

    def variant(self, key: str) -> Optional[str]:
        v = self.feature_flags.get(key)
        return v if isinstance(v, str) else None

    def payload(self, key: str) -> Any:
        return self.feature_flag_payloads.get(key)


class Flags:
    """Client for POST {host}/v1/flags."""

    def __init__(
        self,
        host: str,
        token: str | None = None,
        project: str | None = None,
        timeout: float = 5.0,
    ) -> None:
        self.host = host.rstrip("/")
        self.token = token
        self.project = project
        self.timeout = timeout

    def evaluate(
        self,
        distinct_id: str,
        person_properties: dict[str, Any] | None = None,
        groups: dict[str, dict[str, Any]] | None = None,
    ) -> Result:
        body: dict[str, Any] = {"distinct_id": distinct_id}
        if person_properties:
            body["person_properties"] = person_properties
        if groups:
            body["groups"] = groups
        req = urllib.request.Request(
            f"{self.host}/v1/flags",
            data=json.dumps(body).encode(),
            headers=self._headers(),
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            out = json.loads(resp.read().decode())
        return Result(
            feature_flags=out.get("featureFlags") or {},
            feature_flag_payloads=out.get("featureFlagPayloads") or {},
            errors_while_computing_flags=bool(out.get("errorsWhileComputingFlags")),
        )

    def _headers(self) -> dict[str, str]:
        h = {"Content-Type": "application/json"}
        if self.token:
            h["Authorization"] = f"Bearer {self.token}"
        if self.project:
            h["X-Project-Id"] = self.project
        return h

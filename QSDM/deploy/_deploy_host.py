"""Resolve the QSDM deployment SSH target from shared operator settings.

Deployment helpers used to fall back to one retired VPS IP address. That made
a migration easy to get wrong: a correctly configured release script could
still send its privileged follow-up action to the old machine. This module is
the one resolver used by the Paramiko helpers.

Resolution order (highest priority first):

1. ``QSDM_VPS_HOST`` with optional ``QSDM_VPS_USER`` and ``QSDM_VPS_PORT``.
2. ``QSDM_RELEASE_SSH_TARGET``.
3. ``vps_ssh_target`` in ``QSDM_ENDPOINTS_FILE`` or
   ``QSDM/config/public-endpoints.json``.
4. ``root@node.qsdm.tech`` as a documented compatibility default.

The target accepts ``user@host``, ``user@host:port``, bracketed IPv6, or an
``ssh://`` URL. Invalid and ambiguous values fail before any network action.
"""
from __future__ import annotations

from dataclasses import dataclass
import ipaddress
import json
import os
from pathlib import Path
import re
from urllib.parse import urlsplit


_DEFAULT_TARGET = "root@node.qsdm.tech"
_DEFAULT_USER = "root"
_DEFAULT_PORT = 22
_DEFAULT_PUBLIC_API_BASE = "https://api.qsdm.tech"
_DEFAULT_ENDPOINTS_PATH = Path(__file__).resolve().parent.parent / "config" / "public-endpoints.json"
_USER_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_-]{0,31}$")


class DeploymentTargetError(ValueError):
    """Raised before a deploy helper connects to an unsafe or unclear target."""


@dataclass(frozen=True)
class DeploymentTarget:
    """A parsed SSH destination for a deployment helper."""

    host: str
    user: str
    port: int

    def display(self) -> str:
        rendered_host = f"[{self.host}]" if ":" in self.host else self.host
        suffix = "" if self.port == _DEFAULT_PORT else f":{self.port}"
        return f"{self.user}@{rendered_host}{suffix}"


def _nonempty_env(name: str) -> str | None:
    value = os.getenv(name, "").strip()
    return value or None


def _endpoint_config_path() -> tuple[Path, bool]:
    configured = _nonempty_env("QSDM_ENDPOINTS_FILE")
    if configured:
        return Path(configured).expanduser(), True
    return _DEFAULT_ENDPOINTS_PATH, False


def _configured_endpoint_value(name: str) -> str | None:
    path, explicit = _endpoint_config_path()
    if not path.is_file():
        if explicit:
            raise DeploymentTargetError(
                f"QSDM endpoint configuration was not found at {path}. "
                "Fix QSDM_ENDPOINTS_FILE or unset it."
            )
        return None
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise DeploymentTargetError(f"Invalid QSDM endpoint configuration at {path}: {exc}") from exc
    if not isinstance(payload, dict):
        raise DeploymentTargetError(f"Invalid QSDM endpoint configuration at {path}: expected a JSON object.")
    value = payload.get(name)
    if value is None:
        return None
    if not isinstance(value, str):
        raise DeploymentTargetError(
            f"Invalid QSDM endpoint configuration at {path}: {name} must be a string."
        )
    return value.strip() or None


def _configured_target() -> str | None:
    return _configured_endpoint_value("vps_ssh_target")

def _validate_user(value: str) -> str:
    if not _USER_RE.fullmatch(value):
        raise DeploymentTargetError(
            "Invalid SSH user. Use a Linux account name containing letters, digits, underscores, or hyphens."
        )
    return value


def _parse_port(value: str | int) -> int:
    try:
        port = int(str(value), 10)
    except ValueError as exc:
        raise DeploymentTargetError(f"Invalid SSH port: {value!r}.") from exc
    if not 1 <= port <= 65535:
        raise DeploymentTargetError(f"SSH port must be between 1 and 65535, got {port}.")
    return port


def _validate_host(value: str) -> str:
    host = value.strip().rstrip(".")
    if not host or any(ch.isspace() for ch in host) or any(ch in host for ch in "@/?#[]"):
        raise DeploymentTargetError(f"Invalid SSH host: {value!r}.")
    return host


def _parse_target(value: str) -> DeploymentTarget:
    raw = value.strip()
    if not raw or any(ch.isspace() for ch in raw):
        raise DeploymentTargetError("SSH target must not be empty or contain whitespace.")

    if raw.startswith("ssh://"):
        parsed = urlsplit(raw)
        if parsed.scheme != "ssh" or not parsed.hostname or parsed.password or parsed.query or parsed.fragment:
            raise DeploymentTargetError(f"Invalid SSH URL target: {value!r}.")
        if parsed.path not in ("", "/"):
            raise DeploymentTargetError("SSH URL targets must not include a path.")
        user = _validate_user(parsed.username or _DEFAULT_USER)
        try:
            parsed_port = parsed.port
        except ValueError as exc:
            raise DeploymentTargetError(f"Invalid SSH URL target: {value!r}.") from exc
        return DeploymentTarget(_validate_host(parsed.hostname), user, parsed_port or _DEFAULT_PORT)

    user = _DEFAULT_USER
    host_port = raw
    if "@" in raw:
        user, host_port = raw.rsplit("@", 1)
        user = _validate_user(user)
    if not host_port:
        raise DeploymentTargetError(f"Invalid SSH target: {value!r}.")

    port = _DEFAULT_PORT
    if host_port.startswith("["):
        closing = host_port.find("]")
        if closing <= 1:
            raise DeploymentTargetError(f"Invalid bracketed SSH target: {value!r}.")
        host = host_port[1:closing]
        suffix = host_port[closing + 1 :]
        if suffix:
            if not suffix.startswith(":"):
                raise DeploymentTargetError(f"Invalid bracketed SSH target: {value!r}.")
            port = _parse_port(suffix[1:])
    elif host_port.count(":") == 1:
        candidate_host, candidate_port = host_port.rsplit(":", 1)
        if not candidate_host or not candidate_port.isdigit():
            raise DeploymentTargetError(
                "Use host:port for a port override or bracket an IPv6 host, for example [2001:db8::1]:2202."
            )
        host = candidate_host
        port = _parse_port(candidate_port)
    elif host_port.count(":") > 1:
        # Multiple colons can only represent an unbracketed IPv6 address. It
        # has no explicit port, which remains the SSH default.
        try:
            ipaddress.IPv6Address(host_port)
        except ValueError as exc:
            raise DeploymentTargetError(
                "Unbracketed SSH targets with multiple colons must be a valid IPv6 address."
            ) from exc
        host = host_port
    else:
        host = host_port
    return DeploymentTarget(_validate_host(host), user, port)


def target(
    host_override: str | None = None,
    user_override: str | None = None,
    port_override: str | int | None = None,
) -> DeploymentTarget:
    """Return the one deployment target after applying explicit overrides."""
    configured_host = host_override or _nonempty_env("QSDM_VPS_HOST")
    raw_target = configured_host or _nonempty_env("QSDM_RELEASE_SSH_TARGET") or _configured_target() or _DEFAULT_TARGET
    resolved = _parse_target(raw_target)

    configured_user = user_override or _nonempty_env("QSDM_VPS_USER")
    configured_port = port_override if port_override is not None else _nonempty_env("QSDM_VPS_PORT")
    return DeploymentTarget(
        host=resolved.host,
        user=_validate_user(configured_user) if configured_user else resolved.user,
        port=_parse_port(configured_port) if configured_port is not None else resolved.port,
    )


def host() -> str:
    """Return the configured deployment host."""
    return target().host


def user() -> str:
    """Return the configured deployment SSH user."""
    return target().user


def port() -> int:
    """Return the configured deployment SSH port."""
    return target().port


def normalize_public_api_base(value: str) -> str:
    """Validate a public QSDM API origin and return it without a trailing slash.

    This value is used in deploy-side verification requests and may become a
    report target for a remote sidecar. Restrict it to an HTTPS origin so a
    malformed endpoint cannot turn a migration helper into an arbitrary shell
    command fragment or quietly post trust proofs over clear text.
    """
    raw = value.strip()
    if not raw:
        raise DeploymentTargetError("Public QSDM API base must not be empty.")
    try:
        parsed = urlsplit(raw)
        parsed_port = parsed.port
    except ValueError as exc:
        raise DeploymentTargetError(f"Invalid public QSDM API base: {value!r}.") from exc
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        raise DeploymentTargetError(
            "Public QSDM API base must be an HTTPS origin without credentials, a path, query, or fragment."
        )
    host = _validate_host(parsed.hostname)
    rendered_host = f"[{host}]" if ":" in host else host
    port_suffix = f":{parsed_port}" if parsed_port and parsed_port != 443 else ""
    return f"https://{rendered_host}{port_suffix}"


def public_api_base(override: str | None = None) -> str:
    """Return the public API origin used by deploy-time verification checks."""
    raw = override or _nonempty_env("QSDM_PUBLIC_API_BASE_URL") or _configured_endpoint_value("public_api_base") or _DEFAULT_PUBLIC_API_BASE
    return normalize_public_api_base(raw)


def ngc_proof_ingest_url(public_base_override: str | None = None) -> str:
    """Return the public NGC proof ingest endpoint for a secondary sidecar."""
    return f"{public_api_base(public_base_override)}/api/v1/monitoring/ngc-proof"


def require_root_user(operation: str) -> None:
    """Fail clearly for helpers that write privileged VPS paths.

    The legacy Paramiko installers intentionally use root-owned paths and
    systemd. They do not yet implement a reviewed sudo escalation path.
    """
    resolved = target()
    if resolved.user != "root":
        raise DeploymentTargetError(
            f"{operation} writes privileged VPS paths and currently requires root SSH access, "
            f"but the resolved target is {resolved.display()}. Set the target user to root or use a "
            "separate sudo-aware deployment workflow."
        )

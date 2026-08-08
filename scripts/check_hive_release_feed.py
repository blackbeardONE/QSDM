#!/usr/bin/env python3
"""Verify that the public QSDM Hive updater feed matches an approved release."""

from __future__ import annotations

import argparse
import base64
import json
import re
import ssl
import sys
import urllib.error
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass


DEFAULT_BASE_URL = "https://qsdm.tech/downloads"
PINNED_RELEASE_KEY_ID = (
    "10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1"
)
SEMVER_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")


class FeedCheckError(RuntimeError):
    """Raised when the public release feed is missing or inconsistent."""


@dataclass(frozen=True)
class UpdaterManifest:
    version: str
    path: str


def parse_updater_manifest(content: str, source: str) -> UpdaterManifest:
    values: dict[str, str] = {}
    for line in content.splitlines():
        match = re.match(r"^(version|path):\s*['\"]?([^'\"\s]+)", line.strip())
        if match and match.group(1) not in values:
            values[match.group(1)] = match.group(2)

    if not values.get("version") or not values.get("path"):
        raise FeedCheckError(f"{source} is missing version or path")
    return UpdaterManifest(version=values["version"], path=values["path"])


def validate_release_envelope(
    content: str,
    *,
    source: str,
    expected_version: str,
    expected_commit: str,
    expected_platform: str,
    expected_artifact: str,
) -> None:
    try:
        envelope = json.loads(content)
    except json.JSONDecodeError as error:
        raise FeedCheckError(f"{source} is not valid JSON: {error}") from error

    if envelope.get("schema") != "qsdm.signed-release.v1":
        raise FeedCheckError(f"{source} has an unsupported envelope schema")
    if envelope.get("algorithm") != "ML-DSA-87":
        raise FeedCheckError(f"{source} is not signed with ML-DSA-87")
    if envelope.get("key_id") != PINNED_RELEASE_KEY_ID:
        raise FeedCheckError(f"{source} is signed by an unapproved release key")

    encoded_manifest = envelope.get("manifest_base64")
    if not isinstance(encoded_manifest, str) or not encoded_manifest:
        raise FeedCheckError(f"{source} has no signed manifest payload")
    try:
        manifest = json.loads(
            base64.b64decode(encoded_manifest, validate=True).decode("utf-8")
        )
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise FeedCheckError(f"{source} has an invalid signed manifest: {error}") from error

    expected_fields = {
        "schema": "qsdm.release-manifest.v1",
        "product": "qsdm-hive",
        "channel": "stable",
        "version": expected_version,
        "commit": expected_commit,
        "platform": expected_platform,
        "key_id": PINNED_RELEASE_KEY_ID,
    }
    for field, expected in expected_fields.items():
        if manifest.get(field) != expected:
            raise FeedCheckError(
                f"{source} signed manifest {field} is {manifest.get(field)!r}; "
                f"expected {expected!r}"
            )

    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list):
        raise FeedCheckError(f"{source} signed manifest has no artifact list")
    if not any(
        isinstance(artifact, dict)
        and artifact.get("name") == expected_artifact
        and artifact.get("role") == "installer"
        for artifact in artifacts
    ):
        raise FeedCheckError(
            f"{source} does not authorize installer {expected_artifact}"
        )


def verify_feed(
    *,
    base_url: str,
    expected_version: str,
    expected_commit: str,
    fetch_text: Callable[[str], str],
    require_url: Callable[[str], None],
) -> None:
    base_url = base_url.rstrip("/")
    expected_artifacts = {
        "windows": f"qsdm-hive-{expected_version}-win-x64.exe",
        "linux": f"qsdm-hive-{expected_version}-linux-x86_64.AppImage",
    }
    manifest_names = {"windows": "latest.yml", "linux": "latest-linux.yml"}

    for platform in ("windows", "linux"):
        manifest_name = manifest_names[platform]
        manifest_url = f"{base_url}/{manifest_name}"
        updater_manifest = parse_updater_manifest(
            fetch_text(manifest_url), manifest_url
        )
        if updater_manifest.version != expected_version:
            raise FeedCheckError(
                f"{manifest_url} advertises {updater_manifest.version}; "
                f"expected {expected_version}"
            )
        expected_artifact = expected_artifacts[platform]
        if updater_manifest.path != expected_artifact:
            raise FeedCheckError(
                f"{manifest_url} points to {updater_manifest.path}; "
                f"expected {expected_artifact}"
            )
        require_url(f"{base_url}/{expected_artifact}")

        envelope_url = f"{base_url}/qsdm-hive-release-{platform}.json"
        validate_release_envelope(
            fetch_text(envelope_url),
            source=envelope_url,
            expected_version=expected_version,
            expected_commit=expected_commit,
            expected_platform=platform,
            expected_artifact=expected_artifact,
        )


def _fetch_text(url: str) -> str:
    request = urllib.request.Request(
        url, headers={"User-Agent": "qsdm-hive-release-feed-check/1"}
    )
    try:
        with urllib.request.urlopen(
            request, timeout=20, context=_certificate_context()
        ) as response:
            return response.read().decode("utf-8")
    except (urllib.error.URLError, TimeoutError, UnicodeDecodeError) as error:
        raise FeedCheckError(f"could not read {url}: {error}") from error


def _require_url(url: str) -> None:
    request = urllib.request.Request(
        url,
        method="HEAD",
        headers={"User-Agent": "qsdm-hive-release-feed-check/1"},
    )
    try:
        with urllib.request.urlopen(
            request, timeout=20, context=_certificate_context()
        ) as response:
            if response.status != 200:
                raise FeedCheckError(f"{url} returned HTTP {response.status}")
    except (urllib.error.URLError, TimeoutError) as error:
        raise FeedCheckError(f"could not reach {url}: {error}") from error


def _certificate_context() -> ssl.SSLContext:
    # Some supported Windows operator hosts use Python builds whose bundled
    # OpenSSL trust path is stale even though the Windows and Git trust stores
    # are current. Prefer certifi when the host already provides it; CI needs no
    # additional dependency and continues to use its operating-system bundle.
    try:
        import certifi
    except ImportError:
        return ssl.create_default_context()
    return ssl.create_default_context(cafile=certifi.where())


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expected-version", required=True)
    parser.add_argument("--expected-commit", required=True)
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    args = parser.parse_args()

    if not SEMVER_RE.fullmatch(args.expected_version):
        parser.error("--expected-version must be MAJOR.MINOR.PATCH")
    if not COMMIT_RE.fullmatch(args.expected_commit):
        parser.error("--expected-commit must be a full lowercase Git commit")

    try:
        verify_feed(
            base_url=args.base_url,
            expected_version=args.expected_version,
            expected_commit=args.expected_commit,
            fetch_text=_fetch_text,
            require_url=_require_url,
        )
    except FeedCheckError as error:
        print(f"QSDM Hive production feed check failed: {error}", file=sys.stderr)
        print(
            "Publish the locally signed Windows and Linux release with "
            "QSDM/deploy/scripts/publish_hive_dual_platform_release.sh, "
            "then rerun the release workflow.",
            file=sys.stderr,
        )
        return 1

    print(
        f"QSDM Hive production feed matches {args.expected_version} "
        f"at {args.expected_commit}."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

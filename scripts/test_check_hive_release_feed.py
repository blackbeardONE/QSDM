import base64
import json
import unittest

from scripts.check_hive_release_feed import (
    DEFAULT_BASE_URL,
    FeedCheckError,
    PINNED_RELEASE_KEY_ID,
    parse_updater_manifest,
    verify_feed,
)


VERSION = "1.4.16"
COMMIT = "f4e3f36bdf16dab7f568c5b2928be524cc51421f"
BASE_URL = "https://downloads.example.test"


ROTATED_V1_KEY_ID = (
    "10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1"
)


def envelope(
    platform: str,
    artifact: str,
    *,
    version: str = VERSION,
    key_id: str = PINNED_RELEASE_KEY_ID,
) -> str:
    manifest = {
        "schema": "qsdm.release-manifest.v1",
        "product": "qsdm-hive",
        "channel": "stable",
        "platform": platform,
        "version": version,
        "commit": COMMIT,
        "key_id": key_id,
        "artifacts": [{"name": artifact, "role": "installer"}],
    }
    return json.dumps(
        {
            "schema": "qsdm.signed-release.v1",
            "algorithm": "ML-DSA-87",
            "key_id": key_id,
            "manifest_base64": base64.b64encode(
                json.dumps(manifest).encode("utf-8")
            ).decode("ascii"),
        }
    )


def valid_feed() -> dict[str, str]:
    windows = f"qsdm-hive-{VERSION}-win-x64.exe"
    linux = f"qsdm-hive-{VERSION}-linux-x86_64.AppImage"
    return {
        f"{BASE_URL}/latest.yml": f"version: {VERSION}\npath: {windows}\n",
        f"{BASE_URL}/latest-linux.yml": f"version: {VERSION}\npath: {linux}\n",
        f"{BASE_URL}/qsdm-hive-release-windows-v2.json": envelope(
            "windows", windows
        ),
        f"{BASE_URL}/qsdm-hive-release-linux-v2.json": envelope("linux", linux),
    }


class HiveReleaseFeedCheckTests(unittest.TestCase):
    def test_defaults_to_v2_channel_and_key(self) -> None:
        self.assertEqual(DEFAULT_BASE_URL, "https://qsdm.tech/downloads/hive-v2")
        self.assertEqual(
            PINNED_RELEASE_KEY_ID,
            "4081bf2c4755f4c5c1565b4fac75e14a7e0b52042ac3d34bf8a7f525866c64a9",
        )

    def test_rejects_envelope_signed_by_rotated_v1_key(self) -> None:
        feed = valid_feed()
        windows = f"qsdm-hive-{VERSION}-win-x64.exe"
        feed[f"{BASE_URL}/qsdm-hive-release-windows-v2.json"] = envelope(
            "windows", windows, key_id=ROTATED_V1_KEY_ID
        )

        with self.assertRaisesRegex(FeedCheckError, "unapproved release key"):
            verify_feed(
                base_url=BASE_URL,
                expected_version=VERSION,
                expected_commit=COMMIT,
                fetch_text=feed.__getitem__,
                require_url=lambda _url: None,
            )

    def test_parses_electron_builder_manifest(self) -> None:
        manifest = parse_updater_manifest(
            "version: 1.4.16\nfiles:\n  - url: ignored\npath: app.exe\n",
            "latest.yml",
        )
        self.assertEqual(manifest.version, VERSION)
        self.assertEqual(manifest.path, "app.exe")

    def test_accepts_matching_dual_platform_feed(self) -> None:
        feed = valid_feed()
        checked_urls: list[str] = []

        verify_feed(
            base_url=BASE_URL,
            expected_version=VERSION,
            expected_commit=COMMIT,
            fetch_text=feed.__getitem__,
            require_url=checked_urls.append,
        )

        self.assertEqual(
            checked_urls,
            [
                f"{BASE_URL}/qsdm-hive-{VERSION}-win-x64.exe",
                f"{BASE_URL}/qsdm-hive-{VERSION}-linux-x86_64.AppImage",
            ],
        )

    def test_rejects_stale_updater_pointer(self) -> None:
        feed = valid_feed()
        feed[f"{BASE_URL}/latest.yml"] = (
            "version: 1.4.15\npath: qsdm-hive-1.4.15-win-x64.exe\n"
        )

        with self.assertRaisesRegex(FeedCheckError, "advertises 1.4.15"):
            verify_feed(
                base_url=BASE_URL,
                expected_version=VERSION,
                expected_commit=COMMIT,
                fetch_text=feed.__getitem__,
                require_url=lambda _url: None,
            )

    def test_rejects_envelope_for_different_release(self) -> None:
        feed = valid_feed()
        windows = f"qsdm-hive-{VERSION}-win-x64.exe"
        feed[f"{BASE_URL}/qsdm-hive-release-windows-v2.json"] = envelope(
            "windows", windows, version="1.4.15"
        )

        with self.assertRaisesRegex(FeedCheckError, "expected '1.4.16'"):
            verify_feed(
                base_url=BASE_URL,
                expected_version=VERSION,
                expected_commit=COMMIT,
                fetch_text=feed.__getitem__,
                require_url=lambda _url: None,
            )


if __name__ == "__main__":
    unittest.main()

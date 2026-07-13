from __future__ import annotations

import json
import unittest

from scripts.check_secrets import content_findings, path_finding


class SecretScannerTests(unittest.TestCase):
    def test_rejects_wallet_and_private_key_paths(self) -> None:
        self.assertIsNotNone(path_finding("custody/wallet.json"))
        self.assertIsNotNone(path_finding("server/identity.ppk"))
        self.assertIsNone(path_finding("examples/service.env.example"))

    def test_rejects_private_key_blocks(self) -> None:
        key_block = "-----BEGIN " + "PRIVATE KEY-----\nredacted\n"
        findings = content_findings("notes.txt", key_block.encode())
        self.assertIn("private-key-block", {finding.rule for finding in findings})

    def test_rejects_provider_tokens(self) -> None:
        token = "ghp_" + "A" * 36
        findings = content_findings("config.txt", token.encode())
        self.assertIn("github-access-token", {finding.rule for finding in findings})

    def test_rejects_nvidia_ngc_tokens(self) -> None:
        token = "nvapi-" + "A" * 32
        findings = content_findings("quickstart.md", token.encode())
        self.assertIn("nvidia-ngc-api-key", {finding.rule for finding in findings})

    def test_rejects_literal_qsdm_secret_assignment(self) -> None:
        content = f"QSDM_SIGNER_TOKEN={'a' * 64}\n"
        findings = content_findings("runtime.env", content.encode())
        self.assertIn(
            "literal-secret-assignment", {finding.rule for finding in findings}
        )

    def test_allows_public_addresses_and_placeholders(self) -> None:
        address = "bd7021b490c688306ca267a96d3943dfdf66166de0a9808ababcaf27cab8caff"
        content = (
            f"EXPECTED_SOURCE={address}\n"
            "QSDM_SIGNER_TOKEN=REPLACE_WITH_RANDOM_32_BYTE_SECRET\n"
        )
        self.assertEqual(content_findings("funding.env.example", content.encode()), [])

    def test_rejects_wallet_keystore_json(self) -> None:
        keystore = json.dumps(
            {
                "address": "a" * 64,
                "ciphertext": "not-a-real-secret",
                "kdf": {"name": "argon2id"},
            }
        )
        findings = content_findings("renamed.json", keystore.encode())
        self.assertIn(
            "encrypted-wallet-keystore", {finding.rule for finding in findings}
        )


if __name__ == "__main__":
    unittest.main()

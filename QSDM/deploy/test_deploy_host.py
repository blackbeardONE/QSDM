"""Regression tests for the shared Python deployment SSH resolver."""
from __future__ import annotations

import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import _deploy_host


_ENV_KEYS = (
    "QSDM_ENDPOINTS_FILE",
    "QSDM_RELEASE_SSH_TARGET",
    "QSDM_VPS_HOST",
    "QSDM_VPS_USER",
    "QSDM_VPS_PORT",
)


class DeploymentHostTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.config = Path(self.temp_dir.name) / "public-endpoints.json"
        self._environment = {name: os.environ.get(name) for name in _ENV_KEYS}
        for name in _ENV_KEYS:
            os.environ.pop(name, None)

    def tearDown(self) -> None:
        for name, value in self._environment.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value

    def write_config(self, target: str) -> None:
        self.config.write_text(json.dumps({"vps_ssh_target": target}), encoding="utf-8")
        os.environ["QSDM_ENDPOINTS_FILE"] = str(self.config)

    def test_config_target_supports_user_and_port(self) -> None:
        self.write_config("root@next-vps.example:2202")
        self.assertEqual(
            _deploy_host.target(),
            _deploy_host.DeploymentTarget("next-vps.example", "root", 2202),
        )

    def test_release_target_overrides_config_and_supports_ipv6(self) -> None:
        self.write_config("root@old-vps.example")
        os.environ["QSDM_RELEASE_SSH_TARGET"] = "deploy@[2001:db8::20]:2222"
        self.assertEqual(
            _deploy_host.target(),
            _deploy_host.DeploymentTarget("2001:db8::20", "deploy", 2222),
        )

    def test_host_user_and_port_overrides_win(self) -> None:
        self.write_config("root@old-vps.example")
        os.environ["QSDM_VPS_HOST"] = "staging-vps.example"
        os.environ["QSDM_VPS_USER"] = "qsdm-deploy"
        os.environ["QSDM_VPS_PORT"] = "2203"
        self.assertEqual(
            _deploy_host.target(),
            _deploy_host.DeploymentTarget("staging-vps.example", "qsdm-deploy", 2203),
        )

    def test_explicit_missing_config_is_not_silently_ignored(self) -> None:
        os.environ["QSDM_ENDPOINTS_FILE"] = str(self.config)
        with self.assertRaisesRegex(_deploy_host.DeploymentTargetError, "was not found"):
            _deploy_host.target()

    def test_invalid_target_is_rejected_before_connection(self) -> None:
        os.environ["QSDM_RELEASE_SSH_TARGET"] = "root@bad host"
        with self.assertRaisesRegex(_deploy_host.DeploymentTargetError, "whitespace"):
            _deploy_host.target()

    def test_malformed_unbracketed_ipv6_is_rejected(self) -> None:
        os.environ["QSDM_RELEASE_SSH_TARGET"] = "root@not:an:ipv6"
        with self.assertRaisesRegex(_deploy_host.DeploymentTargetError, "valid IPv6"):
            _deploy_host.target()

    def test_legacy_helpers_share_the_target_resolver(self) -> None:
        deploy_dir = Path(__file__).resolve().parent
        privileged = (
            "remote_apply_paramiko.py",
            "remote_bootstrap_paramiko.py",
            "remote_fix_service_paramiko.py",
            "remote_harden_ssh_paramiko.py",
            "remote_install_caddy_paramiko.py",
            "remote_verify_paramiko.py",
        )
        for filename in privileged:
            source = (deploy_dir / filename).read_text(encoding="utf-8")
            self.assertIn("port as _port", source, filename)
            self.assertIn("require_root_user", source, filename)
            self.assertNotIn("206.189.132.232", source, filename)
        command_source = (deploy_dir / "remote_cmd_paramiko.py").read_text(encoding="utf-8")
        self.assertIn("port as _port", command_source)
        self.assertNotIn("206.189.132.232", command_source)
        observability_source = (deploy_dir / "scripts" / "install_observability.sh").read_text(encoding="utf-8")
        self.assertIn("QSDM_OBSERVABILITY_SSH_TARGET", observability_source)
        self.assertNotIn("206.189.132.232", observability_source)

    def test_compatibility_default_is_a_hostname_not_retired_ip(self) -> None:
        with mock.patch.object(_deploy_host, "_DEFAULT_ENDPOINTS_PATH", self.config):
            self.assertEqual(
                _deploy_host.target(),
                _deploy_host.DeploymentTarget("node.qsdm.tech", "root", 22),
            )

    def test_privileged_helpers_reject_non_root_targets(self) -> None:
        os.environ["QSDM_RELEASE_SSH_TARGET"] = "operator@next-vps.example"
        with self.assertRaisesRegex(_deploy_host.DeploymentTargetError, "requires root SSH access"):
            _deploy_host.require_root_user("remote_apply_paramiko.py")


if __name__ == "__main__":
    unittest.main(verbosity=2)
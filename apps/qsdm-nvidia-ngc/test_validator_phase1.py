import io
import json
import os
import sys
import types
import unittest
import urllib.error
from unittest import mock

import validator_phase1


class _Response:
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, traceback):
        return False

    def read(self):
        return b"{}"


class ReportToQsdmTests(unittest.TestCase):
    def test_verified_context_uses_certifi_when_no_bundle_is_configured(self):
        expected_context = object()
        fake_certifi = types.SimpleNamespace(where=lambda: "/trusted/cacert.pem")
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.dict(
            sys.modules, {"certifi": fake_certifi}
        ), mock.patch(
            "validator_phase1.ssl.create_default_context", return_value=expected_context
        ) as create_context:
            context = validator_phase1._report_ssl_context()

        self.assertIs(context, expected_context)
        create_context.assert_called_once_with(cafile="/trusted/cacert.pem")

    def test_no_report_url_is_a_valid_local_only_run(self):
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch(
            "validator_phase1.urllib.request.urlopen"
        ) as urlopen:
            self.assertTrue(validator_phase1.maybe_report_to_qsdm({"proof": "ok"}))
            urlopen.assert_not_called()

    def test_configured_report_requires_an_ingest_secret(self):
        stderr = io.StringIO()
        with mock.patch.dict(
            os.environ,
            {"QSDM_NGC_REPORT_URL": "https://api.qsdm.test/ngc-proof"},
            clear=True,
        ), mock.patch("sys.stderr", stderr):
            self.assertFalse(validator_phase1.maybe_report_to_qsdm({"proof": "ok"}))

        message = json.loads(stderr.getvalue())
        self.assertIn("QSDM_NGC_INGEST_SECRET", message["ngc_report_error"])

    def test_successful_report_sends_the_proof_and_secret_once(self):
        captured_request = None
        captured_context = None
        expected_context = object()

        def open_request(request, **kwargs):
            nonlocal captured_request, captured_context
            captured_request = request
            captured_context = kwargs.get("context")
            return _Response()

        env = {
            "QSDM_NGC_REPORT_URL": "https://api.qsdm.test/ngc-proof",
            "QSDM_NGC_INGEST_SECRET": "test-secret",
        }
        with mock.patch.dict(os.environ, env, clear=True), mock.patch(
            "validator_phase1.urllib.request.urlopen", side_effect=open_request
        ), mock.patch(
            "validator_phase1._report_ssl_context", return_value=expected_context
        ):
            self.assertTrue(validator_phase1.maybe_report_to_qsdm({"proof": "ok"}))

        self.assertIsNotNone(captured_request)
        self.assertIs(captured_context, expected_context)
        self.assertEqual(captured_request.get_header("X-qsdm-ngc-secret"), "test-secret")
        self.assertEqual(json.loads(captured_request.data), {"proof": "ok"})

    def test_http_failure_is_reported_and_returns_false(self):
        failure = urllib.error.HTTPError(
            "https://api.qsdm.test/ngc-proof", 503, "unavailable", {}, None
        )
        stderr = io.StringIO()
        env = {
            "QSDM_NGC_REPORT_URL": "https://api.qsdm.test/ngc-proof",
            "QSDM_NGC_INGEST_SECRET": "test-secret",
        }
        with mock.patch.dict(os.environ, env, clear=True), mock.patch(
            "validator_phase1.urllib.request.urlopen", side_effect=failure
        ), mock.patch(
            "validator_phase1._report_ssl_context", return_value=object()
        ), mock.patch("sys.stderr", stderr):
            self.assertFalse(validator_phase1.maybe_report_to_qsdm({"proof": "ok"}))

        message = json.loads(stderr.getvalue())
        self.assertIn("503", message["ngc_report_error"])
        self.assertNotIn("test-secret", stderr.getvalue())

    def test_main_returns_nonzero_when_report_delivery_fails(self):
        stdout = io.StringIO()
        with mock.patch("validator_phase1.build_block", return_value={"proof": "ok"}), mock.patch(
            "validator_phase1.gossip_block_summary"
        ), mock.patch("validator_phase1.maybe_report_to_qsdm", return_value=False), mock.patch(
            "sys.stdout", stdout
        ):
            self.assertEqual(validator_phase1.main(), 1)
        self.assertEqual(stdout.getvalue(), "")


if __name__ == "__main__":
    unittest.main()

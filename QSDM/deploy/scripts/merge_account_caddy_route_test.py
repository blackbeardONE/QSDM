#!/usr/bin/env python3

import importlib.util
import pathlib
import unittest


SCRIPT = pathlib.Path(__file__).with_name("merge_account_caddy_route.py")
SPEC = importlib.util.spec_from_file_location("merge_account_caddy_route", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


FIXTURE = """{
\tadmin off
}

api.qsdm.tech, node.qsdm.tech {
\timport /etc/caddy/qsdm-edge-relay.caddy
\treverse_proxy 127.0.0.1:8443
}

qsdm.tech {
\tencode zstd gzip
\troot * /var/www/qsdm

\tredir /wallet /wallet.html 302
\tredir /wallet/ /wallet.html 302

\thandle {
\t\ttry_files {path} {path}/index.html /index.html
\t\tfile_server
\t}
}
"""


class MergeAccountCaddyRouteTests(unittest.TestCase):
    def test_merge_preserves_unrelated_live_route(self):
        merged = MODULE.merge_caddy_text(FIXTURE)
        self.assertIn("import /etc/caddy/qsdm-edge-relay.caddy", merged)
        self.assertEqual(merged.count("handle /api/account/* {"), 1)
        self.assertEqual(merged.count("reverse_proxy 127.0.0.1:8092"), 1)
        self.assertEqual(merged.count("redir /account /account/ 302"), 1)

    def test_merge_is_idempotent(self):
        once = MODULE.merge_caddy_text(FIXTURE)
        self.assertEqual(MODULE.merge_caddy_text(once), once)

    def test_crlf_is_preserved(self):
        merged = MODULE.merge_caddy_text(FIXTURE.replace("\n", "\r\n"))
        self.assertIn("\r\n", merged)
        self.assertNotIn("\n", merged.replace("\r\n", ""))

    def test_wrong_existing_upstream_is_rejected(self):
        value = MODULE.merge_caddy_text(FIXTURE).replace(
            "reverse_proxy 127.0.0.1:8092",
            "reverse_proxy 127.0.0.1:9999",
        )
        with self.assertRaisesRegex(MODULE.MergeError, "127.0.0.1:8092"):
            MODULE.merge_caddy_text(value)

    def test_account_route_outside_site_is_rejected(self):
        value = "handle /api/account/* {\n\treverse_proxy 127.0.0.1:8092\n}\n" + FIXTURE
        with self.assertRaisesRegex(MODULE.MergeError, "outside qsdm.tech"):
            MODULE.merge_caddy_text(value)

    def test_ambiguous_site_is_rejected(self):
        with self.assertRaisesRegex(MODULE.MergeError, "exactly one"):
            MODULE.merge_caddy_text(FIXTURE + FIXTURE)


if __name__ == "__main__":
    unittest.main()

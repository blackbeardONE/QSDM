"""Tests for watch-full-node.py (RUN_A_FULL_NODE.md, "Watching your node").

Run from the repository root:
    python3 -m unittest discover -s QSDM/scripts/fullnode -p 'test_watch_full_node.py' -v

The node's API, the network's API and the webhook are small HTTP servers on 127.0.0.1. systemd, /proc, the clock and
the pauses are stand-ins, so a 6-hour outage takes no time. Nothing here reaches the internet.
"""

import contextlib
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock
from urllib.parse import parse_qs, urlsplit

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "watch-full-node.py")
_spec = importlib.util.spec_from_file_location("watch_full_node", SCRIPT)
W = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(W)

os.environ["no_proxy"] = "*"  # the test servers are local; never send them through a proxy
T0 = 1794625200  # 2026-11-14 03:00 UTC: two hours before the daily summary
FAKE_HOOK = "https://discord.com/api/webhooks/0/fake-test-only-token"


class Server:
    def __init__(self, handler):
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                handler(owner, self)

            def do_POST(self):
                handler(owner, self)

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.base = f"http://127.0.0.1:{self.httpd.server_address[1]}"

    def reply(self, h, code, obj=None):
        body = json.dumps(obj if obj is not None else {}).encode()
        h.send_response(code)
        h.send_header("Content-Type", "application/json")
        h.send_header("Content-Length", str(len(body)))
        h.end_headers()
        h.wfile.write(body)

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


class FakeAPI(Server):
    """GET /api/v1/status and /api/v1/chain/blocks, like a node or api.qsdm.tech."""

    def __init__(self, tip=1000):
        self.tip, self.version = tip, "fullnode-test"
        self.down = False       # answer 503 to everything, as the network does during a producer restart
        self.blocks_down = False
        self.changed = {}       # height -> [hash, state_root, reads]: reads with the changed block (None: always)
        self.block_reads = 0
        super().__init__(FakeAPI.handle)
        self.url = self.base + "/api/v1"

    def handle(self, h):
        parts = urlsplit(h.path)
        if self.down:
            return self.reply(h, 503, {"error": "QSDM services are being migrated"})
        if parts.path == "/api/v1/status":
            return self.reply(h, 200, {"chain_tip": self.tip, "version": self.version})
        if parts.path == "/api/v1/chain/blocks":
            self.block_reads += 1
            if self.blocks_down:
                return self.reply(h, 500, {"error": "internal"})
            height = int(parse_qs(parts.query)["from"][0])
            if height > self.tip:
                return self.reply(h, 200, {"blocks": []})
            digest, root = f"hash-{height}", f"root-{height}"
            change = self.changed.get(height)
            if change and (change[2] is None or change[2] > 0):
                digest, root = change[0], change[1]
                if change[2] is not None:
                    change[2] -= 1
            return self.reply(h, 200, {"blocks": [{"height": height, "hash": digest, "state_root": root}]})
        return self.reply(h, 404)


class FakeHook(Server):
    def __init__(self):
        self.posts, self.fail = [], None
        super().__init__(FakeHook.handle)
        self.url = self.base + "/api/webhooks/0/fake-test-only-token"

    def handle(self, h):
        length = int(h.headers.get("Content-Length", "0"))
        data = json.loads(h.rfile.read(length) or b"{}")
        if self.fail:
            return self.reply(h, self.fail, {"message": "no"})
        self.posts.append(data)
        h.send_response(204)
        h.end_headers()


class FakeSystem:
    def __init__(self):
        self.info = {"LoadState": "loaded", "ActiveState": "active", "SubState": "running", "MainPID": "4242",
                     "NRestarts": "0"}
        self.kb = 2097152  # 2 GiB

    def service(self, name):
        return dict(self.info)

    def main_pid(self, name, info):
        pid = info.get("MainPID", "")
        return int(pid) if pid.isdigit() and int(pid) > 0 else None

    def rss_anon_kb(self, pid):
        return self.kb


class Harness:
    def __init__(self, test, **env):
        self.node, self.net, self.hook = FakeAPI(), FakeAPI(), FakeHook()
        self.dir = tempfile.mkdtemp(prefix="watch-test-")
        self.path = os.path.join(self.dir, "state.json")
        self.now, self.system, self.out, self.sleeps = T0, FakeSystem(), io.StringIO(), []
        self.env = {"QSDM_WATCH_NAME": "test node", "QSDM_WATCH_LOCAL_API": self.node.url,
                    "QSDM_WATCH_NETWORK_API": self.net.url, "QSDM_WATCH_WEBHOOK": self.hook.url}
        self.env.update(env)
        test.addCleanup(self.close)

    def close(self):
        for server in (self.node, self.net, self.hook):
            server.close()
        shutil.rmtree(self.dir, ignore_errors=True)

    def run(self):
        watch = W.Watch(W.Config(self.env), self.path, system=self.system, clock=lambda: self.now,
                        sleep=self.sleeps.append, out=self.out)
        return watch.run()

    def tick(self, minutes=5, net=None, node=None):
        """Moves the clock on; the network gains 6 blocks a minute, and the node the same unless told otherwise."""
        self.now += minutes * 60
        gained = minutes * 6 if net is None else net
        self.net.tip += gained
        self.node.tip += gained if node is None else node
        return self.run()

    def messages(self):
        return [p["content"] for p in self.hook.posts]

    def lines(self):
        return self.out.getvalue().splitlines()

    def state(self):
        with open(self.path, encoding="utf-8") as f:
            return json.load(f)


class WatchTest(unittest.TestCase):
    def test_first_run_says_it_started_and_all_is_well(self):
        h = Harness(self)
        self.assertEqual(h.run(), 0)
        posts = h.messages()
        self.assertEqual(len(posts), 1)
        self.assertTrue(posts[0].startswith("test node: watching started (2026-11-14 03:00 UTC)."), posts[0])
        self.assertIn("Node fullnode-test at tip 1,000, the network at 1,000.", posts[0])
        self.assertIn("Same blocks as the network at 998 and 900.", posts[0])
        self.assertIn("Node memory (RssAnon): 2.00 GiB.", posts[0])
        self.assertIn("Open problems: none.", posts[0])
        self.assertEqual(h.lines()[-1], "ok: node fullnode-test tip 1,000; network tip 1,000; "
                                        "same blocks at 998 and 900; node memory 2.00 GiB")
        self.assertEqual(h.tick(), 0)
        self.assertEqual(len(h.messages()), 1)  # nothing new to say

    def test_a_producer_restart_is_quiet(self):
        h = Harness(self)
        h.run()
        h.net.down = True  # the network's API answers 503 while the producer restarts
        self.assertEqual([h.tick(net=0, node=0), h.tick(net=0, node=0)], [0, 0])
        self.assertIn("network: no answer (HTTP 503)", h.lines()[-1])
        h.net.down = False
        self.assertEqual(h.tick(net=60, node=60), 0)
        self.assertEqual(len(h.messages()), 1)

    def test_network_down_alerts_after_15_minutes_reminds_and_clears(self):
        h = Harness(self, QSDM_WATCH_SUMMARY_HOUR="23")  # no daily summary in these 6 hours
        h.run()
        h.net.down = True
        self.assertEqual([h.tick(net=0, node=0) for _ in range(4)], [0, 0, 0, 1])
        posts = h.messages()
        self.assertEqual(len(posts), 2)
        self.assertRegex(posts[1], r"^PROBLEM on test node: the network's API \(127\.0\.0\.1:\d+\) has not answered "
                                   r"for 15 minutes \(last error: HTTP 503\)\.$")
        self.assertEqual(h.tick(minutes=300, net=0, node=0), 1)
        self.assertEqual(len(h.messages()), 2)  # no reminder before 6 hours
        h.tick(minutes=60, net=0, node=0)
        self.assertTrue(h.messages()[-1].startswith("STILL a problem on test node, since 6 hours:"), h.messages()[-1])
        h.net.down = False
        self.assertEqual(h.tick(net=6, node=6), 0)
        self.assertEqual(h.messages()[-1], "OK again on test node: the network's API answers again (after 6 hours 5 min).")
        self.assertEqual(h.state()["problems"], {})

    def test_network_stalled(self):
        h = Harness(self)
        h.run()
        self.assertEqual([h.tick(net=0, node=0) for _ in range(3)], [0, 0, 1])
        self.assertEqual(h.messages()[-1], "PROBLEM on test node: the network's tip has stayed at 1,000 for 15 minutes "
                                           "(a block normally comes every 10 seconds).")
        self.assertEqual(h.tick(), 0)
        self.assertEqual(h.messages()[-1], "OK again on test node: the network's tip moves again (after 5 minutes).")

    def test_a_failed_service_alerts_at_once(self):
        h = Harness(self)
        h.run()
        h.node.down = True
        h.system.info.update(ActiveState="failed", SubState="failed", MainPID="0", NRestarts="5")
        self.assertEqual(h.tick(node=0), 1)
        self.assertRegex(h.messages()[-1], r"^PROBLEM on test node: the node's service has failed: its API "
                                           r"\(127\.0\.0\.1:\d+\) does not answer \(HTTP 503\); systemd says "
                                           r"qsdm-follower is failed \(failed, 5 restarts\)\.$")
        self.assertNotIn("node memory", h.lines()[-1])

    def test_a_stopped_node_gets_15_minutes(self):
        h = Harness(self)
        h.run()
        h.node.down = True  # an operator's upgrade: stopped, replaced, started again
        h.system.info.update(ActiveState="inactive", SubState="dead", MainPID="0")
        self.assertEqual([h.tick(node=0) for _ in range(3)], [0, 0, 0])
        self.assertEqual(h.tick(node=0), 1)
        self.assertTrue(h.messages()[-1].startswith("PROBLEM on test node: the node has been down for 15 minutes: "))
        h.node.down = False
        h.system.info.update(ActiveState="active", SubState="running", MainPID="4243")
        h.node.tip = h.net.tip - 24
        self.assertEqual(h.tick(), 0)
        self.assertEqual(h.messages()[-1], "OK again on test node: the node answers again (after 5 minutes).")

    def test_a_stalled_node_while_the_network_moves(self):
        h = Harness(self)
        h.run()
        self.assertEqual([h.tick(node=0) for _ in range(3)], [0, 0, 1])
        self.assertEqual(h.messages()[-1], "PROBLEM on test node: the node's tip has stayed at 1,000 for 15 minutes "
                                           "while the network is at 1,090.")
        self.assertEqual(list(h.state()["problems"]), ["node-stalled"])

    def test_a_node_catching_up_gets_one_warning_with_its_speed(self):
        h = Harness(self)
        h.node.tip, h.net.tip = 100000, 200000
        h.run()
        self.assertEqual([h.tick(node=3650) for _ in range(4)], [0, 0, 0, 0])  # 730 blocks a minute
        warnings = [m for m in h.messages() if m.startswith("WARNING")]
        self.assertEqual(len(warnings), 1)
        self.assertRegex(warnings[0], r"^WARNING on test node: the node is 89,140 blocks behind the network \(its tip "
                                      r"110,950, the network's 200,090\); it gains about 730 blocks a minute, about "
                                      r"2 hours 3 min to go\.$")
        self.assertIn("same blocks at 114,598 and 114,500", h.lines()[-1])
        h.node.tip = h.net.tip
        self.assertEqual(h.tick(), 0)
        self.assertEqual(h.messages()[-1], "OK again on test node: the node has caught up with the network "
                                           "(after 10 minutes).")

    def test_warnings_get_no_reminders_but_problems_do(self):
        cfg, st = W.Config({"QSDM_WATCH_NAME": "n"}), W.new_state()
        self.assertEqual(len(W.transitions(cfg, st, {"node-behind": ("warn", "behind")}, 0)), 1)
        self.assertEqual(W.transitions(cfg, st, {"node-behind": ("warn", "behind")}, 7 * 3600), [])
        self.assertEqual(W.transitions(cfg, st, {"node-behind": ("crit", "stuck")}, 7 * 3600 + 1),
                         ["PROBLEM on n (it was a warning): stuck."])
        self.assertEqual(W.transitions(cfg, st, {"node-behind": ("crit", "stuck")}, 12 * 3600), [])
        self.assertEqual(W.transitions(cfg, st, {"node-behind": ("crit", "stuck")}, 13 * 3600 + 1),
                         ["STILL a problem on n, since 13 hours: stuck."])

    def test_different_blocks_are_read_again_then_reported(self):
        h = Harness(self)
        h.run()
        h.net.changed[1028] = ["other-hash-1028", "root-1028", None]
        self.assertEqual(h.tick(), 1)
        self.assertEqual(h.sleeps, [W.REREAD_PAUSE])
        self.assertEqual(h.messages()[-1],
                         "PROBLEM on test node: the node and the network have different blocks, also when read again: "
                         "block 1,028: the node has hash-1028 (state root root-1028), the network other-hash-1028 "
                         "(state root root-1028). As RUN_A_FULL_NODE.md says (\"If something does not match\"): stop "
                         "the node, keep its state directory as it is, and report it.")

    def test_a_difference_that_goes_away_is_not_reported(self):
        h = Harness(self)
        h.run()
        h.net.changed[1028] = ["other-hash-1028", "root-1028", 1]  # only the first read differs
        self.assertEqual(h.tick(), 0)
        self.assertEqual(h.sleeps, [W.REREAD_PAUSE])
        self.assertEqual(len(h.messages()), 1)
        self.assertIn("same blocks at 1,028 and 930", h.lines()[-1])

    def test_blocks_that_cannot_be_compared_are_a_warning(self):
        h = Harness(self)
        h.run()
        h.net.blocks_down = True
        self.assertEqual([h.tick() for _ in range(4)], [0, 0, 0, 0])
        self.assertEqual(h.messages()[-1], "WARNING on test node: the blocks could not be compared for 15 minutes "
                                           "(HTTP 500).")
        self.assertIn("blocks not compared (HTTP 500)", h.lines()[-1])

    def test_one_daily_summary_a_day_with_the_memory_a_day_ago(self):
        h = Harness(self)
        h.run()  # 03:00 UTC
        for hour in range(26):  # to 05:00 UTC the next day
            if hour == 24:
                h.system.kb = 2621440  # 2.5 GiB
            h.tick(minutes=60)
        daily = [m for m in h.messages() if " daily check " in m]
        self.assertEqual(len(daily), 2)
        self.assertTrue(daily[0].startswith("test node: daily check (2026-11-14 05:00 UTC)."), daily[0])
        self.assertTrue(daily[1].startswith("test node: daily check (2026-11-15 05:00 UTC)."), daily[1])
        self.assertIn("Node memory (RssAnon): 2.50 GiB, 2.00 GiB a day ago.", daily[1])
        self.assertIn("Last 24 hours: 24 checks; the network's API did not answer in 0, the node in 0.", daily[1])

    def test_a_webhook_that_fails_keeps_the_messages_and_never_shows_its_url(self):
        h = Harness(self)
        h.hook.fail = 500
        self.assertEqual(h.run(), 0)
        self.assertIn("webhook: not posted (HTTP 500), 1 message(s) kept for the next run", h.lines()[-1])
        self.assertEqual(len(h.state()["unsent"]), 1)
        h.hook.fail = None
        h.tick()
        self.assertEqual(len(h.messages()), 1)
        self.assertTrue(h.messages()[0].startswith("(delayed, from 03:00 UTC) test node: watching started"))
        self.assertEqual(h.state()["unsent"], [])
        h.env["QSDM_WATCH_WEBHOOK"] = "http://127.0.0.1:9/api/webhooks/0/fake-test-only-token"  # nothing listens
        h.net.down = True
        for _ in range(4):
            h.tick(net=0, node=0)
        self.assertIn("webhook: not posted (", h.lines()[-1])
        with open(h.path, encoding="utf-8") as f:
            saved = f.read()
        for text in (h.out.getvalue(), saved):
            self.assertNotIn("fake-test-only-token", text)

    def test_settings_are_checked_without_repeating_them(self):
        for env in ({"QSDM_WATCH_DOWN_MINUTES": "0"}, {"QSDM_WATCH_STALL_MINUTES": "ten"},
                    {"QSDM_WATCH_SUMMARY_HOUR": "24"}, {"QSDM_WATCH_NETWORK_API": "ftp://api.qsdm.tech"},
                    {"QSDM_WATCH_WEBHOOK": "http://discord.com/api/webhooks/0/fake-test-only-token"},
                    {"QSDM_WATCH_WEBHOOK": FAKE_HOOK + " x"}, {"QSDM_WATCH_SERVICE": "qsdm follower"},
                    {"QSDM_WATCH_NAME": 'my "node"'}):
            with self.assertRaises(W.SettingsError, msg=env) as caught:
                W.Config(env)
            self.assertNotIn("fake-test-only-token", str(caught.exception))
        cfg = W.Config({"QSDM_WATCH_WEBHOOK": FAKE_HOOK})
        self.assertEqual((cfg.local_api, cfg.network_api, cfg.service, cfg.webhook),
                         ("http://127.0.0.1:8080/api/v1", "https://api.qsdm.tech/api/v1", "qsdm-follower", FAKE_HOOK))
        self.assertEqual((cfg.down, cfg.stall, cfg.behind, cfg.remind, cfg.summary_hour), (900, 900, 30, 21600, 5))
        self.assertEqual(W.reason(ValueError("unknown url type: '" + FAKE_HOOK + "'")), "ValueError")

    def test_check_stops_on_a_wrong_setting(self):
        state = tempfile.mkdtemp(prefix="watch-test-")
        self.addCleanup(shutil.rmtree, state, True)
        env = dict(os.environ, QSDM_WATCH_STATE_DIR=state, QSDM_WATCH_WEBHOOK="http://discord.com/api/webhooks/0/"
                                                                             "fake-test-only-token")
        env.pop("STATE_DIRECTORY", None)
        done = subprocess.run([sys.executable, "-I", SCRIPT, "check"], env=env, capture_output=True, text=True,
                              timeout=60)
        self.assertEqual(done.returncode, 2, done.stderr)
        self.assertIn("watch-full-node: settings: QSDM_WATCH_WEBHOOK must be an https:// URL", done.stderr)
        self.assertNotIn("fake-test-only-token", done.stdout + done.stderr)

    def test_install_writes_its_files_and_keeps_the_webhook(self):
        root = tempfile.mkdtemp(prefix="watch-root-")
        self.addCleanup(shutil.rmtree, root, True)
        hook_file = os.path.join(root, "hook.txt")
        with open(hook_file, "w", encoding="utf-8") as f:
            f.write("\n" + FAKE_HOOK + "\n")
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = W.main(["install", "--root", root, "--no-start", "--name", "Japan full node",
                           "--webhook-file", hook_file])
        self.assertEqual(code, 0)
        self.assertIn("WATCH-INSTALL OK (not started)", out.getvalue())
        self.assertIn("webhook: yes", out.getvalue())
        self.assertNotIn("fake-test-only-token", out.getvalue())

        def read(path):
            with open(os.path.join(root, path.lstrip("/")), encoding="utf-8") as f:
                return f.read()

        env_file = read(W.ENV_FILE)
        self.assertEqual(env_file, f'QSDM_WATCH_NAME="Japan full node"\nQSDM_WATCH_WEBHOOK="{FAKE_HOOK}"\n')
        unit = read(f"{W.UNIT_DIR}/{W.UNIT}.service")
        for want in (" -I /opt/qsdm-fullnode-watch/watch-full-node.py check\n", "Type=oneshot\n", "DynamicUser=yes\n",
                     "EnvironmentFile=/etc/qsdm-fullnode-watch/watch.env\n", "StateDirectory=qsdm-fullnode-watch\n",
                     "ProtectSystem=strict\n", "NoNewPrivileges=yes\n"):
            self.assertIn(want, unit)
        self.assertIn("OnCalendar=*:0/5\n", read(f"{W.UNIT_DIR}/{W.UNIT}.timer"))
        with open(SCRIPT, "rb") as a, open(os.path.join(root, W.OPT_FILE.lstrip("/")), "rb") as b:
            self.assertEqual(a.read(), b.read())
        if os.name == "posix":
            self.assertEqual(os.stat(os.path.join(root, W.ENV_FILE.lstrip("/"))).st_mode & 0o777, 0o600)

        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(W.main(["install", "--root", root, "--no-start", "--service", "qsdm-follower"]), 0)
        self.assertEqual(read(W.ENV_FILE), f'QSDM_WATCH_NAME="Japan full node"\nQSDM_WATCH_SERVICE="qsdm-follower"\n'
                                           f'QSDM_WATCH_WEBHOOK="{FAKE_HOOK}"\n')
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(W.main(["install", "--root", root, "--no-start", "--no-webhook"]), 0)
        self.assertNotIn("WEBHOOK", read(W.ENV_FILE))
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()) as err:
            self.assertEqual(W.main(["install", "--root", root, "--no-start", "--webhook-file",
                                     os.path.join(root, "missing.txt")]), 2)
        self.assertIn("cannot read the webhook URL", err.getvalue())

    def test_status_shows_the_last_run_and_the_memory(self):
        h = Harness(self)
        h.run()
        out = io.StringIO()
        with mock.patch.dict(os.environ, {"QSDM_WATCH_STATE_DIR": h.dir}), contextlib.redirect_stdout(out), \
                mock.patch.object(W.time, "time", return_value=h.now):
            self.assertEqual(W.main(["status"]), 0)
        text = out.getvalue()
        self.assertIn("last check: 2026-11-14 03:00 UTC: ok: node fullnode-test tip 1,000", text)
        self.assertIn("open problems: none", text)
        self.assertIn("2026-11-14 03:00 UTC  2.00 GiB", text)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""watch-full-node.py: watches a QSDM full node and the network it follows.

RUN_A_FULL_NODE.md ("Watching your node") describes it. A systemd timer runs `check` every 5 minutes. Each run
repeats the guide's step 7 (the same block, hash and state root, on the node and on the network) and reads both
sides' status. It reports these problems:

  node-down        the node's API has not answered for DOWN_MINUTES; at once when systemd marks its service failed
  node-stalled     the node's tip has not moved for STALL_MINUTES while the network is ahead of it
  node-behind      the node has stayed more than BEHIND_BLOCKS behind the network for DOWN_MINUTES (a warning)
  blocks-differ    the node and the network have different blocks 2 or 100 blocks below the tip they share, also
                   when read again 20 seconds later
  compare-failed   the blocks could not be compared for DOWN_MINUTES (a warning)
  network-down     the network's public API has not answered for DOWN_MINUTES
  network-stalled  the network's tip has not moved for STALL_MINUTES

With a webhook it posts when a problem starts, every REMIND_HOURS while a critical one lasts, and when it ends, plus
a daily summary with the node's memory (RssAnon of its process). It only reads: it never stops, restarts or changes
the node. Settings come from the environment; the install writes them to /etc/qsdm-fullnode-watch/watch.env:

  QSDM_WATCH_NAME          the name in its messages (default: the host name)
  QSDM_WATCH_LOCAL_API     the node's API (default http://127.0.0.1:8080/api/v1)
  QSDM_WATCH_NETWORK_API   the network's API (default https://api.qsdm.tech/api/v1)
  QSDM_WATCH_SERVICE       the node's systemd service (default qsdm-follower)
  QSDM_WATCH_WEBHOOK       a Discord webhook URL (default: none; the results then go only to the journal)
  QSDM_WATCH_DOWN_MINUTES (15), QSDM_WATCH_STALL_MINUTES (15), QSDM_WATCH_BEHIND_BLOCKS (30),
  QSDM_WATCH_REMIND_HOURS (6), QSDM_WATCH_SUMMARY_HOUR (5, in UTC)

Commands:
  check                one run (what the timer starts): prints one line; exit 1 while a critical problem is open,
                       2 when a setting is wrong
  status               the last run, the open problems and the node's memory over the last 7 days (as root)
  install [options]    (as root) copies this file to /opt/qsdm-fullnode-watch, writes the settings and the systemd
                       service and timer, starts the timer and runs the first check; see install --help
  uninstall [--purge]  (as root) removes them; --purge also removes the saved state
"""

import argparse
import datetime
import http.client
import ipaddress
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

UNIT = "qsdm-fullnode-watch"
OPT_FILE = "/opt/qsdm-fullnode-watch/watch-full-node.py"
ENV_FILE = "/etc/qsdm-fullnode-watch/watch.env"
UNIT_DIR = "/etc/systemd/system"
STATE_DIR = "/var/lib/qsdm-fullnode-watch"
GUIDE = "https://github.com/blackbeardONE/QSDM/blob/main/QSDM/docs/docs/RUN_A_FULL_NODE.md"
USER_AGENT = "qsdm-fullnode-watch/1"
MAX_ANSWER = 4 << 20     # bytes: far above a status answer or one block
READ_TIMEOUT = 15        # seconds per request
REREAD_PAUSE = 20        # seconds: a difference is read again after this pause before it counts
POST_PAUSE = 1           # seconds between two webhook posts
DAY = 86400
SAMPLE_EVERY = 3600      # one memory sample an hour,
SAMPLES_KEPT = 90 * DAY  # kept for 90 days
UNSENT_KEPT = 20         # messages kept for the next run when the webhook does not answer

DEFAULTS = {
    "QSDM_WATCH_NAME": "",
    "QSDM_WATCH_LOCAL_API": "http://127.0.0.1:8080/api/v1",
    "QSDM_WATCH_NETWORK_API": "https://api.qsdm.tech/api/v1",
    "QSDM_WATCH_SERVICE": "qsdm-follower",
    "QSDM_WATCH_WEBHOOK": "",
    "QSDM_WATCH_DOWN_MINUTES": "15",
    "QSDM_WATCH_STALL_MINUTES": "15",
    "QSDM_WATCH_BEHIND_BLOCKS": "30",
    "QSDM_WATCH_REMIND_HOURS": "6",
    "QSDM_WATCH_SUMMARY_HOUR": "5",
}

OK_AGAIN = {
    "node-down": "the node answers again",
    "node-stalled": "the node's tip moves again",
    "node-behind": "the node has caught up with the network",
    "blocks-differ": "the node and the network have the same blocks again",
    "compare-failed": "the blocks can be compared again",
    "network-down": "the network's API answers again",
    "network-stalled": "the network's tip moves again",
}


class SettingsError(Exception):
    pass


class ReadError(Exception):
    pass


def reason(err):
    """A short reason for a failed request or command. It never contains the URL."""
    if isinstance(err, urllib.error.HTTPError):
        return f"HTTP {err.code}"
    if isinstance(err, urllib.error.URLError):
        err = err.reason
    if isinstance(err, (socket.timeout, TimeoutError, subprocess.TimeoutExpired)):
        return "timed out"
    if isinstance(err, OSError) and err.strerror:
        return err.strerror
    if isinstance(err, str):  # a URLError's plain-text reason
        return err[:120]
    return type(err).__name__  # never str(err): a ValueError from urllib repeats the URL


def is_loopback(host):
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def checked_url(value, key, allow_empty=False):
    """An http(s) URL without a trailing slash; plain http only on this machine. Errors never repeat the value."""
    if not value:
        if allow_empty:
            return ""
        raise SettingsError(f"{key} is empty")
    if any(c.isspace() or c in '"\\' for c in value):
        raise SettingsError(f"{key} has spaces, quotes or backslashes")
    try:
        parts = urllib.parse.urlsplit(value)
        host = parts.hostname or ""
    except ValueError:
        raise SettingsError(f"{key} is not a URL")
    if parts.scheme == "https" and host:
        return value.rstrip("/")
    if parts.scheme == "http" and is_loopback(host):
        return value.rstrip("/")
    raise SettingsError(f"{key} must be an https:// URL (plain http:// only for this machine)")


class Config:
    def __init__(self, env):
        def get(key):
            return env.get(key, "").strip() or DEFAULTS[key]

        def number(key, low, high=None):
            raw = get(key)
            if not re.fullmatch(r"[0-9]{1,6}", raw):
                raise SettingsError(f"{key} must be a whole number")
            value = int(raw)
            if value < low or (high is not None and value > high):
                raise SettingsError(f"{key} must be from {low}" + (f" to {high}" if high is not None else " up"))
            return value

        self.name = get("QSDM_WATCH_NAME") or socket.gethostname()
        if any(ord(c) < 32 or c in '"\\' for c in self.name):
            raise SettingsError("QSDM_WATCH_NAME has control characters, quotes or backslashes")
        self.local_api = checked_url(get("QSDM_WATCH_LOCAL_API"), "QSDM_WATCH_LOCAL_API")
        self.network_api = checked_url(get("QSDM_WATCH_NETWORK_API"), "QSDM_WATCH_NETWORK_API")
        self.service = get("QSDM_WATCH_SERVICE")
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9@._:-]{0,200}", self.service):
            raise SettingsError("QSDM_WATCH_SERVICE is not a systemd unit name")
        self.webhook = checked_url(env.get("QSDM_WATCH_WEBHOOK", "").strip(), "QSDM_WATCH_WEBHOOK", allow_empty=True)
        self.down = number("QSDM_WATCH_DOWN_MINUTES", 1) * 60
        self.stall = number("QSDM_WATCH_STALL_MINUTES", 1) * 60
        self.behind = number("QSDM_WATCH_BEHIND_BLOCKS", 1)
        self.remind = number("QSDM_WATCH_REMIND_HOURS", 1) * 3600
        self.summary_hour = number("QSDM_WATCH_SUMMARY_HOUR", 0, 23)


# --- reading ------------------------------------------------------------------------------------------------------

def fetch_json(url, proxies):
    handlers = [] if proxies else [urllib.request.ProxyHandler({})]
    opener = urllib.request.build_opener(*handlers)
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT, "Accept": "application/json"})
    try:
        with opener.open(request, timeout=READ_TIMEOUT) as answer:
            body = answer.read(MAX_ANSWER + 1)
    except (urllib.error.URLError, http.client.HTTPException, OSError, ValueError) as err:
        raise ReadError(reason(err))
    if len(body) > MAX_ANSWER:
        raise ReadError("the answer is too large")
    try:
        return json.loads(body)
    except ValueError:
        raise ReadError("the answer is not JSON")


def read_status(api, proxies):
    data = fetch_json(api + "/status", proxies)
    tip = data.get("chain_tip") if isinstance(data, dict) else None
    if not isinstance(tip, int) or isinstance(tip, bool) or tip < 0:
        raise ReadError("the status has no chain_tip")
    version = data.get("version")
    return {"tip": tip, "version": version if isinstance(version, str) else ""}


def read_block(api, height, proxies):
    data = fetch_json(f"{api}/chain/blocks?from={height}&to={height}&limit=1", proxies)
    blocks = data.get("blocks") if isinstance(data, dict) else None
    if not isinstance(blocks, list) or not blocks or not isinstance(blocks[0], dict):
        raise ReadError(f"block {height} is not in the answer")
    block = blocks[0]
    if block.get("height") != height:
        raise ReadError(f"the answer for block {height} holds another block")
    digest, root = block.get("hash"), block.get("state_root")
    if not isinstance(digest, str) or not digest or not isinstance(root, str):
        raise ReadError(f"block {height} has no hash or state_root")
    return [digest, root]


class System:
    """What the watch reads from the operating system. The tests replace it."""

    def service(self, name):
        cmd = ["systemctl", "show", "-p", "LoadState", "-p", "ActiveState", "-p", "SubState", "-p", "MainPID",
               "-p", "NRestarts", name]
        try:
            done = subprocess.run(cmd, capture_output=True, text=True, timeout=20, check=False)
        except (OSError, subprocess.SubprocessError) as err:
            return {"error": reason(err)}
        if done.returncode != 0:
            return {"error": (done.stderr.strip() or f"systemctl exit code {done.returncode}")[:200]}
        info = {}
        for line in done.stdout.splitlines():
            key, sep, value = line.partition("=")
            if sep:
                info[key] = value
        return info

    def main_pid(self, name, info):
        pid = info.get("MainPID", "")
        if pid.isdigit() and int(pid) > 0:
            return int(pid)
        try:  # without systemctl: the first process in the service's cgroup
            with open(f"/sys/fs/cgroup/system.slice/{name}.service/cgroup.procs", encoding="ascii") as procs:
                first = procs.readline().strip()
            return int(first) if first.isdigit() else None
        except OSError:
            return None

    def rss_anon_kb(self, pid):
        try:
            with open(f"/proc/{int(pid)}/status", encoding="ascii", errors="replace") as status:
                for line in status:
                    if line.startswith("RssAnon:"):
                        return int(line.split()[1])
        except (OSError, ValueError, IndexError):
            return None
        return None


# --- words --------------------------------------------------------------------------------------------------------

def span(seconds):
    minutes = int(max(seconds, 0) // 60)
    if minutes < 60:
        return f"{minutes} minute{'' if minutes == 1 else 's'}"
    hours, minutes = divmod(minutes, 60)
    if hours < 48:
        return f"{hours} hour{'' if hours == 1 else 's'}" + (f" {minutes} min" if minutes else "")
    days, hours = divmod(hours, 24)
    return f"{days} days" + (f" {hours} hour{'' if hours == 1 else 's'}" if hours else "")


def gib(kb):
    return f"{kb / 1048576:.2f} GiB"


def host(url):
    return urllib.parse.urlsplit(url).netloc


def utc(t, fmt="%Y-%m-%d %H:%M UTC"):
    return datetime.datetime.fromtimestamp(t, datetime.timezone.utc).strftime(fmt)


def short(digest):
    return digest[:16] + "…" if len(digest) > 16 else digest


# --- one run ------------------------------------------------------------------------------------------------------

def new_state():
    return {"format": 1, "net": {}, "node": {}, "cmp_fail_since": None, "problems": {}, "samples": [],
            "recent": [], "summary_day": None, "unsent": [], "last_run": None, "last_line": ""}


def load_state(path):
    try:
        with open(path, encoding="utf-8") as f:
            state = json.load(f)
    except (OSError, ValueError):
        return None
    if not isinstance(state, dict) or state.get("format") != 1:
        return None
    base = new_state()
    base.update(state)
    return base


def save_state(path, state):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(state, f, sort_keys=True)
    os.replace(tmp, path)


def evaluate(cfg, st, now, r):
    """Updates st from this run's readings r and returns the problems found now: {key: (level, text)}."""
    found = {}
    net, node, svc = r.get("net"), r.get("node"), r.get("svc") or {}
    ns, nd = st["net"], st["node"]

    if net:
        if ns.get("tip") != net["tip"]:
            ns["tip"], ns["tip_since"] = net["tip"], now
        ns["fail_since"], ns["version"] = None, net["version"]
        if now - ns["tip_since"] >= cfg.stall:
            found["network-stalled"] = ("crit", f"the network's tip has stayed at {ns['tip']:,} for "
                                                f"{span(now - ns['tip_since'])} (a block normally comes every 10 seconds)")
    else:
        ns["fail_since"] = ns.get("fail_since") or now
        if now - ns["fail_since"] >= cfg.down:
            found["network-down"] = ("crit", f"the network's API ({host(cfg.network_api)}) has not answered for "
                                             f"{span(now - ns['fail_since'])} (last error: {r.get('net_err', '?')})")

    if node:
        if nd.get("tip") != node["tip"]:
            nd["tip"], nd["tip_since"] = node["tip"], now
        nd["down_since"], nd["version"] = None, node["version"]
    else:
        nd["down_since"] = nd.get("down_since") or now
        why = [f"its API ({host(cfg.local_api)}) does not answer ({r.get('node_err', '?')})"]
        if "error" not in svc and svc.get("LoadState") == "loaded":
            why.append(f"systemd says {cfg.service} is {svc.get('ActiveState', '?')} ({svc.get('SubState', '?')}, "
                       f"{svc.get('NRestarts', '?')} restarts)")
        if svc.get("ActiveState") == "failed":
            found["node-down"] = ("crit", "the node's service has failed: " + "; ".join(why))
        elif now - nd["down_since"] >= cfg.down:
            found["node-down"] = ("crit", f"the node has been down for {span(now - nd['down_since'])}: "
                                          + "; ".join(why))

    if node and net:
        gap = net["tip"] - node["tip"]
        if gap > 0 and now - nd["tip_since"] >= cfg.stall:
            found["node-stalled"] = ("crit", f"the node's tip has stayed at {node['tip']:,} for "
                                             f"{span(now - nd['tip_since'])} while the network is at {net['tip']:,}")
        if gap > cfg.behind and "node-stalled" not in found:
            if nd.get("behind_since") is None:
                nd["behind_since"] = now
            if now - nd["behind_since"] >= cfg.down:
                text = (f"the node is {gap:,} blocks behind the network (its tip {node['tip']:,}, the network's "
                        f"{net['tip']:,})")
                prev_tip, prev_t = nd.get("prev_tip"), nd.get("prev_t")
                if isinstance(prev_tip, int) and prev_t and now > prev_t:
                    per_minute = (node["tip"] - prev_tip) * 60 / (now - prev_t)
                    text += f"; it gains about {per_minute:,.0f} blocks a minute"
                    if per_minute > 6:  # the network adds about 6 a minute
                        text += f", about {span(gap / (per_minute - 6) * 60)} to go"
                found["node-behind"] = ("warn", text)
        else:
            nd["behind_since"] = None
    else:
        nd["behind_since"] = None
    if node:
        nd["prev_tip"], nd["prev_t"] = node["tip"], now

    cmp = r.get("cmp")
    if cmp and cmp["state"] == "differ":
        where = "; ".join(f"block {d['height']:,}: the node has {short(d['node'][0])} (state root "
                          f"{short(d['node'][1])}), the network {short(d['network'][0])} (state root "
                          f"{short(d['network'][1])})" for d in cmp["detail"])
        found["blocks-differ"] = ("crit", f"the node and the network have different blocks, also when read again: "
                                          f"{where}. As RUN_A_FULL_NODE.md says (\"If something does not match\"): stop "
                                          f"the node, keep its state directory as it is, and report it")
    if cmp and cmp["state"] == "failed":
        st["cmp_fail_since"] = st.get("cmp_fail_since") or now
        if now - st["cmp_fail_since"] >= cfg.down:
            found["compare-failed"] = ("warn", f"the blocks could not be compared for "
                                               f"{span(now - st['cmp_fail_since'])} ({cmp['error']})")
    elif cmp:
        st["cmp_fail_since"] = None
    return found


def transitions(cfg, st, found, now):
    """Updates the open problems and returns the messages they call for."""
    messages, old, new = [], st["problems"], {}
    for key, (level, text) in found.items():
        word = "PROBLEM" if level == "crit" else "WARNING"
        prev = old.get(key)
        if prev is None:
            messages.append(f"{word} on {cfg.name}: {text}.")
            new[key] = {"level": level, "since": now, "posted": now, "text": text}
            continue
        entry = dict(prev, text=text)
        if level != prev["level"]:
            messages.append(f"{word} on {cfg.name} (it was a {'warning' if level == 'crit' else 'problem'}): {text}.")
            entry.update(level=level, posted=now)
        elif level == "crit" and now - prev["posted"] >= cfg.remind:
            messages.append(f"STILL a problem on {cfg.name}, since {span(now - prev['since'])}: {text}.")
            entry["posted"] = now
        new[key] = entry
    for key, prev in old.items():
        if key not in found:
            messages.append(f"OK again on {cfg.name}: {OK_AGAIN.get(key, key)} (after {span(now - prev['since'])}).")
    st["problems"] = new
    return messages


def near(samples, t):
    """The memory sample within 2 hours of time t, if any."""
    best = None
    for when, kb in samples:
        if abs(when - t) <= 7200 and (best is None or abs(when - t) < abs(best[0] - t)):
            best = (when, kb)
    return best[1] if best else None


def summary(cfg, st, r, now, first):
    net, node, cmp = r.get("net"), r.get("node"), r.get("cmp") or {}
    parts = [f"{cfg.name}: {'watching started' if first else 'daily check'} ({utc(now)})."]
    if node:
        parts.append(f"Node {node['version'] or '?'} at tip {node['tip']:,}" +
                     (f", the network at {net['tip']:,}." if net else "; the network's API does not answer."))
    else:
        parts.append("The node does not answer." + (f" The network is at {net['tip']:,}." if net else ""))
    if cmp.get("state") == "same":
        parts.append("Same blocks as the network at " + " and ".join(f"{h:,}" for h in cmp["heights"]) + ".")
    elif cmp.get("state") == "differ":
        parts.append("The blocks DIFFER from the network's.")
    kb = r.get("rss_kb")
    if kb:
        mem = f"Node memory (RssAnon): {gib(kb)}"
        for label, ago in (("a day ago", DAY), ("a week ago", 7 * DAY)):
            old = near(st["samples"], now - ago)
            if old:
                mem += f", {gib(old)} {label}"
        parts.append(mem + ".")
    recent = st["recent"]
    if recent and not first:
        parts.append(f"Last 24 hours: {len(recent)} checks; the network's API did not answer in "
                     f"{sum(1 for x in recent if not x[1])}, the node in {sum(1 for x in recent if not x[2])}.")
    open_now = st["problems"]
    parts.append("Open problems: " + (", ".join(sorted(open_now)) if open_now else "none") + ".")
    return " ".join(parts)


class Watch:
    def __init__(self, cfg, state_path, system=None, clock=time.time, sleep=time.sleep, out=None):
        self.cfg, self.state_path = cfg, state_path
        self.system = system or System()
        self.clock, self.sleep = clock, sleep
        self.out = out or sys.stdout

    def say(self, text):
        print(text, file=self.out, flush=True)

    def read(self):
        cfg, r = self.cfg, {}
        try:
            r["net"] = read_status(cfg.network_api, proxies=True)
        except ReadError as err:
            r["net_err"] = str(err)
        try:
            r["node"] = read_status(cfg.local_api, proxies=False)
        except ReadError as err:
            r["node_err"] = str(err)
        r["svc"] = self.system.service(cfg.service)
        pid = self.system.main_pid(cfg.service, r["svc"])
        r["rss_kb"] = self.system.rss_anon_kb(pid) if pid else None
        if "net" in r and "node" in r:
            r["cmp"] = self.compare(min(r["node"]["tip"], r["net"]["tip"]))
        return r

    def compare(self, shared):
        heights = [h for h in (shared - 2, shared - 100) if h >= 1]
        if not heights:
            return None
        try:
            diff = self.differ(heights)
            if diff:  # read both sides again: only a difference that stays counts
                self.sleep(REREAD_PAUSE)
                diff = self.differ(heights)
        except ReadError as err:
            return {"state": "failed", "error": str(err), "heights": heights}
        if diff:
            return {"state": "differ", "detail": diff, "heights": heights}
        return {"state": "same", "heights": heights}

    def differ(self, heights):
        out = []
        for h in heights:
            mine = read_block(self.cfg.local_api, h, proxies=False)
            theirs = read_block(self.cfg.network_api, h, proxies=True)
            if mine != theirs:
                out.append({"height": h, "node": mine, "network": theirs})
        return out

    def post(self, text):
        body = json.dumps({"content": text[:1900], "allowed_mentions": {"parse": []}}).encode("utf-8")
        request = urllib.request.Request(self.cfg.webhook, data=body, method="POST", headers={
            "Content-Type": "application/json", "User-Agent": USER_AGENT})
        try:
            with urllib.request.urlopen(request, timeout=READ_TIMEOUT) as answer:
                answer.read(65536)
        except (urllib.error.URLError, http.client.HTTPException, OSError, ValueError) as err:
            return reason(err)
        return None

    def deliver(self, st, messages, now):
        """Posts the messages kept from earlier runs, then the new ones. Returns why posting failed, if it did."""
        queue = [(t, text, True) for t, text in st["unsent"]] + [(now, text, False) for text in messages]
        st["unsent"] = []
        for i, (t, text, late) in enumerate(queue):
            if i:
                self.sleep(POST_PAUSE)
            failed = self.post(f"(delayed, from {utc(t, '%H:%M UTC')}) {text}" if late else text)
            if failed:
                st["unsent"] = [[t0, text0] for t0, text0, _ in queue[i:]][-UNSENT_KEPT:]
                return failed
        return None

    def run(self):
        cfg, now = self.cfg, self.clock()
        st = load_state(self.state_path)
        first = st is None
        st = st or new_state()
        r = self.read()
        found = evaluate(cfg, st, now, r)
        messages = transitions(cfg, st, found, now)

        kb = r.get("rss_kb")
        if kb and (not st["samples"] or now - st["samples"][-1][0] >= SAMPLE_EVERY - 60):
            st["samples"].append([now, kb])
        st["samples"] = [s for s in st["samples"] if now - s[0] <= SAMPLES_KEPT]
        st["recent"] = [x for x in st["recent"] if now - x[0] < DAY] + [[now, "net" in r, "node" in r]]

        today, hour = utc(now, "%Y-%m-%d"), int(utc(now, "%H"))
        daily = None
        if first or (hour >= cfg.summary_hour and st["summary_day"] != today):
            daily = summary(cfg, st, r, now, first)
            if hour >= cfg.summary_hour:
                st["summary_day"] = today
            self.say(daily)

        posted = None
        if cfg.webhook:
            posted = self.deliver(st, messages + ([daily] if daily else []), now)

        line = self.line(r, st)
        if posted:
            line += f"; webhook: not posted ({posted}), {len(st['unsent'])} message(s) kept for the next run"
        st["last_run"], st["last_line"] = now, line
        save_state(self.state_path, st)
        for text in messages:
            self.say(text)
        self.say(line)
        return 1 if any(p["level"] == "crit" for p in st["problems"].values()) else 0

    def line(self, r, st):
        net, node, cmp = r.get("net"), r.get("node"), r.get("cmp")
        parts = [f"node {node['version'] or '?'} tip {node['tip']:,}" if node
                 else f"node: no answer ({r.get('node_err', '?')})",
                 f"network tip {net['tip']:,}" if net else f"network: no answer ({r.get('net_err', '?')})"]
        if cmp and cmp["state"] == "same":
            parts.append("same blocks at " + " and ".join(f"{h:,}" for h in cmp["heights"]))
        elif cmp and cmp["state"] == "failed":
            parts.append(f"blocks not compared ({cmp['error']})")
        if r.get("rss_kb"):
            parts.append(f"node memory {gib(r['rss_kb'])}")
        problems = st["problems"]
        head = "ok" if not problems else "PROBLEM " + ", ".join(
            f"{k} ({p['level']}, {span(self.clock() - p['since'])})" for k, p in sorted(problems.items()))
        return f"{head}: " + "; ".join(parts)


def state_path():
    base = os.environ.get("STATE_DIRECTORY", "").split(":")[0] or os.environ.get("QSDM_WATCH_STATE_DIR") or STATE_DIR
    os.makedirs(base, exist_ok=True)
    return os.path.join(base, "state.json")


# --- install ------------------------------------------------------------------------------------------------------

def read_env_file(path):
    env = {}
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                key, sep, value = line.strip().partition("=")
                if sep and key.startswith("QSDM_WATCH_"):
                    if len(value) >= 2 and value[0] == value[-1] == '"':
                        value = value[1:-1]
                    env[key] = value
    except FileNotFoundError:
        pass
    return env


def unit_text(python):
    return f"""[Unit]
Description=QSDM full-node watch: compares this node with the network
Documentation={GUIDE}
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart={python} -I {OPT_FILE} check
EnvironmentFile={ENV_FILE}
DynamicUser=yes
StateDirectory={UNIT}
StateDirectoryMode=0700
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
NoNewPrivileges=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
TimeoutStartSec=5min
"""


TIMER_TEXT = f"""[Unit]
Description=Run the QSDM full-node watch every 5 minutes

[Timer]
OnCalendar=*:0/5
RandomizedDelaySec=20

[Install]
WantedBy=timers.target
"""


def write_file(path, data, mode):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = path + ".new"
    with open(tmp, "wb") as f:
        f.write(data)
    os.chmod(tmp, mode)
    os.replace(tmp, path)


def systemctl(*args):
    done = subprocess.run(["systemctl", *args], capture_output=True, text=True, check=False)
    return done.returncode, (done.stdout + done.stderr).strip()


def cmd_install(args):
    root = os.path.abspath(args.root) if args.root else "/"
    if root == "/" and hasattr(os, "geteuid") and os.geteuid() != 0:
        return fail("install needs root: run it with sudo")
    at = lambda path: os.path.join(root, path.lstrip("/"))  # noqa: E731
    env = read_env_file(at(ENV_FILE))
    for key, value in (("QSDM_WATCH_NAME", args.name), ("QSDM_WATCH_LOCAL_API", args.local_api),
                       ("QSDM_WATCH_NETWORK_API", args.network_api), ("QSDM_WATCH_SERVICE", args.service)):
        if value is not None:
            env[key] = value.strip()
    env.setdefault("QSDM_WATCH_NAME", socket.gethostname())
    if args.webhook_file or args.webhook_stdin:
        try:
            if args.webhook_file:
                with open(args.webhook_file, encoding="utf-8") as source:
                    lines = [line.strip() for line in source if line.strip()]
            else:
                lines = [line.strip() for line in sys.stdin if line.strip()]
        except (OSError, UnicodeDecodeError) as err:
            return fail(f"cannot read the webhook URL: {reason(err)}")
        if not lines:
            return fail("no webhook URL was given (the file or input is empty)")
        env["QSDM_WATCH_WEBHOOK"] = lines[0]
    if args.no_webhook:
        env.pop("QSDM_WATCH_WEBHOOK", None)
    try:
        cfg = Config(env)
    except SettingsError as err:
        return fail(f"settings: {err}")

    python = "/usr/bin/python3" if os.path.exists("/usr/bin/python3") else (shutil.which("python3") or sys.executable)
    with open(os.path.abspath(__file__), "rb") as f:
        me = f.read()
    # only the known settings, each checked by Config above
    settings = "".join(f'{key}="{env[key]}"\n' for key in sorted(env) if key in DEFAULTS and env[key])
    write_file(at(OPT_FILE), me, 0o755)
    write_file(at(ENV_FILE), settings.encode("utf-8"), 0o600)
    write_file(at(f"{UNIT_DIR}/{UNIT}.service"), unit_text(python).encode("utf-8"), 0o644)
    write_file(at(f"{UNIT_DIR}/{UNIT}.timer"), TIMER_TEXT.encode("utf-8"), 0o644)
    print(f"wrote {OPT_FILE}, {ENV_FILE} (mode 0600) and the {UNIT} service and timer in {UNIT_DIR}"
          + ("" if root == "/" else f", under {root}"))
    print(f"name: {cfg.name}; node API {cfg.local_api}; network API {cfg.network_api}; service {cfg.service}; "
          f"webhook: {'yes' if cfg.webhook else 'none (results only in the journal)'}")
    if args.no_start:
        print("WATCH-INSTALL OK (not started)")
        return 0
    for step in (("daemon-reload",), ("enable", "--now", f"{UNIT}.timer")):
        code, text = systemctl(*step)
        if code != 0:
            return fail(f"systemctl {' '.join(step)} failed: {text}")
    code, _ = systemctl("start", f"{UNIT}.service")
    st = load_state(os.path.join(STATE_DIR, "state.json"))
    print("first check: " + (st["last_line"] if st else "(no result saved; see journalctl -u " + UNIT + ")"))
    if code != 0:
        print("The first check found a problem (the line above says which); the watch keeps checking.")
    print(f"WATCH-INSTALL OK: a check runs every 5 minutes. "
          f"Read it with: journalctl -u {UNIT} -n 20, or sudo python3 {OPT_FILE} status")
    return 0


def cmd_uninstall(args):
    if hasattr(os, "geteuid") and os.geteuid() != 0:
        return fail("uninstall needs root: run it with sudo")
    systemctl("disable", "--now", f"{UNIT}.timer")
    systemctl("stop", f"{UNIT}.service")
    if args.purge:
        systemctl("clean", "--what=state", f"{UNIT}.service")
    for path in (f"{UNIT_DIR}/{UNIT}.service", f"{UNIT_DIR}/{UNIT}.timer", ENV_FILE, OPT_FILE):
        try:
            os.remove(path)
        except FileNotFoundError:
            pass
        try:
            os.rmdir(os.path.dirname(path))
        except OSError:
            pass
    systemctl("daemon-reload")
    print("WATCH-UNINSTALL OK" + (" (state removed)" if args.purge else f" (state kept in {STATE_DIR})"))
    return 0


def cmd_status():
    path = os.path.join(os.environ.get("QSDM_WATCH_STATE_DIR") or STATE_DIR, "state.json")
    st = load_state(path)
    if st is None:
        return fail(f"no saved state in {path} (not installed, no check yet, or not run as root)")
    print(f"last check: {utc(st['last_run']) if st['last_run'] else '?'}: {st['last_line']}")
    for key, p in sorted(st["problems"].items()):
        print(f"open: {key} ({p['level']}) since {utc(p['since'])}: {p['text']}")
    if not st["problems"]:
        print("open problems: none")
    if st["unsent"]:
        print(f"{len(st['unsent'])} message(s) wait for the webhook")
    now = time.time()
    print("node memory (RssAnon), one sample a day for the last 7 days:")
    for days in range(7, -1, -1):
        kb = near(st["samples"], now - days * DAY)
        if kb:
            print(f"  {utc(now - days * DAY, '%Y-%m-%d %H:00 UTC')}  {gib(kb)}")
    return 0


def fail(text):
    print(f"watch-full-node: {text}", file=sys.stderr)
    return 2


def main(argv=None):
    parser = argparse.ArgumentParser(prog="watch-full-node.py", description=__doc__.split("\n\n")[0],
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("check", help="one run (what the timer starts)")
    sub.add_parser("status", help="the last run, the open problems and the node's memory")
    install = sub.add_parser("install", help="install the watch and its timer (as root)")
    install.add_argument("--name", help="the name in its messages (default: the host name)")
    install.add_argument("--local-api", help=f"the node's API (default {DEFAULTS['QSDM_WATCH_LOCAL_API']})")
    install.add_argument("--network-api", help=f"the network's API (default {DEFAULTS['QSDM_WATCH_NETWORK_API']})")
    install.add_argument("--service", help=f"the node's service (default {DEFAULTS['QSDM_WATCH_SERVICE']})")
    hook = install.add_mutually_exclusive_group()
    hook.add_argument("--webhook-file", help="a file whose first line is a Discord webhook URL")
    hook.add_argument("--webhook-stdin", action="store_true", help="read the webhook URL from standard input")
    hook.add_argument("--no-webhook", action="store_true", help="remove the webhook (results only in the journal)")
    install.add_argument("--root", help="write every file under this folder instead of / (for tests and packaging)")
    install.add_argument("--no-start", action="store_true", help="write the files, but do not start anything")
    uninstall = sub.add_parser("uninstall", help="remove the watch and its timer (as root)")
    uninstall.add_argument("--purge", action="store_true", help="also remove the saved state")
    args = parser.parse_args(argv)

    if args.command == "install":
        return cmd_install(args)
    if args.command == "uninstall":
        return cmd_uninstall(args)
    if args.command == "status":
        return cmd_status()
    try:
        cfg = Config(os.environ)
    except SettingsError as err:
        return fail(f"settings: {err}")
    return Watch(cfg, state_path()).run()


if __name__ == "__main__":
    sys.exit(main())

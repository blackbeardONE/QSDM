#!/usr/bin/env python3
"""Check that QSDM's published security contacts still lead somewhere (issue #188).

A reporter found on 2026-11-11 that both channels in security.txt were dead: GitHub private vulnerability reporting
was switched off, and admin@qsdm.tech refused all mail. This check runs every week and fails loudly when:
  - https://qsdm.tech/.well-known/security.txt or /security.txt does not answer, lacks the GitHub advisory Contact or
    an email Contact, or expires within 30 days;
  - the repository's public Security page no longer offers "Report a vulnerability" (private reporting switched off);
  - the email Contact's domain has no MX record.
Whether the mailbox itself accepts mail cannot be checked from here (hosted runners cannot use SMTP port 25): test it
by hand after any change at the registrar.

Usage: python3 scripts/check_security_contacts.py   (exit 0 all good, 1 a problem, each printed on its own line)
"""
import datetime
import json
import re
import sys
import urllib.parse
import urllib.request

SECURITY_TXT = ("https://qsdm.tech/.well-known/security.txt", "https://qsdm.tech/security.txt")
ADVISORY_URL = "https://github.com/blackbeardONE/QSDM/security/advisories/new"
SECURITY_PAGE = "https://github.com/blackbeardONE/QSDM/security"
MIN_DAYS_LEFT = 30
UA = {"User-Agent": "qsdm-security-contact-check (+https://github.com/blackbeardONE/QSDM)"}


def fetch(url, timeout=30):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.status, r.read().decode("utf-8", "replace")


def parse_security_txt(text):
    """The fields of an RFC 9116 file: {lowercase name: [values]}, comments ignored."""
    fields = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or ":" not in line:
            continue
        name, value = line.split(":", 1)
        fields.setdefault(name.strip().lower(), []).append(value.strip())
    return fields


def check_security_txt(text, now, label):
    """Problems with one security.txt, as plain sentences; also returns the email domains it names."""
    problems, domains = [], []
    f = parse_security_txt(text)
    contacts = f.get("contact", [])
    if ADVISORY_URL not in contacts:
        problems.append(f"{label}: no Contact line for {ADVISORY_URL}")
    mails = [c[len("mailto:"):] for c in contacts if c.lower().startswith("mailto:")]
    if not mails:
        problems.append(f"{label}: no email Contact line")
    domains += [m.rsplit("@", 1)[-1].lower() for m in mails if "@" in m]
    expires = f.get("expires", [])
    if len(expires) != 1:
        problems.append(f"{label}: needs exactly one Expires line, has {len(expires)}")
    else:
        try:
            when = datetime.datetime.fromisoformat(expires[0].replace("Z", "+00:00"))
            left = (when - now).days
            if left < MIN_DAYS_LEFT:
                problems.append(f"{label}: Expires {expires[0]} is {left} days away (renew it)")
        except ValueError:
            problems.append(f"{label}: Expires is not a date: {expires[0]}")
    return problems, domains


def has_mx(domain):
    q = urllib.parse.urlencode({"name": domain, "type": "MX"})
    _, body = fetch("https://dns.google/resolve?" + q)
    d = json.loads(body)
    return d.get("Status") == 0 and any(a.get("type") == 15 for a in d.get("Answer", []))


def main():
    now = datetime.datetime.now(datetime.timezone.utc)
    problems, domains = [], set()
    for url in SECURITY_TXT:
        try:
            status, text = fetch(url)
        except Exception as e:  # network errors are findings, not crashes
            problems.append(f"{url}: no answer ({type(e).__name__})")
            continue
        if status != 200:
            problems.append(f"{url}: HTTP {status}")
            continue
        p, d = check_security_txt(text, now, url)
        problems += p
        domains.update(d)
    try:
        _, page = fetch(SECURITY_PAGE)
        if not re.search(r"Report a vulnerability", page):
            problems.append(f"{SECURITY_PAGE}: no 'Report a vulnerability' (private vulnerability reporting is off?)")
    except Exception as e:
        problems.append(f"{SECURITY_PAGE}: no answer ({type(e).__name__})")
    for domain in sorted(domains):
        try:
            if not has_mx(domain):
                problems.append(f"email domain {domain}: no MX record")
        except Exception as e:
            problems.append(f"email domain {domain}: DNS lookup failed ({type(e).__name__})")
    for p in problems:
        print("PROBLEM " + p)
    print("SECURITY-CONTACTS " + ("OK" if not problems else f"FAILED: {len(problems)} problem(s)"))
    return 0 if not problems else 1


if __name__ == "__main__":
    sys.exit(main())

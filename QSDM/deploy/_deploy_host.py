"""Single source of truth for the production VPS hostname / IP.

Historically every script under QSDM/deploy/ hardcoded the reference
validator's IP address (``206.189.132.232``) at the top of the file.
That made the scripts unusable by anyone who forks the repo to run
their own QSDM validator, and it meant that a future VPS migration
would require a cross-file sed pass every time.

The indirection here is deliberately the smallest thing that could
work:

* Read ``QSDM_VPS_HOST`` from the environment; that lets operators
  point any of the deploy scripts at a different node without editing
  source or re-building, which is the common case (ops runbooks,
  second-validator bring-up, test environments, forks).
* If unset, fall back to the historical reference-node IP. That value
  is not a secret -- it is already the public A record for
  ``api.qsdm.tech`` / ``dashboard.qsdm.tech`` / ``node.qsdm.tech`` in
  DNS and is documented in ``QSDM/deploy/Caddyfile``. Keeping a
  sensible default means running ``python QSDM/deploy/remote_verify_paramiko.py``
  without any env wiring still Just Works on the reference node.

``QSDM_VPS_USER`` follows the same pattern for the SSH account name;
every deploy script has always used ``root`` for the reference node,
but the indirection removes the last hardcoded assumption.
"""
from __future__ import annotations

import os

_DEFAULT_HOST = "206.189.132.232"
_DEFAULT_USER = "root"


def host() -> str:
    """Return the target VPS host (env override or historical default)."""
    return os.getenv("QSDM_VPS_HOST", _DEFAULT_HOST)


def user() -> str:
    """Return the SSH user to connect as (env override or 'root')."""
    return os.getenv("QSDM_VPS_USER", _DEFAULT_USER)

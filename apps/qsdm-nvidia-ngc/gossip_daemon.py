#!/usr/bin/env python3
"""UDP gossip listener (architecture §3.1). Bind QSDM_MESH_PORT (default 9910)."""
from __future__ import annotations

import json
import os
import socket
import sys


def main() -> int:
    port = int(os.environ.get("QSDM_MESH_PORT", "9910"))
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("0.0.0.0", port))
    print(f"gossip_daemon listening udp/0.0.0.0:{port}", flush=True)
    while True:
        data, addr = sock.recvfrom(65507)
        try:
            msg = json.loads(data.decode("utf-8"))
            print(f"from {addr[0]}:{addr[1]} {json.dumps(msg)}", flush=True)
        except (UnicodeDecodeError, json.JSONDecodeError):
            print(f"from {addr[0]}:{addr[1]} raw {len(data)}B", flush=True)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("exit", flush=True)
        sys.exit(0)

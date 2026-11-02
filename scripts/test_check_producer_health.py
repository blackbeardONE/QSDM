import json
import unittest

from scripts.check_producer_health import (
    API,
    ATTEMPT_PAUSE_S,
    TIP_WAIT_S,
    Response,
    chain_tip,
    check,
)


def status(tip: object) -> Response:
    return Response(200, json.dumps({"chain_tip": tip, "version": "x"}).encode())


class FakeFetch:
    def __init__(self, replies: dict[str, list[Response]]) -> None:
        self.replies = {url: list(rs) for url, rs in replies.items()}
        self.calls: list[str] = []

    def __call__(self, url: str) -> Response:
        self.calls.append(url)
        queue = self.replies.get(url, [])
        return queue.pop(0) if queue else Response(0, b"")


class FakeSleep:
    def __init__(self) -> None:
        self.slept: list[float] = []

    def __call__(self, seconds: float) -> None:
        self.slept.append(seconds)


STATUS = f"{API}/status"
WORK = f"{API}/mining/work"


class ProducerHealthTests(unittest.TestCase):
    def test_healthy(self) -> None:
        fetch = FakeFetch({STATUS: [status(100), status(103)], WORK: [Response(200, b"{}")]})
        sleep = FakeSleep()
        self.assertIsNone(check(fetch, sleep, attempts=3))
        self.assertEqual(sleep.slept, [TIP_WAIT_S])

    def test_api_down(self) -> None:
        fetch = FakeFetch({})
        reason = check(fetch, FakeSleep(), attempts=1)
        self.assertIn("API status unreachable", reason or "")
        self.assertIn("HTTP 0", reason or "")

    def test_chain_not_advancing(self) -> None:
        fetch = FakeFetch({STATUS: [status(100), status(100)], WORK: [Response(200, b"{}")]})
        reason = check(fetch, FakeSleep(), attempts=1)
        self.assertIn("chain not advancing", reason or "")

    def test_mining_closed_while_chain_advances(self) -> None:
        fetch = FakeFetch({STATUS: [status(100), status(104)], WORK: [Response(503, b"")]})
        reason = check(fetch, FakeSleep(), attempts=1)
        self.assertIn("mining closed", reason or "")
        self.assertIn("HTTP 503", reason or "")

    def test_retry_recovers(self) -> None:
        fetch = FakeFetch(
            {
                STATUS: [Response(502, b""), status(200), status(201)],
                WORK: [Response(200, b"{}")],
            }
        )
        sleep = FakeSleep()
        self.assertIsNone(check(fetch, sleep, attempts=3))
        self.assertEqual(sleep.slept, [ATTEMPT_PAUSE_S, TIP_WAIT_S])

    def test_all_attempts_fail_reports_last_reason(self) -> None:
        fetch = FakeFetch(
            {
                STATUS: [status(1), status(1), status(1), status(1), Response(503, b"")],
                WORK: [Response(200, b"{}")] * 3,
            }
        )
        sleep = FakeSleep()
        reason = check(fetch, sleep, attempts=3)
        self.assertIn("API status unreachable", reason or "")
        self.assertEqual(sleep.slept.count(ATTEMPT_PAUSE_S), 2)

    def test_chain_tip_rejects_bad_bodies(self) -> None:
        self.assertIsNone(chain_tip(Response(200, b"not json")))
        self.assertIsNone(chain_tip(Response(200, b"[]")))
        self.assertIsNone(chain_tip(status(True)))
        self.assertIsNone(chain_tip(status("100")))
        self.assertIsNone(chain_tip(Response(500, json.dumps({"chain_tip": 5}).encode())))
        self.assertEqual(chain_tip(status(884000)), 884000)


if __name__ == "__main__":
    unittest.main()

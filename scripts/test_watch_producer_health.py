import unittest

from scripts.report_producer_incident import IncidentReportError
from scripts.watch_producer_health import watch


class Clock:
    def __init__(self) -> None:
        self.t = 0.0

    def now(self) -> float:
        return self.t

    def sleep(self, seconds: float) -> None:
        self.t += seconds


def run(reasons, *, duration=3600.0, interval=900.0, check_cost=100.0, fail_reports=()):
    clock = Clock()
    calls = []
    starts = []
    remaining = list(reasons)

    def check():
        starts.append(clock.t)
        clock.t += check_cost
        return remaining.pop(0) if remaining else None

    def reconcile(result, reason):
        calls.append((result, reason))
        if len(calls) in fail_reports:
            raise IncidentReportError("gh failed")
        return "ok"

    code = watch(
        check=check,
        reconcile=reconcile,
        duration=duration,
        interval=interval,
        now=clock.now,
        sleep=clock.sleep,
        log=lambda line: None,
    )
    return code, calls, starts


class WatchTest(unittest.TestCase):
    def test_checks_every_interval_until_the_window_ends(self):
        code, calls, starts = run([None, None, None, None])
        self.assertEqual(code, 0)
        self.assertEqual(starts, [0.0, 900.0, 1800.0, 2700.0])
        self.assertEqual([c[0] for c in calls], ["success"] * 4)

    def test_reports_failure_and_recovery(self):
        code, calls, _ = run(["chain not advancing", "chain not advancing", None, None])
        self.assertEqual(code, 0)
        self.assertEqual(
            calls,
            [
                ("failure", "chain not advancing"),
                ("failure", "chain not advancing"),
                ("success", "healthy"),
                ("success", "healthy"),
            ],
        )

    def test_a_slow_check_does_not_shift_the_schedule_or_sleep_negative(self):
        _, _, starts = run([None] * 4, check_cost=1000.0)
        self.assertEqual(starts, [0.0, 1000.0, 2000.0, 3000.0])

    def test_a_failed_report_keeps_watching_and_fails_the_job(self):
        code, calls, _ = run([None] * 4, fail_reports=(2,))
        self.assertEqual(len(calls), 4)
        self.assertEqual(code, 1)

    def test_default_window_fits_the_six_hour_job_limit(self):
        from scripts import watch_producer_health as w

        self.assertLess(w.DURATION_S + w.INTERVAL_S, 6 * 3600)


if __name__ == "__main__":
    unittest.main()

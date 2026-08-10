import json
import unittest

from scripts.report_hive_release_feed_incident import reconcile_incident


REPOSITORY = "blackbeardONE/QSDM"
RUN_URL = "https://github.com/blackbeardONE/QSDM/actions/runs/123"
CHECKED_AT = "2026-08-08T03:45:00Z"


class FakeGh:
    def __init__(self, issues: list[dict[str, object]] | None = None) -> None:
        self.issues = issues or []
        self.calls: list[list[str]] = []

    def __call__(self, arguments: list[str]) -> str:
        self.calls.append(list(arguments))
        if arguments[:2] == ["issue", "list"]:
            return json.dumps(self.issues)
        return ""


class HiveReleaseFeedIncidentTests(unittest.TestCase):
    def test_opens_first_failure(self) -> None:
        gh = FakeGh()
        action = reconcile_incident(
            result="failure",
            repository=REPOSITORY,
            run_url=RUN_URL,
            run_gh=gh,
            checked_at=CHECKED_AT,
        )

        self.assertEqual(action, "opened")
        self.assertEqual(gh.calls[-1][:2], ["issue", "create"])
        self.assertTrue(any(RUN_URL in argument for argument in gh.calls[-1]))

    def test_updates_existing_failure(self) -> None:
        gh = FakeGh(
            [
                {
                    "number": 42,
                    "title": "[Production] QSDM Hive updater feed unhealthy",
                }
            ]
        )
        action = reconcile_incident(
            result="failure",
            repository=REPOSITORY,
            run_url=RUN_URL,
            run_gh=gh,
            checked_at=CHECKED_AT,
        )

        self.assertEqual(action, "updated")
        self.assertEqual(gh.calls[-1][:3], ["issue", "comment", "42"])

    def test_closes_incident_after_recovery(self) -> None:
        gh = FakeGh(
            [
                {
                    "number": 42,
                    "title": "[Production] QSDM Hive updater feed unhealthy",
                }
            ]
        )
        action = reconcile_incident(
            result="success",
            repository=REPOSITORY,
            run_url=RUN_URL,
            run_gh=gh,
            checked_at=CHECKED_AT,
        )

        self.assertEqual(action, "closed")
        self.assertEqual(gh.calls[-1][:3], ["issue", "close", "42"])
        self.assertIn("completed", gh.calls[-1])

    def test_healthy_feed_without_incident_is_noop(self) -> None:
        gh = FakeGh()
        action = reconcile_incident(
            result="success",
            repository=REPOSITORY,
            run_url=RUN_URL,
            run_gh=gh,
            checked_at=CHECKED_AT,
        )

        self.assertEqual(action, "healthy")
        self.assertEqual(gh.calls[-1][:2], ["issue", "list"])

    def test_cancelled_run_does_not_touch_github(self) -> None:
        gh = FakeGh()
        action = reconcile_incident(
            result="cancelled",
            repository=REPOSITORY,
            run_url=RUN_URL,
            run_gh=gh,
            checked_at=CHECKED_AT,
        )

        self.assertEqual(action, "ignored")
        self.assertEqual(gh.calls, [])


if __name__ == "__main__":
    unittest.main()

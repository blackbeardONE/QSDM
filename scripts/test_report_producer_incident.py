import json
import unittest

from scripts.report_producer_incident import (
    INCIDENT_TITLE,
    IncidentReportError,
    clean_reason,
    reconcile_incident,
)


REPOSITORY = "blackbeardONE/QSDM"
RUN_URL = "https://github.com/blackbeardONE/QSDM/actions/runs/123"
CHECKED_AT = "2026-10-19T12:00:00Z"
REASON = "mining closed (GET /mining/work HTTP 503, chain at 884000)"


class FakeGh:
    def __init__(self, issues: list[dict[str, object]] | None = None) -> None:
        self.issues = issues or []
        self.calls: list[list[str]] = []

    def __call__(self, arguments: list[str]) -> str:
        self.calls.append(list(arguments))
        if arguments[:2] == ["issue", "list"]:
            return json.dumps(self.issues)
        return ""


def reconcile(gh: FakeGh, result: str, reason: str = REASON, **kwargs: str) -> str:
    return reconcile_incident(
        result=result,
        reason=reason,
        repository=kwargs.get("repository", REPOSITORY),
        run_url=kwargs.get("run_url", RUN_URL),
        run_gh=gh,
        checked_at=CHECKED_AT,
    )


class ProducerIncidentTests(unittest.TestCase):
    def test_opens_first_failure_with_reason(self) -> None:
        gh = FakeGh()
        self.assertEqual(reconcile(gh, "failure"), "opened")
        self.assertEqual(gh.calls[-1][:2], ["issue", "create"])
        body = gh.calls[-1][gh.calls[-1].index("--body") + 1]
        self.assertIn(REASON, body)
        self.assertIn(RUN_URL, body)

    def test_open_incident_is_not_commented_again(self) -> None:
        gh = FakeGh([{"number": 7, "title": INCIDENT_TITLE}])
        self.assertEqual(reconcile(gh, "failure"), "unchanged")
        self.assertFalse(any(c[:2] in (["issue", "create"], ["issue", "comment"]) for c in gh.calls))

    def test_closes_incident_after_recovery(self) -> None:
        gh = FakeGh([{"number": 7, "title": INCIDENT_TITLE}])
        self.assertEqual(reconcile(gh, "success"), "closed")
        self.assertEqual(gh.calls[-1][:3], ["issue", "close", "7"])
        self.assertIn("completed", gh.calls[-1])

    def test_healthy_without_incident_is_noop(self) -> None:
        gh = FakeGh()
        self.assertEqual(reconcile(gh, "success"), "healthy")
        self.assertFalse(any(c[:2] == ["issue", "create"] for c in gh.calls))

    def test_other_titles_with_the_label_are_ignored(self) -> None:
        gh = FakeGh([{"number": 9, "title": "[Production] QSDM Hive updater feed unhealthy"}])
        self.assertEqual(reconcile(gh, "success"), "healthy")
        self.assertEqual(reconcile(gh, "failure"), "opened")

    def test_cancelled_or_skipped_runs_are_ignored(self) -> None:
        gh = FakeGh()
        self.assertEqual(reconcile(gh, "cancelled"), "ignored")
        self.assertEqual(reconcile(gh, "skipped"), "ignored")
        self.assertEqual(gh.calls, [])

    def test_rejects_bad_repository_and_run_url(self) -> None:
        with self.assertRaises(IncidentReportError):
            reconcile(FakeGh(), "failure", repository="not a repo")
        with self.assertRaises(IncidentReportError):
            reconcile(FakeGh(), "failure", run_url="http://example.com/run")

    def test_reason_is_reduced_to_a_short_plain_line(self) -> None:
        self.assertEqual(clean_reason(""), "no reason given")
        self.assertEqual(clean_reason("a\nb`$(x)<script>"), "ab(x)script>")
        self.assertEqual(len(clean_reason("x" * 500)), 200)
        self.assertEqual(clean_reason(REASON), REASON)


if __name__ == "__main__":
    unittest.main()

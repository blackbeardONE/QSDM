from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from scripts.check_workflow_action_pins import find_unpinned_actions


class WorkflowActionPinTests(unittest.TestCase):
    def scan(self, content: str):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir) / ".github" / "workflows"
            root.mkdir(parents=True)
            (root / "test.yml").write_text(content, encoding="utf-8")
            return find_unpinned_actions(root)

    def test_accepts_commit_pins_and_safe_local_sources(self) -> None:
        sha = "a" * 40
        findings = self.scan(
            f"steps:\n  - uses: actions/checkout@{sha} # v4\n"
            "  - uses: ./actions/local\n"
            "  - uses: docker://alpine:3.20\n"
        )
        self.assertEqual(findings, [])

    def test_rejects_tags_branches_and_short_shas(self) -> None:
        findings = self.scan(
            "steps:\n"
            "  - uses: actions/checkout@v4\n"
            "  - uses: owner/action@main\n"
            "  - uses: owner/action@deadbeef\n"
        )
        self.assertEqual(
            [finding.target for finding in findings],
            ["actions/checkout@v4", "owner/action@main", "owner/action@deadbeef"],
        )

    def test_checks_reusable_external_workflows(self) -> None:
        findings = self.scan(
            "jobs:\n  call:\n    uses: owner/repository/.github/workflows/build.yml@v1\n"
        )
        self.assertEqual(len(findings), 1)


if __name__ == "__main__":
    unittest.main()

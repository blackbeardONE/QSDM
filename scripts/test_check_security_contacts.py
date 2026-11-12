"""Unit tests for scripts/check_security_contacts.py (no network)."""
import datetime
import unittest

from scripts import check_security_contacts as c

NOW = datetime.datetime(2026, 11, 12, tzinfo=datetime.timezone.utc)
GOOD = """# comment
Contact: https://github.com/blackbeardONE/QSDM/security/advisories/new
Contact: mailto:admin@qsdm.tech
Expires: 2027-05-17T00:00:00Z
Policy: https://qsdm.tech/security.html
"""


class CheckSecurityTxt(unittest.TestCase):
    def test_good_file(self):
        problems, domains = c.check_security_txt(GOOD, NOW, "x")
        self.assertEqual(problems, [])
        self.assertEqual(domains, ["qsdm.tech"])

    def test_missing_advisory_contact(self):
        problems, _ = c.check_security_txt(GOOD.replace("Contact: https://github.com/blackbeardONE/QSDM/security/advisories/new\n", ""), NOW, "x")
        self.assertTrue(any("no Contact line" in p for p in problems))

    def test_missing_email(self):
        problems, domains = c.check_security_txt(GOOD.replace("Contact: mailto:admin@qsdm.tech\n", ""), NOW, "x")
        self.assertTrue(any("no email Contact" in p for p in problems))
        self.assertEqual(domains, [])

    def test_expiring_soon(self):
        problems, _ = c.check_security_txt(GOOD.replace("2027-05-17", "2026-11-30"), NOW, "x")
        self.assertTrue(any("days away" in p for p in problems))

    def test_two_expires(self):
        problems, _ = c.check_security_txt(GOOD + "Expires: 2028-01-01T00:00:00Z\n", NOW, "x")
        self.assertTrue(any("exactly one Expires" in p for p in problems))

    def test_bad_date(self):
        problems, _ = c.check_security_txt(GOOD.replace("2027-05-17T00:00:00Z", "soon"), NOW, "x")
        self.assertTrue(any("not a date" in p for p in problems))

    def test_comments_are_ignored(self):
        f = c.parse_security_txt("# Contact: mailto:nobody@example.com\n" + GOOD)
        self.assertEqual(len(f["contact"]), 2)


if __name__ == "__main__":
    unittest.main()

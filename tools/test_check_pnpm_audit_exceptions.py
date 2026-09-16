import copy
import datetime as dt
import unittest

from check_pnpm_audit_exceptions import check


class AuditValidationTests(unittest.TestCase):
    def setUp(self):
        self.today = dt.date(2026, 9, 14)
        self.audit = {"advisories": {}, "metadata": {"vulnerabilities": {"high": 0, "critical": 0}}}
        self.policy = {"version": 1, "exceptions": []}

    def finding(self):
        self.audit["advisories"]["1"] = {"severity": "high", "module_name": "example", "github_advisory_id": "GHSA-example"}
        self.audit["metadata"]["vulnerabilities"]["high"] = 1

    def allow(self):
        self.policy["exceptions"] = [{"package": "example", "advisory": "GHSA-example", "severity": "high", "reason": "reviewed", "mitigation": "restricted", "owner": "team", "expires_on": "2026-09-14"}]

    def test_clean(self):
        self.assertEqual([], check(self.audit, self.policy, self.today))

    def test_unknown_finding(self):
        self.finding()
        self.assertEqual(1, len(check(self.audit, self.policy, self.today)))

    def test_current_exception(self):
        self.finding(); self.allow()
        self.assertEqual([], check(self.audit, self.policy, self.today))

    def test_expired_exception(self):
        self.finding(); self.allow()
        self.policy["exceptions"][0]["expires_on"] = "2026-09-13"
        self.assertEqual(1, len(check(self.audit, self.policy, self.today)))

    def test_exception_cannot_mask_severity_change(self):
        self.finding(); self.allow()
        self.audit["advisories"]["1"]["severity"] = "critical"
        self.audit["metadata"]["vulnerabilities"] = {"high": 0, "critical": 1}
        self.assertEqual(1, len(check(self.audit, self.policy, self.today)))

    def test_missing_details(self):
        self.audit["metadata"]["vulnerabilities"]["high"] = 1
        with self.assertRaises(ValueError): check(self.audit, self.policy, self.today)

    def test_incomplete_positive_summary(self):
        self.finding(); self.allow()
        self.audit["metadata"]["vulnerabilities"]["high"] = 2
        with self.assertRaises(ValueError): check(self.audit, self.policy, self.today)

    def test_errors_and_unknown_formats(self):
        for audit in ({}, {"error": "registry unavailable"}, [], {"advisories": {}}):
            with self.subTest(audit=audit), self.assertRaises(ValueError): check(audit, self.policy, self.today)

    def test_invalid_policy(self):
        self.allow()
        duplicate = copy.deepcopy(self.policy)
        duplicate["exceptions"] *= 2
        with self.assertRaises(ValueError): check(self.audit, duplicate, self.today)
        self.policy["exceptions"][0]["expires_on"] = "invalid"
        with self.assertRaises(ValueError): check(self.audit, self.policy, self.today)


if __name__ == "__main__": unittest.main()

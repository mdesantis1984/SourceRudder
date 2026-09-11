import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


PATH = Path(__file__).resolve().parents[1] / "validate_legal_review.py"
SPEC = importlib.util.spec_from_file_location("validate_legal_review", PATH)
legal = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(legal)


class LegalReviewTests(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        self.license = self.root / "LICENSE"
        self.record = self.root / "review.json"
        self.approval = "legal-review:source-rudder/2.0.0#approved"
        self.license.write_text("approved license bytes\n", encoding="utf-8")

    def write_record(self, **overrides):
        data = {
            "schema": 1,
            "status": "approved",
            "release_version": "2.0.0",
            "license_spdx": legal.LICENSE_SPDX,
            "license_sha256": legal.file_sha256(self.license),
            "approval_reference": self.approval,
            "reviewer_name": "Qualified Reviewer",
            "reviewer_qualification": "Licensed legal professional",
            "reviewer_identifier": "bar-registration:example-1234",
            "professional_reference": "engagement:source-rudder-license-review",
            "review_scope": legal.LICENSE_NAME,
            "reviewed_at": "2026-09-10",
        }
        data.update(overrides)
        self.record.write_text(json.dumps(data, sort_keys=True), encoding="utf-8")
        return hashlib.sha256(self.record.read_bytes()).hexdigest()

    def validate(self, record_hash):
        legal.validate(self.record, self.license, "2.0.0", self.approval, record_hash)

    def test_accepts_record_bound_to_exact_license_and_protected_hash(self):
        self.validate(self.write_record())

    def test_rejects_pending_review(self):
        record_hash = self.write_record(status="pending")
        with self.assertRaises(legal.ReviewError):
            self.validate(record_hash)

    def test_rejects_license_changed_after_review(self):
        record_hash = self.write_record()
        self.license.write_text("different license bytes\n", encoding="utf-8")
        with self.assertRaises(legal.ReviewError):
            self.validate(record_hash)

    def test_rejects_record_not_matching_protected_hash(self):
        self.write_record()
        with self.assertRaises(legal.ReviewError):
            self.validate("0" * 64)

    def test_rejects_template_reviewer_identity(self):
        record_hash = self.write_record(reviewer_name="replace-with-reviewer-name")
        with self.assertRaises(legal.ReviewError):
            self.validate(record_hash)


if __name__ == "__main__":
    unittest.main()

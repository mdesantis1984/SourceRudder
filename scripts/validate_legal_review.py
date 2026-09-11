#!/usr/bin/env python3

import argparse
import datetime
import hashlib
import json
import re
from pathlib import Path


LICENSE_NAME = "SourceRudder Commercial Attribution License 1.0"
LICENSE_SPDX = "LicenseRef-SourceRudder-Commercial-Attribution-1.0"


class ReviewError(ValueError):
    pass


def file_sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def validate(record_path, license_path, version, approval_reference, record_sha256):
    if not re.fullmatch(r"[0-9a-f]{64}", record_sha256):
        raise ReviewError("record SHA-256 must be 64 lowercase hex characters")
    if file_sha256(record_path) != record_sha256:
        raise ReviewError("review record does not match the protected SHA-256")

    with Path(record_path).open(encoding="utf-8") as handle:
        record = json.load(handle)

    expected = {
        "schema": 1,
        "status": "approved",
        "release_version": version,
        "license_spdx": LICENSE_SPDX,
        "license_sha256": file_sha256(license_path),
        "approval_reference": approval_reference,
        "review_scope": LICENSE_NAME,
    }
    for key, value in expected.items():
        if record.get(key) != value:
            raise ReviewError(f"{key} must equal {value!r}")

    for key in ("reviewer_name", "reviewer_qualification", "reviewer_identifier", "professional_reference"):
        value = record.get(key)
        if not isinstance(value, str) or len(value.strip()) < 8 or "replace-with" in value.lower():
            raise ReviewError(f"{key} must identify the professional review without template values")

    if len(approval_reference.strip()) < 12 or "replace-with" in approval_reference:
        raise ReviewError("approval_reference must identify the immutable review record")

    reviewed_at = record.get("reviewed_at", "")
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", reviewed_at):
        raise ReviewError("reviewed_at must use YYYY-MM-DD")
    review_date = datetime.date.fromisoformat(reviewed_at)
    if review_date > datetime.date.today():
        raise ReviewError("reviewed_at cannot be in the future")


def main():
    parser = argparse.ArgumentParser(description="Validate the SourceRudder legal review record")
    parser.add_argument("--record", required=True)
    parser.add_argument("--license", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--approval-reference", required=True)
    parser.add_argument("--record-sha256", required=True)
    args = parser.parse_args()
    try:
        validate(args.record, args.license, args.version, args.approval_reference, args.record_sha256)
    except (OSError, json.JSONDecodeError, ReviewError) as error:
        parser.exit(1, f"legal-review: {error}\n")
    print("legal-review: PASS")


if __name__ == "__main__":
    main()

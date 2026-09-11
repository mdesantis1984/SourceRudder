# SourceRudder License Review Gate

[English](README.md) | [Español](README.es.md)

SourceRudder 2.0.0 is unreleased. The root `LICENSE` remains MIT, and no custom
license is active or represented as legally approved.

The release workflow requires independent evidence after professional legal
review. This technical gate does not determine legal sufficiency; it prevents a
release from proceeding without a review record that matches the exact license
bytes being distributed.

## Activation procedure

1. Have qualified legal counsel review the proposed license, attribution terms,
   compatibility implications, governing law, and intended distribution model.
2. Apply counsel-approved text to the root `LICENSE`, including
   `SPDX-License-Identifier: LicenseRef-SourceRudder-Commercial-Attribution-1.0`.
3. Copy `source-rudder-license-review.example.json` to
   `source-rudder-license-review.json` and have the reviewer complete every
   field, including professional identification and engagement/opinion
   references. Set `license_sha256` to the lowercase SHA-256 of the final
   `LICENSE`.
4. Commit the license and review record through normal protected review.
5. Configure a protected GitHub `release` environment with required reviewers.
   Store the immutable approval reference as
   `SOURCERUDDER_LEGAL_APPROVAL_REF` and the committed review-record SHA-256 as
   `SOURCERUDDER_LEGAL_REVIEW_SHA256` environment secrets.
6. Run the full release-readiness gate. Do not bypass a failed legal check.

Historical IA_Buscar copies retain all rights previously granted under MIT.

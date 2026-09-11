# SourceRudder 2.0 Cutover

[English](cutover.md) | [Español](cutover.es.md)

This checklist prepares the external cutover. It does not authorize bypassing
legal review, protected branches, required reviewers, or release gates.

## Private repository bootstrap

1. Create `mdesantis1984/SourceRudder` as a private repository, publish the
   verified migration snapshot to `main`, and retain IA_Buscar as a legacy
   remote and historical public repository.
2. Configure protected `main`, least-privilege Actions permissions, dependency
   alerts, issue/PR policy labels, and a restricted `release` environment.
3. Confirm CI on the published commit. CodeQL remains skipped while the private
   repository lacks GitHub Code Security licensing; Gitleaks, gosec, and
   govulncheck remain mandatory.

## Before public release

1. Complete professional review of the final SourceRudder license. Commit the
   activated `LICENSE` and matching `docs/legal/source-rudder-license-review.json`.
2. Configure the GitHub `release` environment with required reviewers and the
   `SOURCERUDDER_LEGAL_APPROVAL_REF` and
   `SOURCERUDDER_LEGAL_REVIEW_SHA256` environment secrets.
3. Preserve the final IA_Buscar 1.x tag, image, MIT license, and rollback
   artifacts. Do not rewrite or relicense historical copies.
4. Verify the local remotes point to the independent repositories:

   ```bash
   git remote get-url origin
   git remote get-url legacy
   git remote -v
   ```

5. Run `bash scripts/release-readiness.sh --identity-only` from a fresh clone of
   SourceRudder.
6. Make SourceRudder public only when licensing, security settings, repository
   metadata, external integrations, and release gates have been reviewed.

## Publish 2.0.0

1. Create the exact `v2.0.0` tag only after the full release-readiness gate
   passes on a clean worktree and the reviewed commit.
2. Push the tag. `.github/workflows/release.yml` then verifies all gates, builds
   cross-platform archives, publishes a multi-platform GHCR image with SBOM and
   provenance, renders immutable deployment assets, and creates the GitHub
   Release.
3. Verify release checksums and attestations, and confirm the published image
   reference contains the expected digest.
4. Smoke-test a controlled deployment before directing clients to SourceRudder.
5. Keep the prior 1.x deployment available through the observation window.
6. Archive IA_Buscar only after SourceRudder is public, stable, and linked from
   the legacy repository. Do not delete or rewrite its history.

## Abort conditions

Stop the cutover if the license or review record does not match its protected
hash, CI is not green, the image digest is mutable or unexpected, attestations
cannot be verified, protected settings were lost, or rollback artifacts are
missing. Follow the [rollback runbook](rollback.md) rather than improvising.

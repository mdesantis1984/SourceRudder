# SourceRudder 3.0 Cutover

[English](cutover.md) | [Español](cutover.es.md)

This checklist governs the public 3.0.0 cutover. It does not authorize bypassing
protected branches, required checks, artifact verification, or release gates.

## Repository readiness

1. Keep `mdesantis1984/SourceRudder` public and retain IA_Buscar as the legacy
   remote and historical repository.
2. Keep least-privilege Actions permissions, dependency alerts, issue/PR policy
   labels, protected `main` and `develop` branches, and the `release` environment.
3. Require green CI, CodeQL, Gitleaks, gosec, govulncheck, and release-gate checks
   on the reviewed release commit.

## Before public release

1. Verify that the root `LICENSE` is the approved MIT license for 3.0.0 and that
   release notes and distribution metadata identify MIT consistently.
2. Restrict the GitHub `release` environment to the exact approved release tag.
3. Preserve the SourceRudder 2.0.0 and final IA_Buscar 1.x tags, images, MIT
   licenses, and rollback artifacts. Do not rewrite or relicense historical copies.
4. Verify the local remotes point to the independent repositories:

   ```bash
   git remote get-url origin
   git remote get-url legacy
   git remote -v
   ```

5. Run identity and MIT license readiness checks from a fresh SourceRudder clone.
6. Confirm repository visibility, security settings, metadata, integrations, and
   release gates before tagging.
7. Confirm operators have removed obsolete IA_Recuerdo flags and `MEMORY_*`
   variables before replacing a 2.0.0 deployment.
8. Keep `main` protected with required `build`, `gate`, and `Analyze Go` checks.

## Publish 3.0.0

1. Create the exact `v3.0.0` tag only after the full release-readiness gate
   passes on a clean worktree and the approved commit.
2. Push the tag. `.github/workflows/release.yml` then verifies all gates, builds
   cross-platform archives, publishes a multi-platform GHCR image with SBOM and
   provenance, renders immutable deployment assets, and creates the GitHub
   Release.
3. Verify release checksums and attestations, and confirm the published image
   reference contains the expected digest.
4. Smoke-test a controlled deployment before directing clients to SourceRudder.
5. Keep the prior 2.0.0 deployment available through the observation window.
6. Keep the historical IA_Buscar repository and release history intact.

## Abort conditions

Stop the cutover if the license does not match its approved hash, CI is not
green, the image digest is mutable or unexpected, attestations
cannot be verified, protected settings were lost, or rollback artifacts are
missing. Follow the [rollback runbook](rollback.md) rather than improvising.

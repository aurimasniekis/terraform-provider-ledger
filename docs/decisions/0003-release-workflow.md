# ADR-0003: Release workflow and signing

Status: Accepted (2026-11-22)

## Context

Decision D19 selected a GPG-signed release for Terraform Registry publication. The signing key already registered with the Registry is provided to this repository as the secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`. ADR-0001 section 8 made every workflow read-only; a release must create a GitHub release and needs one write scope.

## Decision

1. **Trigger and tooling.** `.github/workflows/release.yml` runs on tags matching `v*`. It follows the standard provider-scaffolding release: checkout with full history, `crazy-max/ghaction-import-gpg` (reads the two secrets and outputs the fingerprint), then `goreleaser/goreleaser-action` with `release --clean`, passing the fingerprint as `GPG_FINGERPRINT`. Actions are pinned by commit SHA; GoReleaser is pinned to v2.20.3 (workflow env, Makefile and `ci.yml`; bump together).
2. **Artifacts.** `.goreleaser.yml`: a before hook runs `scripts/generate-notices.sh`, and the zip archives include `THIRD_PARTY_NOTICES.txt` next to `LICENSE` and `README.md`. Pure-Go builds (`CGO_ENABLED=0`, `-trimpath`, version through `-X main.version`) for the Registry's usual matrix (freebsd, windows, linux, darwin on amd64, 386, arm, arm64 minus darwin/386, darwin/arm and windows/arm, which are not Go targets in the toolchain go.mod pins (`go tool dist list`)), zip archives, a `_SHA256SUMS` file with a detached GPG signature (`.sig`), and `terraform-registry-manifest.json` attached as `<project>_<version>_manifest.json` and listed in the checksums. The wider matrix is chosen because the Registry serves any practitioner platform; the narrower D19 binary list is for the Ledger application only.
3. **Permissions.** Top level stays `contents: read`. Only the `release` job gets `contents: write`. `scripts/check-workflow-permissions.sh` now accepts exactly that one job-level line in `release.yml` and nothing else; every other write scope, a second grant in the file, and `contents: write` in any other workflow are still rejected (negative fixtures cover each). The Go build cache is off in the release job.
4. **Pre-tag validation.** `ci.yml` runs `make release-check release-snapshot`: `goreleaser check` plus an unsigned, unpublished snapshot build, so a broken config is found before a tag. It then runs `make release-targets-check`, which fails when the TARGETS list in `scripts/generate-notices.sh` differs from the binaries the snapshot built. Signing is only exercised by a real tag.
5. **Secrets.** Only secret names appear in the repository. The fingerprint is not secret. The snapshot target skips signing and publishing and needs no secrets.

## Consequences

- Signing and the GitHub release step cannot be exercised offline. The first tag is the first real run; see `RELEASING.md`.
- Registry publication is a manual operator step after the first signed tag.
- The MPL-2.0 notices of dependencies (ADR-0001 section 9) ship in every archive as `THIRD_PARTY_NOTICES.txt`, generated from the module cache by `scripts/generate-notices.sh`. The generator fails when a module has no licence file. Its tool choice is ours (no external licence scanner), so it can be replaced without a format change.
- Open: no SBOM is generated for the provider (D19 names an SBOM for the application images). It needs a decision on tooling before it is added.

# ADR-0001: Provider conventions, configuration precedence and CI

Status: Accepted (2026-11-20)

## Context

The repository held only a README. The provider must cover every configurable Ledger object (decision D18), but Ledger's Management API and its OpenAPI description do not exist yet, so this scaffold cannot call any endpoint. The conventions for configuration, secrets, tooling and CI are set here so that the resource tasks follow them.

## Decision

1. **Framework and versions.** Terraform Plugin Framework v1.19.0, protocol v6 (`terraform-registry-manifest.json` lists `6.0`), terraform-plugin-testing v1.16.0, tfplugindocs v0.25.0 as a Go `tool` dependency (`go tool tfplugindocs`). Versions were read from the Go module proxy on 2026-11-20. `go.mod` uses Go 1.27.4, like go-ledger. The transitive modules `golang.org/x/net`, `golang.org/x/text` and `google.golang.org/grpc` are raised above what the framework requires, because govulncheck reported called vulnerabilities in the lower versions (grpc is reached through `providerserver.Serve`).
2. **Provider address.** `registry.terraform.io/aurimasniekis/ledger`. The registry namespace is an assumption taken from the repository owner; change it in `main.go` if publishing uses another one.
3. **Configuration.** Attributes `endpoint`, `token` (`Sensitive`) and `token_file`, all optional in the schema and checked in `Configure`. Precedence, first set wins: endpoint = `endpoint` then `LEDGER_ENDPOINT`; token = `token`, then `token_file`, then `LEDGER_TOKEN`, then `LEDGER_TOKEN_FILE`. `token` and `token_file` conflict when both are set in the block. Blank values count as unset. A source that is set but unusable (missing, empty or multi-line token file) is an error and never falls through to a lower source, so a typo cannot silently use another credential. The logic is the pure function `resolve` in `internal/provider/config.go`, tested with injected environment and file readers.
4. **Secrets.** The token is held as `client.Secret`, which prints as `[REDACTED]` under every fmt verb. Diagnostics name an attribute, a path or a source, never a value; endpoint parse errors do not echo the input, because it can contain credentials. An endpoint with embedded credentials, a query or a fragment is rejected. A plain `http` endpoint to a non-loopback host produces a warning.
5. **Client.** `internal/client` builds an `*http.Client` that sends the bearer token only to the configured host, refuses other hosts and does not follow redirects. It has no API methods: **extension point, TODO(management-api)** until the OpenAPI description is served. Timeout is a 30 s placeholder; timeout and retry limits are an open product decision.
6. **Resources.** None registered. Each configurable object (D18) gets a resource and data source. Resource secrets use write-only attributes (`*_wo` plus `*_wo_version`) where the object allows it; Terraform 1.11+ is needed for those to be honoured. **Exception (D21): the API-token resource exposes the token value that Ledger generates as a `Computed`, `Sensitive` attribute, set once at create with `UseStateForUnknown`. That value is stored in Terraform state, so the resource documents a state-file warning (protect and encrypt state) and has no client-supplied `value_wo` path.** Never put a secret in a set type.
7. **Docs.** Generated with tfplugindocs into `docs/` and committed. `make docs-check` regenerates, runs `tfplugindocs validate` and fails if the generated files in `docs/` changed. `docs/decisions/` holds ADRs, is not generated, and is excluded from the check; the Registry ignores it. CI runs it.
8. **CI.** `.github/workflows/ci.yml` mirrors go-ledger's ADR-0007: top-level `permissions: contents: read` and no job write scope (enforced by `scripts/check-workflow-permissions.sh` with negative fixtures), every action pinned by full commit SHA with a version comment, actionlint and zizmor (`--offline`), golangci-lint v2.15.0, govulncheck v1.10.0, race tests, a CGO-free build and dependency review on pull requests. The same Makefile targets run locally (`make check`) and in CI. Release signing and publishing are a separate task and need write permissions, which this file never requests.
9. **Licence.** Apache-2.0 (`LICENSE`, no per-file headers), as in go-ledger ADR-0002. The HashiCorp framework, plugin, testing and docs modules (and their transitive modules) are MPL-2.0. This is accepted: they are used unmodified as Go module dependencies, and no MPL-licensed file is copied into this repository (checked for MPL, Mozilla, HashiCorp copyright and SPDX headers). MPL-2.0 is file-level copyleft, so it does not change the licence of this Apache-2.0 code. Binary releases must ship the MPL-2.0 notices of the dependencies; the release task owns that (open until then).
10. **Terraform and OpenTofu versions.** The floor is Terraform 1.11, the first release with write-only attributes. The version matrix, which the resource tasks add, runs Terraform 1.11 and the latest Terraform, and OpenTofu 1.13 and 1.12. OpenTofu 1.11 is not in the matrix: it is a documented functional floor only, and its support status was not verified. The docs job pins the CLI that tfplugindocs uses to Terraform 1.11.4 (`--tf-version` in `main.go` and in the `docs-check` target of the `Makefile`), so generated docs and validation do not depend on the terraform on PATH. Bump the pin deliberately, in both places, together with the matrix.

## Why

- Pure `resolve` plus injected readers makes the precedence testable without process environment and the provider tests parallel-safe.
- Failing on an unusable higher-precedence source is safer for a credential than falling through.
- The docs job pins the Terraform CLI version (section 10), so the CLI version does not depend on the runner image.

## Consequences

- Acceptance tests (`TF_ACC=1`) and the version matrix in section 10 are not part of this scaffold: there are no resources to test. The first resource task adds them.
- `zizmor --offline` skips online audits, and the permission policy is a text check; see go-ledger ADR-0007 for the same limits.
- A `.terraform.lock.hcl` is ignored by git because this is a provider repository, not a configuration.

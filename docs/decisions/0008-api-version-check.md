# ADR-0008: API version compatibility check in Configure

Status: Accepted (2026-11-22)

## Context

Operator decision D18 requires the provider to cover Ledger's whole Management API, and ADR-0005 (F7) asks the provider
to check, when it configures itself, that the server serves a major version it supports. Until now only the acceptance
harness checked the version, and only against an exact pin (ADR-0002). A server on a new major version would otherwise be
used silently, with a contract the provider was not written against.

## Decision

1. In `Configure`, after the client is built and before any resource or data source receives it, the provider reads
   `info.version` from `GET /api/v1/openapi.json`. That document is public and needs no token. The client still sends its token on every request, this one included,
   as it does for every request to the configured host; the document does not require it.
2. A major version other than `client.SupportedAPIMajor` (1) is an error. Its summary is "Unsupported Ledger API version",
   reported on the `endpoint` attribute, and no client is configured.
3. The same major with a newer minor version than `client.SupportedAPIMinor` (0) is a warning, "Newer Ledger API version",
   and the provider continues.
4. Patch versions are not compared.
5. An unreachable server, an answer other than HTTP 200, a document that is not JSON and a document without a usable
   `info.version` are an error, "Cannot verify the Ledger API version". The provider does not use a server it could not verify.
6. The check runs once per `Configure`, which Terraform calls for plan, apply and import, not for `validate`. The check
   is a GET, so it gets the read retry policy of ADR-0009 (429, 502, 503 and 504 answers and transport errors are retried, at most
   three times) and the client's per-attempt timeout (`client.DefaultTimeout`). Its error messages never contain the token.

## Why

- Failing closed follows the financial-integrity rule: a server that cannot be verified could return or accept the wrong
  data for a plan or apply. A warning would let the run continue against an unknown contract.
- The major version is the contract boundary. A minor version only adds operations and fields, so it is accepted with a
  warning rather than refused, which keeps the provider usable across Ledger minor releases.
- Rejected: requiring the exact pinned version, as the harness does. That would make every Ledger patch release a
  provider change; the exact pin stays a test-harness concern (ADR-0002).
- Rejected: a separate `ledger_api` data source or a `skip_version_check` setting. Neither is needed for the check, and
  an opt-out would undo point 5.

## Consequences

- Plan and apply need the server reachable when Terraform configures the provider. `terraform validate` still works offline.
- The check adds one GET per Terraform run. The check does not cache across runs.
- Unit tests stub the check in the other `Configure` tests, so they stay offline; the check itself is tested against an
  `httptest` server. The acceptance test runs it against the pinned local Ledger through Terraform.
- A refused major version cannot be produced by the pinned local Ledger, so it is covered by unit tests only.
- The check retries transient failures under the read policy of ADR-0009 (F8 in ADR-0005), so a brief restart or rate limit
  during `Configure` does not fail the run.

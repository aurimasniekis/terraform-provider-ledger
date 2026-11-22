# ADR-0002: Acceptance harness against a real local Ledger

- Status: accepted

## Context

The provider talks to Ledger's Management API (`/api/v1`). Unit tests with fakes cannot show that the two agree. The
resource tests need a real server, but must never touch a real Ledger, a bank, a model or any other network service, and
must run in CI.

## Decision

1. **Pinned build.** `internal/ledgertest/ledger.pin` records a full go-ledger commit and the OpenAPI `info.version` it
   serves. The pinned commit is `fad378e62a36d1713c1449f36684a3988ddeea1e`, a commit of go-ledger's `work` branch
   (API 1.0.0). Moving the pin is a reviewed change to that file.
2. **Built locally.** `ledgertest.Start` requires `LEDGER_ACC_SOURCE`, a go-ledger checkout. It fails unless the checkout
   is exactly at the pin with no changes to tracked files, then builds `./cmd/ledger` with `CGO_ENABLED=0`.
3. **Isolated.** A temporary directory holds the database; the key-encryption key is a synthetic constant; both listeners
   bind to loopback on free ports. The CLI and server get a minimal environment, so no `LEDGER_*` setting, credential or
   proxy of the developer leaks in. Both commands run with the temporary directory as working directory, `HOME` is that
   directory and `LEDGER_ENV_FILE` names a file that does not exist, so no `.env` from the caller's directory is read. On
   Linux the server also gets `Pdeathsig=SIGKILL`, so it cannot outlive a killed test binary (other platforms rely on the
   test cleanup). "No network services" means at runtime; only the module download of the build needs a network, and only on a cold cache.
4. **Bootstrap token via the CLI.** Before `ledger serve` starts, `ledger token create --create administrator --root`
   creates the owner and a root token, printed once on stdout. The token lives in memory (a redacting `client.Secret`),
   is never logged, and dies with the temporary database. The product's own Terraform-style provisioning is not used.
5. **Compatibility check.** The harness reads `info.version` from the served `/api/v1/openapi.json` and fails unless it is
   on the supported major (`SupportedAPIMajor`, the `/api/v1` prefix) and equals the pinned version, so API drift shows
   up as a deliberate pin update.
6. **Opt-in.** Tests skip unless `TF_ACC=1` (the convention of terraform-plugin-testing), so `make test` and
   `make check` stay free of network services and fast. `make acceptance` and a CI job (`acceptance`) run them; CI checks out go-ledger
   (public) at the pinned commit read from the pin file.

## Consequences

- Resource tests (later tasks) call `ledgertest.Start(t)` and get a URL and token.
- The harness does not verify behaviour beyond version, bootstrap token and authentication; resource coverage is later work.
- A pinned commit must stay reachable in go-ledger, otherwise CI fails at checkout.

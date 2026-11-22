# Contributing

The Ledger Terraform provider is maintained by one person. Changes arrive as pull requests.

## Setup

- Go, as set in `go.mod`.
- `make`.
- golangci-lint v2.15.0, for `make lint`.
- A `terraform` binary on `PATH` for `make docs` (tfplugindocs reads the provider schema from it). Without one, tfplugindocs downloads Terraform, which needs network access.
- Go modules, downloaded once (`go mod download`). After that, unit tests need no network access.

## Workflow

1. Branch from the integration branch (`work`) or `main`. Never push to `main` and never force-push a shared branch.
2. Keep one task or concern per pull request.
3. Run `make check` before you open a pull request: gofmt, vet, golangci-lint, race-enabled tests, the pure-Go build, the docs drift check and the workflow policy.
4. After a schema change (any `MarkdownDescription`, attribute or example), run `make docs` and commit `docs/`. CI fails when committed docs differ from the generated ones.
5. Open a pull request that says what changed, how you verified it, and what you did not verify.
6. Record a significant tradeoff in `docs/decisions/` (start from `0000-template.md`).

Release steps are in [RELEASING.md](RELEASING.md).

## Commit messages

Use [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/): `<type>(<scope>)!: <summary>`, with types `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, `perf`, `chore`. Summary in the imperative, lower case, no full stop.

## Rules that always apply

- No secrets, tokens, real bank data or real personal finance records in the repository, commits, logs, test fixtures or Terraform state examples. Fixtures are synthetic.
- Tests run offline. Unit tests use fakes (an `httptest` server or an injected function); acceptance tests (`TF_ACC=1`, not run by `make check`) only talk to a local Ledger test server, never a bank.
- Provider conventions (precedence, secrets, errors) are in [ADR-0001](docs/decisions/0001-provider-conventions.md).

## Licence

Apache-2.0 (see `LICENSE`). By contributing you agree that your contribution is licensed the same way.

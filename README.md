# terraform-provider-ledger

Terraform provider for [Ledger](https://github.com/aurimasniekis/go-ledger), a personal finance application.

**Status: scaffold.** The provider configures a connection to a Ledger server, but it registers no resources or data
sources yet, because Ledger's Management API does not exist yet. Resources for every configurable object follow once
the API is served (see [ADR-0001](docs/decisions/0001-provider-conventions.md)).

## Configuration

```hcl
provider "ledger" {
  endpoint   = "https://ledger.example.com" # or LEDGER_ENDPOINT
  token_file = "/run/secrets/ledger-token"  # or token, LEDGER_TOKEN, LEDGER_TOKEN_FILE
}
```

Explicit configuration wins over the environment. Endpoint: `endpoint`, then `LEDGER_ENDPOINT`. Token: `token`, then
`token_file`, then `LEDGER_TOKEN`, then `LEDGER_TOKEN_FILE`. The token is marked sensitive and the provider does not
print it in diagnostics. See [docs/index.md](docs/index.md) for the generated reference.

> **Debugging warning:** setting `TF_LOG_SDK_PROTO_DATA_DIR` makes terraform-plugin-go write the raw provider
> configuration to that directory, and that includes a `token` set in the provider block. Leave the variable unset
> unless you are debugging, do not share those files, and prefer `token_file` or `LEDGER_TOKEN_FILE` for the token.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). `make check` runs formatting, vet, lint, offline tests, the build, the docs
drift check and the workflow policy.

## Licence

Apache-2.0, see [LICENSE](LICENSE).

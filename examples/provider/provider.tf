# Credentials normally come from the environment (LEDGER_ENDPOINT, LEDGER_TOKEN or LEDGER_TOKEN_FILE),
# so an empty block is enough.
provider "ledger" {}

# Or set them explicitly. Prefer token_file over token so the secret stays out of configuration files.
provider "ledger" {
  alias      = "explicit"
  endpoint   = "https://ledger.example.com"
  token_file = "/run/secrets/ledger-token"
}

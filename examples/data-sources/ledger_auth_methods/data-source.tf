# Public: which sign-in methods the server offers.
data "ledger_auth_methods" "methods" {}

output "oidc_enabled" {
  value = data.ledger_auth_methods.methods.oidc.enabled
}

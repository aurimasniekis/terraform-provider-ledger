# Metadata only: Ledger never serves a token's secret again. Needs the tokens:read grant.
data "ledger_api_token" "reporting" {
  id = ledger_api_token.reporting.id
}

output "reporting_token_hint" {
  value = data.ledger_api_token.reporting.hint
}

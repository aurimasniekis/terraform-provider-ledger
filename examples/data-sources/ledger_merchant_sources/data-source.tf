# What each merchant lookup source sends off the machine, if enabled.
data "ledger_merchant_sources" "all" {}

output "enabled_sources" {
  value = [for s in data.ledger_merchant_sources.all.sources : s.name if s.enabled]
}

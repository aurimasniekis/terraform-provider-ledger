# Connections that need a new consent, and how fresh the data of each account is.
# Connecting a bank is done in Ledger's UI (consent happens at the bank); Terraform only reads.
data "ledger_bank_connections" "needing_attention" {
  status = "reconnect_required"
}

data "ledger_bank_connections" "all" {}

output "stale_or_failed_connections" {
  value = [for c in data.ledger_bank_connections.all.items : c.id if contains(["stale", "failed", "reconnect_required"], c.freshness)]
}

output "reconnect_needed" {
  value = [for c in data.ledger_bank_connections.needing_attention.items : "${c.institution.name} (${c.id})"]
}

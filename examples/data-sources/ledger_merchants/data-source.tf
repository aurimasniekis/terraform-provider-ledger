# Active merchants; set include_archived = true to list archived and merged ones too.
data "ledger_merchants" "all" {
  include_archived = true
}

output "merchant_names" {
  value = [for m in data.ledger_merchants.all.items : m.display_name]
}

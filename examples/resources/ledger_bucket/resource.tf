# A bucket groups commitments and accounts to slice the monthly outlook. It holds no money and is not a budget envelope.
resource "ledger_bucket" "fixed_costs" {
  name = "Fixed costs"
}

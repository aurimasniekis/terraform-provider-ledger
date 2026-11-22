package acceptance

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestBankDataSources reads the bank data sources of a local Ledger. A fresh Ledger has no provider application and no
// connection (connecting needs consent at a bank, which no test may do), so the lists are empty and the lookups of
// something that does not exist say so; the populated shapes are covered by the offline unit tests with a fake.
func TestBankDataSources(t *testing.T) {
	orgFixture(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "ledger_bank_applications" "all" {}
data "ledger_bank_connections" "all" {}
data "ledger_bank_connections" "active" {
  status = "active"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.#", "0"),
					resource.TestCheckResourceAttr("data.ledger_bank_connections.all", "items.#", "0"),
					resource.TestCheckResourceAttr("data.ledger_bank_connections.active", "items.#", "0"),
				),
			},
			{
				Config: `
data "ledger_bank_connection" "none" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZ099"
}
`,
				ExpectError: regexp.MustCompile(`No such bank connection`),
			},
			{
				Config: `
data "ledger_bank_institutions" "none" {
  application = "not-configured"
  country     = "LT"
}
`,
				ExpectError: regexp.MustCompile(`(?s)No such bank\s+application|Listing the bank institutions failed`),
			},
		},
	})
}

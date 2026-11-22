package acceptance

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

func commitmentConfig(rentAmount, rentSchedule, rentLeeway string) string {
	return fmt.Sprintf(`
resource "ledger_commitment" "rent" {
  name            = "Rent"
  kind            = "service"
  amount          = %[1]q
  currency        = "EUR"
  start_date      = "2026-01-01"
  priority        = "essential"
  notes           = "Lease signed in 2025"
  match_tolerance = "0.50"
  schedule = %[2]s
  %[3]s
}

resource "ledger_commitment" "quarterly_vps" {
  name       = "VPS"
  kind       = "server"
  amount     = "30.00"
  currency   = "USD"
  start_date = "2026-01-31"
  end_date   = "2027-12-31"
  schedule = {
    frequency = "monthly"
    interval  = 3
    day       = 31
  }
}

resource "ledger_commitment" "domain" {
  name       = "example.org"
  kind       = "domain"
  amount     = "12.50"
  currency   = "EUR"
  start_date = "2024-02-29"
  priority   = "flexible"
  schedule = {
    frequency = "annual"
    month     = 2
    day       = 29
  }
  leeway = {
    kind     = "deadline"
    until    = "2026-12-31"
    source   = "registrar terms"
  }
}

resource "ledger_commitment" "loan" {
  name       = "Car loan"
  kind       = "debt_repayment"
  amount     = "199.999"
  currency   = "EUR"
  start_date = "2026-03-01"
  priority   = "important"
  schedule = {
    frequency = "monthly"
    interval  = 1
    day       = 5
  }
  leeway = {
    kind = "none"
  }
}

resource "ledger_commitment" "streaming" {
  name       = "Streaming"
  kind       = "subscription"
  amount     = "9.99"
  currency   = "EUR"
  start_date = "2026-01-10"
  schedule = {
    frequency = "monthly"
    interval  = 1
    day       = 10
  }
  leeway = {
    kind = "unknown"
  }
}

data "ledger_commitment" "rent" {
  id = ledger_commitment.rent.id
}

data "ledger_commitments" "eur_services" {
  kind       = "service"
  currency   = "EUR"
  depends_on = [ledger_commitment.rent]
}

data "ledger_commitments" "all_usd" {
  currency   = "USD"
  depends_on = [ledger_commitment.quarterly_vps]
}
`, rentAmount, rentSchedule, rentLeeway)
}

const (
	monthlyOn1  = `{ frequency = "monthly", interval = 1, day = 1 }`
	annualOnJan = `{ frequency = "annual", month = 1, day = 15 }`
	periodGrace = `leeway = { kind = "period", days = 5, conditions = "after written notice" }`
)

func TestCommitmentResource(t *testing.T) {
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imp := func(name string) resource.TestStep {
		return resource.TestStep{ResourceName: name, ImportState: true, ImportStateVerify: true}
	}
	base := commitmentConfig("850.00", monthlyOn1, periodGrace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			// Invalid combinations are refused at plan time, offline of any apply.
			{Config: commitmentConfig("0.00", monthlyOn1, ""), PlanOnly: true, ExpectError: regexp.MustCompile("greater than zero")},
			{Config: commitmentConfig("850.00", `{ frequency = "annual", day = 1, interval = 1 }`, ""), PlanOnly: true, ExpectError: regexp.MustCompile("Missing schedule month")},
			{
				Config: base,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_commitment.rent", "amount", "850.00"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "currency", "EUR"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "match_tolerance", "0.50"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "schedule.frequency", "monthly"),
					resource.TestCheckNoResourceAttr("ledger_commitment.rent", "schedule.month"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "leeway.kind", "period"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "version", "1"),
					// Quarterly on day 31 and the Feb 29 annual are stored as written (the calendar rules are Ledger's).
					resource.TestCheckResourceAttr("ledger_commitment.quarterly_vps", "schedule.interval", "3"),
					resource.TestCheckResourceAttr("ledger_commitment.quarterly_vps", "end_date", "2027-12-31"),
					resource.TestCheckResourceAttr("ledger_commitment.domain", "schedule.month", "2"),
					resource.TestCheckResourceAttr("ledger_commitment.domain", "leeway.until", "2026-12-31"),
					// An amount keeps the scale it was written with.
					resource.TestCheckResourceAttr("ledger_commitment.loan", "amount", "199.999"),
					resource.TestCheckResourceAttr("ledger_commitment.loan", "leeway.kind", "none"),
					resource.TestCheckResourceAttr("ledger_commitment.streaming", "leeway.kind", "unknown"),
					// Data sources.
					resource.TestCheckResourceAttrPair("data.ledger_commitment.rent", "id", "ledger_commitment.rent", "id"),
					resource.TestCheckResourceAttr("data.ledger_commitment.rent", "amount", "850.00"),
					resource.TestCheckResourceAttr("data.ledger_commitment.rent", "schedule.day", "1"),
					resource.TestCheckResourceAttr("data.ledger_commitments.eur_services", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_commitments.eur_services", "items.0.name", "Rent"),
					resource.TestCheckResourceAttr("data.ledger_commitments.all_usd", "items.#", "1"),
				),
			},
			// An unchanged reapply plans nothing (no spurious differences from normalisation).
			{Config: base, PlanOnly: true},
			imp("ledger_commitment.rent"),
			imp("ledger_commitment.quarterly_vps"),
			imp("ledger_commitment.domain"),
			imp("ledger_commitment.loan"),
			imp("ledger_commitment.streaming"),
			// Update in place: new amount, and the schedule switches from monthly to annual while the leeway is removed.
			{
				Config: commitmentConfig("900.00", annualOnJan, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_commitment.rent", "amount", "900.00"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "schedule.frequency", "annual"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "schedule.month", "1"),
					resource.TestCheckNoResourceAttr("ledger_commitment.rent", "schedule.interval"),
					resource.TestCheckNoResourceAttr("ledger_commitment.rent", "leeway.kind"),
					resource.TestCheckResourceAttr("ledger_commitment.rent", "version", "2"),
				),
			},
			{Config: commitmentConfig("900.00", annualOnJan, ""), PlanOnly: true},
			// Drift: a change made outside Terraform is shown in the plan and applying restores the configuration.
			{
				PreConfig: func() {
					list, err := c.ListCommitments(ctx, client.CommitmentFilter{Kinds: []string{"service"}})
					if err != nil || len(list) != 1 {
						t.Fatalf("listing: %v (%d)", err, len(list))
					}
					in := list[0]
					patch := client.CommitmentInput{
						Name: "Rent (changed elsewhere)", Kind: in.Kind, Amount: in.Amount, Schedule: in.Schedule, StartDate: in.StartDate,
						EndDate: in.EndDate, Priority: in.Priority, Notes: in.Notes, MatchTolerance: in.MatchTolerance, Leeway: in.Leeway,
					}
					if _, err := c.UpdateCommitment(ctx, in.ID, in.Version, patch); err != nil {
						t.Fatal(err)
					}
				},
				Config:             commitmentConfig("900.00", annualOnJan, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: commitmentConfig("900.00", annualOnJan, ""), Check: resource.TestCheckResourceAttr("ledger_commitment.rent", "name", "Rent")},
			// Drift: a commitment deleted outside Terraform is planned for creation again.
			{
				PreConfig: func() {
					list, err := c.ListCommitments(ctx, client.CommitmentFilter{Kinds: []string{"subscription"}})
					if err != nil || len(list) != 1 {
						t.Fatalf("listing: %v (%d)", err, len(list))
					}
					if err := c.DeleteCommitment(ctx, list[0].ID, list[0].Version); err != nil {
						t.Fatal(err)
					}
				},
				Config:             commitmentConfig("900.00", annualOnJan, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: commitmentConfig("900.00", annualOnJan, ""), Check: resource.TestCheckResourceAttr("ledger_commitment.streaming", "version", "1")},
		},
	})
}

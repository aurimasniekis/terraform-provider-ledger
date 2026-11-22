package acceptance

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

// deferralMonths returns dates that do not depend on the day the test runs: a commitment that started this month has
// an occurrence due on the 1st of next month, which is in the future (it owes something and is not "not recorded"), so
// it can be deferred to a later date of that month or of the month after.
func deferralMonths() (start, month, planned, revised, later, laterMonth, revisedMonth string) {
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := first.AddDate(0, 1, 0)
	after := first.AddDate(0, 2, 0)
	return first.Format("2006-01-02"), next.Format("2006-01"), next.AddDate(0, 0, 19).Format("2006-01-02"),
		next.AddDate(0, 0, 24).Format("2006-01-02"), after.AddDate(0, 0, 4).Format("2006-01-02"), after.Format("2006-01"), next.Format("2006-01")
}

func deferralConfig(start, month, planType, date, reason, fee string) string {
	if reason != "" {
		reason = fmt.Sprintf("reason = %q", reason)
	}
	return fmt.Sprintf(`
resource "ledger_commitment" "hosting" {
  name       = "Hosting"
  kind       = "server"
  amount     = "120.00"
  currency   = "EUR"
  start_date = %[1]q
  schedule = {
    frequency = "monthly"
    interval  = 1
    day       = 1
  }
}

data "ledger_occurrence" "next" {
  commitment_id = ledger_commitment.hosting.id
  month         = %[2]q
}

resource "ledger_deferral_plan" "hosting" {
  occurrence_id = data.ledger_occurrence.next.id
  plan_type     = %[3]q
  planned_date  = %[4]q
  %[5]s
}
%[6]s

data "ledger_deferral_plan" "hosting" {
  id         = ledger_deferral_plan.hosting.id
  depends_on = [ledger_deferral_plan_adjustment.fee]
}

data "ledger_deferral_plans" "active" {
  commitment_id = ledger_commitment.hosting.id
  state         = "active"
  depends_on    = [ledger_deferral_plan.hosting, ledger_deferral_plan_adjustment.fee]
}

data "ledger_deferral_plan_history" "hosting" {
  deferral_plan_id = ledger_deferral_plan.hosting.id
  depends_on       = [ledger_deferral_plan.hosting, ledger_deferral_plan_adjustment.fee]
}
`, start, month, planType, date, reason, fee)
}

func feeBlock(amount, currency string, paid bool) string {
	return fmt.Sprintf(`
resource "ledger_deferral_plan_adjustment" "fee" {
  deferral_plan_id = ledger_deferral_plan.hosting.id
  kind             = "fee"
  amount           = %q
  currency         = %q
  note             = "late fee"
  paid             = %t
}
`, amount, currency, paid)
}

func TestDeferralPlanResource(t *testing.T) {
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start, month, planned, revised, later, laterMonth, revisedMonth := deferralMonths()
	var planID, occurrenceID string
	capture := func(st *terraform.State) error {
		planID = st.RootModule().Resources["ledger_deferral_plan.hosting"].Primary.ID
		occurrenceID = st.RootModule().Resources["ledger_deferral_plan.hosting"].Primary.Attributes["occurrence_id"]
		return nil
	}
	cfg := func(planType, date, reason, fee string) string {
		return deferralConfig(start, month, planType, date, reason, fee)
	}
	adjImportID := func(st *terraform.State) (string, error) {
		return st.RootModule().Resources["ledger_deferral_plan.hosting"].Primary.ID + "/" + st.RootModule().Resources["ledger_deferral_plan_adjustment.fee"].Primary.ID, nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		// Destroy undoes the plan: Ledger keeps it (undone) and removed the fee first.
		CheckDestroy: func(*terraform.State) error {
			p, err := c.GetDeferralPlan(ctx, planID)
			if err != nil {
				return err
			}
			if p.State != "undone" || p.UnpaidAdjustmentTotal.Amount != "0" && p.UnpaidAdjustmentTotal.Amount != "0.00" {
				return fmt.Errorf("plan after destroy: state %s, unpaid adjustments %+v", p.State, p.UnpaidAdjustmentTotal)
			}
			for _, a := range p.Adjustments {
				if a.RemovedAt == nil && a.PaidAt == nil {
					return fmt.Errorf("adjustment %s is still planned after destroy", a.ID)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			// Refused at plan time, before any request: a bad currency shape, a non-date and a bad month.
			{Config: cfg("personal", "2026-02-30", "", feeBlock("5.00", "EUR", false)), PlanOnly: true, ExpectError: regexp.MustCompile("real calendar date")},
			{Config: cfg("personal", planned, "", feeBlock("0.00", "EUR", false)), PlanOnly: true, ExpectError: regexp.MustCompile("greater than zero")},
			{Config: cfg("maybe", planned, "", feeBlock("5.00", "EUR", false)), PlanOnly: true, ExpectError: regexp.MustCompile("plan_type")},
			// Ledger refuses a planned date that is not after the due date, and names it.
			{Config: cfg("personal", start, "", feeBlock("5.00", "EUR", false)), ExpectError: regexp.MustCompile("planned_date")},
			{
				Config: cfg("personal", planned, "waiting for salary", feeBlock("5.00", "EUR", false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					capture,
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "state", "active"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "plan_type", "personal"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "planned_month", month),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "reason", "waiting for salary"),
					// History fixed when the plan was made, in the occurrence's currency, scale kept.
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "original_amount", "120.00"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "outstanding_when_planned", "120.00"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "currency", "EUR"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "original_due_date", month+"-01"),
					resource.TestCheckResourceAttrPair("ledger_deferral_plan.hosting", "commitment_id", "ledger_commitment.hosting", "id"),
					// The occurrence lookup.
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "month", month),
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "due_date", month+"-01"),
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "amount", "120.00"),
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "currency", "EUR"),
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "status", "upcoming"),
					resource.TestCheckResourceAttr("data.ledger_occurrence.next", "outstanding_amount", "120.00"),
					// A plan is not payment: the occurrence is untouched by it.
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "occurrence_status", "upcoming"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "outstanding_amount", "120.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "missed", "false"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "adjustments.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "adjustments.0.amount", "5.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "unpaid_adjustment_total", "5.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plans.active", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan_history.hosting", "events.0.action", "created"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan_history.hosting", "events.1.action", "adjustment_added"),
					resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "paid", "false"),
					resource.TestCheckNoResourceAttr("ledger_deferral_plan_adjustment.fee", "paid_at"),
				),
			},
			{Config: cfg("personal", planned, "waiting for salary", feeBlock("5.00", "EUR", false)), PlanOnly: true},
			{ResourceName: "ledger_deferral_plan.hosting", ImportState: true, ImportStateVerify: true},
			{ResourceName: "ledger_deferral_plan_adjustment.fee", ImportState: true, ImportStateVerify: true, ImportStateIdFunc: adjImportID},
			// A second plan on the same occurrence is refused by Ledger (one active plan per occurrence).
			{
				Config: cfg("personal", planned, "waiting for salary", feeBlock("5.00", "EUR", false)) + `
resource "ledger_deferral_plan" "second" {
  occurrence_id = data.ledger_occurrence.next.id
  plan_type     = "personal"
  planned_date  = "` + revised + `"
}
`,
				ExpectError: regexp.MustCompile("deferral_plan_exists"),
			},
			// Revised in place: another type, a date in the month after, no reason.
			{
				Config: cfg("creditor_agreed", later, "", feeBlock("5.00", "EUR", false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "plan_type", "creditor_agreed"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "planned_month", laterMonth),
					resource.TestCheckNoResourceAttr("ledger_deferral_plan.hosting", "reason"),
					// The original due date and amount stay as they were.
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "original_due_date", month+"-01"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "planned_month", laterMonth),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan_history.hosting", "events.2.action", "revised"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan_history.hosting", "events.2.plan_type", "creditor_agreed"),
				),
			},
			{Config: cfg("creditor_agreed", later, "", feeBlock("5.00", "EUR", false)), PlanOnly: true},
			// The fee is marked paid in place: the person's own marker, not payment of the occurrence.
			{
				Config: cfg("creditor_agreed", later, "", feeBlock("5.00", "EUR", true)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "paid", "true"),
					resource.TestCheckResourceAttrSet("ledger_deferral_plan_adjustment.fee", "paid_at"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "unpaid_adjustment_total", "0.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "adjustment_total", "5.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "outstanding_amount", "120.00"),
					resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "occurrence_status", "upcoming"),
				),
			},
			{Config: cfg("creditor_agreed", later, "", feeBlock("5.00", "EUR", true)), PlanOnly: true},
			// And marked unpaid again (reversible).
			{
				Config: cfg("creditor_agreed", later, "", feeBlock("5.00", "EUR", false)),
				Check:  resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "unpaid_adjustment_total", "5.00"),
			},
			// The amount keeps its scale and a change replaces the adjustment; a different currency than the plan's is refused.
			{Config: cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)), Check: resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "amount", "7.500")},
			{Config: cfg("creditor_agreed", later, "", feeBlock("7.500", "USD", false)), ExpectError: regexp.MustCompile("(?i)currency")},
			// Drift: a person removes the fee in Ledger; the plan shows it and applying records it again.
			{
				PreConfig: func() {
					p, err := c.GetDeferralPlan(ctx, planID)
					if err != nil {
						t.Fatal(err)
					}
					for _, a := range p.Adjustments {
						if a.RemovedAt == nil {
							if _, err := c.RemoveDeferralAdjustment(ctx, planID, a.ID, p.Version); err != nil {
								t.Fatal(err)
							}
						}
					}
				},
				Config:             cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)), Check: resource.TestCheckResourceAttr("data.ledger_deferral_plan.hosting", "unpaid_adjustment_total", "7.500")},
			// Drift: the plan is revised in Ledger; Terraform shows it and puts the configuration back.
			{
				PreConfig: func() {
					p, err := c.GetDeferralPlan(ctx, planID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := c.ReviseDeferralPlan(ctx, planID, p.Version, client.DeferralPlanInput{PlanType: "personal", PlannedDate: revised}); err != nil {
						t.Fatal(err)
					}
				},
				Config:             cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)), Check: resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "planned_month", laterMonth)},
			// Drift: the fee is paid out of band and the plan is undone by a person: both leave state and are created again.
			{
				PreConfig: func() {
					p, err := c.GetDeferralPlan(ctx, planID)
					if err != nil {
						t.Fatal(err)
					}
					for _, a := range p.Adjustments {
						if a.RemovedAt == nil {
							if p, err = c.RemoveDeferralAdjustment(ctx, planID, a.ID, p.Version); err != nil {
								t.Fatal(err)
							}
						}
					}
					if _, err := c.UndoDeferralPlan(ctx, planID, p.Version); err != nil {
						t.Fatal(err)
					}
				},
				Config:             cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg("creditor_agreed", later, "", feeBlock("7.500", "EUR", false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan.hosting", "state", "active"),
					func(st *terraform.State) error {
						// A new plan on the same occurrence: Ledger kept the undone one as history.
						if now := st.RootModule().Resources["ledger_deferral_plan.hosting"].Primary.ID; now == planID {
							return fmt.Errorf("a new plan was expected, got the undone one again")
						}
						return capture(st)
					},
					resource.TestCheckResourceAttr("data.ledger_deferral_plan_history.hosting", "events.0.action", "created"),
				),
			},
		},
	})
	_, _, _ = occurrenceID, revisedMonth, later
}

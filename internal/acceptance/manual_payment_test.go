package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

func paymentConfig(occ, payments, override string) string {
	if payments == "" || override == "" {
		return payments + override
	}
	return fmt.Sprintf(`
%[2]s
%[3]s

data "ledger_occurrence_status_override" "current" {
  occurrence_id = %[1]q
  depends_on    = [ledger_manual_payment.part, ledger_occurrence_status_override.settle]
}

data "ledger_manual_payments" "all" {
  occurrence_id = %[1]q
  depends_on    = [ledger_manual_payment.part]
}
`, occ, payments, override)
}

func TestManualPaymentAndStatusOverride(t *testing.T) {
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A commitment whose occurrence is due next month: its due date is after the commitment was recorded (an earlier one is
	// "not recorded"), and a payment can still be dated today (paid_on must not be in the future). The commitment is a
	// fixture made through the client; the resources under test are the payment and the override.
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	one := int64(1)
	com, err := c.CreateCommitment(ctx, client.CommitmentInput{
		Name: "Rent", Kind: "service", Amount: client.Money{Amount: "100.00", Currency: "EUR"},
		Schedule: client.Schedule{Version: 1, Frequency: "monthly", Interval: &one, Day: 1}, StartDate: start.Format("2006-01-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	occID := firstOccurrence(t, s, start.Format("2006-01"), com.ID)
	paidOn := now.Format("2006-01-02")

	payments := func(amount, currency string) string {
		return fmt.Sprintf(`
resource "ledger_manual_payment" "part" {
  occurrence_id = %[1]q
  amount        = %[2]q
  currency      = %[3]q
  paid_on       = %[4]q
  note          = "bank transfer"
}`, occID, amount, currency, paidOn)
	}
	// Ordering matters for what the override reads back: derived_status is the value at the time it was written.
	afterPayment := "depends_on = [ledger_manual_payment.part]"
	override := func(status, reason string) string {
		return fmt.Sprintf(`
resource "ledger_occurrence_status_override" "settle" {
  occurrence_id = %[1]q
  status        = %[2]q
  reason        = %[3]q
  %[4]s
}`, occID, status, reason, afterPayment)
	}
	status := func() client.PaymentState {
		o, err := c.GetOccurrence(ctx, occID)
		if err != nil {
			t.Fatal(err)
		}
		return o.Payment
	}
	statusIs := func(want, derived string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if p := status(); p.Status != want || p.DerivedStatus != derived {
				return fmt.Errorf("payment status %q derived %q, want %q derived %q", p.Status, p.DerivedStatus, want, derived)
			}
			return nil
		}
	}
	noEvidence := func(*terraform.State) error {
		list, err := c.ListManualPayments(ctx, occID)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			return fmt.Errorf("manual payments left: %d", len(list))
		}
		if p := status(); p.Override != nil {
			return fmt.Errorf("an override is still active: %+v", p.Override)
		}
		return nil
	}
	imp := func(name string) resource.TestStep {
		return resource.TestStep{ResourceName: name, ImportState: true, ImportStateVerify: true}
	}
	impID := func(name string, f func(*terraform.State) (string, error)) resource.TestStep {
		return resource.TestStep{ResourceName: name, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: f}
	}
	paymentImportID := func(st *terraform.State) (string, error) {
		rs := st.RootModule().Resources["ledger_manual_payment.part"]
		return rs.Primary.Attributes["occurrence_id"] + "/" + rs.Primary.ID, nil
	}
	const settle = "ledger_occurrence_status_override.settle"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy:             noEvidence,
		Steps: []resource.TestStep{
			// Plan-time validation.
			{Config: paymentConfig(occID, payments("0.00", "EUR"), override("unpaid", "x")), PlanOnly: true, ExpectError: regexp.MustCompile("greater than zero")},
			{Config: paymentConfig(occID, payments("1.00", "EUR"), override("settled", "x")), PlanOnly: true, ExpectError: regexp.MustCompile("(?s)paid.*unpaid")},
			// A paid override needs evidence: Ledger refuses it, and nothing is left behind.
			{Config: paymentConfig(occID, "", strings.ReplaceAll(override("paid", "cash"), afterPayment, "")), ExpectError: regexp.MustCompile("payment_evidence_required")},
			// A payment in another currency is never converted: Ledger refuses it.
			{Config: paymentConfig(occID, payments("40.00", "USD"), ""), ExpectError: regexp.MustCompile("Recording the manual payment failed")},
			// A partial payment keeps its scale and leaves the occurrence partially paid; the override says unpaid.
			{
				Config: paymentConfig(occID, payments("40.500", "EUR"), override("unpaid", "disputed with landlord")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "amount", "40.500"),
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "currency", "EUR"),
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "note", "bank transfer"),
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "version", "1"),
					resource.TestCheckNoResourceAttr("ledger_manual_payment.part", "replaced_by_match_id"),
					resource.TestCheckResourceAttr(settle, "status", "unpaid"),
					resource.TestCheckResourceAttr(settle, "applied", "true"),
					resource.TestCheckResourceAttr(settle, "derived_status", "partially_paid"),
					// An unpaid override says the evidence does not settle it: the status follows the dates (due next month).
					resource.TestCheckResourceAttr(settle, "payment_status", "upcoming"),
					resource.TestCheckResourceAttr("data.ledger_manual_payments.all", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_manual_payments.all", "items.0.amount", "40.500"),
					resource.TestCheckResourceAttr("data.ledger_occurrence_status_override.current", "active", "true"),
					resource.TestCheckResourceAttr("data.ledger_occurrence_status_override.current", "reason", "disputed with landlord"),
					statusIs("upcoming", "partially_paid"),
				),
			},
			{Config: paymentConfig(occID, payments("40.500", "EUR"), override("unpaid", "disputed with landlord")), PlanOnly: true},
			impID("ledger_manual_payment.part", paymentImportID),
			imp(settle),
			// The override changes in place (a second history step); the payment is untouched.
			{
				Config: paymentConfig(occID, payments("40.500", "EUR"), override("paid", "landlord accepted the part payment")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(settle, "status", "paid"),
					resource.TestCheckResourceAttr(settle, "payment_status", "paid"),
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "version", "1"),
					statusIs("paid", "partially_paid"),
				),
			},
			// A payment has no update: changing the amount replaces the record, which now covers the occurrence.
			{
				Config: paymentConfig(occID, payments("100.00", "EUR"), override("paid", "landlord accepted the part payment")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_manual_payment.part", "amount", "100.00"),
					statusIs("paid", "paid"),
				),
			},
			// Drift: a payment deleted outside Terraform is planned for recording again.
			{
				PreConfig: func() {
					list, err := c.ListManualPayments(ctx, occID)
					if err != nil || len(list) != 1 {
						t.Fatalf("listing: %v (%d)", err, len(list))
					}
					if err := c.DeleteManualPayment(ctx, occID, list[0].ID, list[0].Version); err != nil {
						t.Fatal(err)
					}
				},
				Config:             paymentConfig(occID, payments("100.00", "EUR"), override("paid", "landlord accepted the part payment")),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: paymentConfig(occID, payments("100.00", "EUR"), override("paid", "landlord accepted the part payment")), Check: statusIs("paid", "paid")},
			// Drift: an override changed outside Terraform shows in the plan.
			{
				PreConfig: func() {
					p := status()
					o, _ := c.GetOccurrence(ctx, occID)
					if p.Override == nil {
						t.Fatal("expected an override")
					}
					if _, err := c.SetStatusOverride(ctx, occID, o.Version, "unpaid", "changed elsewhere"); err != nil {
						t.Fatal(err)
					}
				},
				Config:             paymentConfig(occID, payments("100.00", "EUR"), override("paid", "landlord accepted the part payment")),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})

	afterPayment = ""
	// Terraform refuses to take over an override it did not create; destroy undoes the whole history.
	o, err := c.GetOccurrence(ctx, occID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Payment.Override != nil {
		t.Fatalf("override left after destroy: %+v", o.Payment.Override)
	}
	if _, err := c.SetStatusOverride(ctx, occID, o.Version, "unpaid", "set by hand"); err != nil {
		t.Fatal(err)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: override("unpaid", "taken over"), ExpectError: regexp.MustCompile("already has a status override")},
			{Config: override("unpaid", "set by hand"), ResourceName: settle, ImportState: true, ImportStatePersist: true, ImportStateId: occID},
			{Config: override("unpaid", "set by hand"), PlanOnly: true},
		},
	})
}

// firstOccurrence lists the occurrences of a month with the raw API (the provider has no occurrence list; the identifier
// of a derived occurrence comes from the occurrences API).
func firstOccurrence(t *testing.T, s *ledgertest.Server, month, commitmentID string) string {
	t.Helper()
	code, body := get(t, s, "/api/v1/occurrences?month="+month+"&commitment_id="+commitmentID, s.Token.Reveal())
	if code != http.StatusOK {
		t.Fatalf("listing occurrences: HTTP %d: %s", code, body)
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("occurrences: %v %s", err, body)
	}
	return page.Items[0].ID
}

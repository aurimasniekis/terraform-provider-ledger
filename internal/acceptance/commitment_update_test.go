package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

// commitmentVariant is one commitment whose optional members and schedule form change between steps.
func commitmentVariant(optional, schedule, leeway string) string {
	return fmt.Sprintf(`
resource "ledger_commitment" "flex" {
  name       = "Flex"
  kind       = "service"
  amount     = "100.00"
  currency   = "EUR"
  start_date = "2024-02-29"
  schedule   = %s
  %s
  %s
}
`, schedule, optional, leeway)
}

// rawJSON sends one request with the root token (the client package has no payment calls; payments are user data
// the test records the way a person would) and returns the status and body.
func rawJSON(t *testing.T, s *ledgertest.Server, method, path, ifMatch string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.URL+"/api/v1"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token.Reveal())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// TestCommitmentClearingSwitchingAndDestroyGuard covers what the main commitment test does not: removing optional
// members (an explicit null on PATCH), annual to monthly, a leeway kind switch, a day the month never has refused at
// plan time, and a destroy refused because the commitment has payment evidence.
func TestCommitmentClearingSwitchingAndDestroyGuard(t *testing.T) {
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())

	full := `
  end_date        = "2030-12-31"
  notes           = "kept until removed"
  priority        = "important"
  match_tolerance = "1.50"`
	annualFeb29 := `{ frequency = "annual", month = 2, day = 29 }`
	monthlyEvery2 := `{ frequency = "monthly", interval = 2, day = 31 }`
	period := `leeway = { kind = "period", days = 3 }`
	deadline := `leeway = { kind = "deadline", until = "2031-01-31" }`

	var occurrenceID, paymentID string
	var paymentVersion int64

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			// Annual day 30 in February can never be due: refused at plan time, not at apply.
			{Config: commitmentVariant("", `{ frequency = "annual", month = 2, day = 30 }`, ""), PlanOnly: true, ExpectError: regexp.MustCompile("Day does not exist in that month")},
			{
				Config: commitmentVariant(full, annualFeb29, period),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_commitment.flex", "end_date", "2030-12-31"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "notes", "kept until removed"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "priority", "important"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "match_tolerance", "1.50"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "schedule.frequency", "annual"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "leeway.kind", "period"),
				),
			},
			// Removing end_date, notes, priority and match_tolerance clears them; annual becomes monthly; the leeway
			// kind switches from period to deadline. One update, then nothing left to change.
			{
				Config: commitmentVariant("", monthlyEvery2, deadline),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "end_date"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "notes"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "priority"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "match_tolerance"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "schedule.frequency", "monthly"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "schedule.interval", "2"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "schedule.day", "31"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "schedule.month"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "leeway.kind", "deadline"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "leeway.until", "2031-01-31"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "leeway.days"),
				),
			},
			{Config: commitmentVariant("", monthlyEvery2, deadline), PlanOnly: true},
			// The leeway kind switches back, the other way round.
			{
				Config: commitmentVariant("", monthlyEvery2, period),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_commitment.flex", "leeway.kind", "period"),
					resource.TestCheckResourceAttr("ledger_commitment.flex", "leeway.days", "3"),
					resource.TestCheckNoResourceAttr("ledger_commitment.flex", "leeway.until"),
				),
			},
			// A manual payment is payment evidence: record one on the first occurrence, outside Terraform.
			{
				PreConfig: func() {
					code, body := rawJSON(t, s, http.MethodGet, "/occurrences?month=2024-02&commitment_id="+commitmentID(t, s), "", nil)
					if code != http.StatusOK {
						t.Fatalf("listing occurrences: HTTP %d", code)
					}
					var list struct {
						Items []struct {
							ID string `json:"id"`
						} `json:"items"`
					}
					if err := json.Unmarshal(body, &list); err != nil || len(list.Items) == 0 {
						t.Fatalf("no occurrence in the start month (%v): %s", err, body)
					}
					occurrenceID = list.Items[0].ID
					code, body = rawJSON(t, s, http.MethodPost, "/occurrences/"+occurrenceID+"/manual-payments", "",
						map[string]any{"amount": map[string]string{"amount": "100.00", "currency": "EUR"}, "paid_on": "2024-02-29"})
					if code != http.StatusCreated {
						t.Fatalf("recording the payment: HTTP %d: %s", code, body)
					}
					var p struct {
						ID      string `json:"id"`
						Version int64  `json:"version"`
					}
					if err := json.Unmarshal(body, &p); err != nil {
						t.Fatal(err)
					}
					paymentID, paymentVersion = p.ID, p.Version
				},
				Config:   commitmentVariant("", monthlyEvery2, period),
				PlanOnly: true,
			},
			// Destroy is refused by Ledger (409) and the provider says what to do; the commitment stays.
			{
				Config:      commitmentVariant("", monthlyEvery2, period),
				Destroy:     true,
				ExpectError: regexp.MustCompile("payment history"),
			},
			// Removing the evidence lets the final destroy at the end of the test succeed.
			{
				PreConfig: func() {
					code, body := rawJSON(t, s, http.MethodDelete, "/occurrences/"+occurrenceID+"/manual-payments/"+paymentID, fmt.Sprintf("\"%d\"", paymentVersion), nil)
					if code != http.StatusNoContent && code != http.StatusOK {
						t.Fatalf("deleting the payment: HTTP %d: %s", code, body)
					}
				},
				Config: commitmentVariant("", monthlyEvery2, period),
				Check:  resource.TestCheckResourceAttr("ledger_commitment.flex", "name", "Flex"),
			},
		},
	})
}

// commitmentID finds the one commitment the test manages through the API.
func commitmentID(t *testing.T, s *ledgertest.Server) string {
	t.Helper()
	code, body := rawJSON(t, s, http.MethodGet, "/commitments?kind=service", "", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); code != http.StatusOK || err != nil || len(list.Items) != 1 {
		t.Fatalf("listing commitments: HTTP %d (%v): %s", code, err, body)
	}
	return list.Items[0].ID
}

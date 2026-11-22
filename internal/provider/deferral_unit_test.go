package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

const fakeOccurrenceID = "01JD3Y4C8V9M2Q7W5R6T0XZ900"

// fakeDeferrals is an in-memory stand-in for the deferral-plan endpoints. It enforces If-Match (409 version_conflict on a
// stale version), bumps the plan's version on every write including adjustment writes, refuses an undo while an
// adjustment is planned and refuses writes to an undone plan, as Ledger does (go-ledger ADR-0050). It records the order
// of the calls so a test can check that adjustments are removed before the plan is undone.
type fakeDeferrals struct {
	mu    sync.Mutex
	plan  *client.DeferralPlan
	next  int
	calls []string
}

func (f *fakeDeferrals) id() string {
	f.next++
	return fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
}

func (f *fakeDeferrals) totals() {
	planned, all := 0, 0
	for _, a := range f.plan.Adjustments {
		if a.RemovedAt != nil {
			continue
		}
		cents, _ := strconv.Atoi(strings.Replace(a.Amount.Amount, ".", "", 1))
		all += cents
		if a.PaidAt == nil {
			planned += cents
		}
	}
	f.plan.AdjustmentTotal = client.Money{Amount: fmt.Sprintf("%d.%02d", all/100, all%100), Currency: "EUR"}
	f.plan.UnpaidAdjustmentTotal = client.Money{Amount: fmt.Sprintf("%d.%02d", planned/100, planned%100), Currency: "EUR"}
}

func (f *fakeDeferrals) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	send := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(status int, code string) {
		send(status, map[string]any{"code": code, "message": code + " by the fake", "request_id": "R"})
	}
	if r.Header.Get("Authorization") != "Bearer unit-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if parts[0] != "deferral-plans" {
		fail(404, "not_found")
		return
	}
	if len(parts) >= 2 {
		f.calls = append(f.calls, r.Method+" "+strings.Join(parts[2:], "/"))
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	now := "2026-03-02T10:00:00Z"
	if len(parts) == 1 && r.Method == http.MethodPost {
		if f.plan != nil && f.plan.State == "active" {
			fail(409, "deferral_plan_exists")
			return
		}
		reason, _ := body["reason"].(string)
		var rp *string
		if reason != "" {
			rp = &reason
		}
		d := body["planned_date"].(string)
		f.plan = &client.DeferralPlan{
			ID: f.id(), OccurrenceID: body["occurrence_id"].(string), CommitmentID: "01JD3Y4C8V9M2Q7W5R6T0XZ901", PlanType: body["plan_type"].(string),
			State: "active", PlannedDate: d, PlannedMonth: d[:7], Reason: rp, OriginalDueDate: "2026-03-01",
			OriginalAmount: client.Money{Amount: "850.00", Currency: "EUR"}, OutstandingWhenPlanned: client.Money{Amount: "850.00", Currency: "EUR"},
			OccurrenceStatus: "overdue", OutstandingAmount: client.Money{Amount: "850.00", Currency: "EUR"},
			Adjustments: []client.DeferralAdjustment{}, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		f.totals()
		send(201, f.plan)
		return
	}
	if f.plan == nil || len(parts) < 2 || parts[1] != f.plan.ID {
		fail(404, "not_found")
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 {
		send(200, f.plan)
		return
	}
	if v := strings.Trim(r.Header.Get("If-Match"), `"`); v != strconv.FormatInt(f.plan.Version, 10) {
		send(409, map[string]any{"code": "version_conflict", "message": "stale", "request_id": "R", "reconciliation": map[string]any{"current_version": f.plan.Version}})
		return
	}
	if f.plan.State == "undone" {
		fail(409, client.CodeDeferralPlanUndone)
		return
	}
	bump := func() {
		f.plan.Version++
		f.plan.UpdatedAt = now
		f.totals()
		send(200, f.plan)
	}
	switch {
	case r.Method == http.MethodPatch && len(parts) == 2:
		f.plan.PlanType, f.plan.PlannedDate = body["plan_type"].(string), body["planned_date"].(string)
		f.plan.PlannedMonth = f.plan.PlannedDate[:7]
		f.plan.Reason = nil
		if s, ok := body["reason"].(string); ok {
			f.plan.Reason = &s
		}
		bump()
	case len(parts) == 3 && parts[2] == "undo":
		if f.plan.UnpaidAdjustmentTotal.Amount != "0.00" {
			fail(409, client.CodeDeferralAdjustmentsUnpaid)
			return
		}
		f.plan.State, f.plan.UndoneAt = "undone", &now
		bump()
	case len(parts) == 3 && parts[2] == "adjustments" && r.Method == http.MethodPost:
		amt := body["amount"].(map[string]any)
		if amt["currency"] != "EUR" {
			fail(400, "validation_failed")
			return
		}
		var note *string
		if s, ok := body["note"].(string); ok {
			note = &s
		}
		f.plan.Adjustments = append(f.plan.Adjustments, client.DeferralAdjustment{
			ID: f.id(), Kind: body["kind"].(string), Amount: client.Money{Amount: amt["amount"].(string), Currency: "EUR"}, Note: note, CreatedAt: now,
		})
		bump()
	case len(parts) >= 4 && parts[2] == "adjustments":
		var adj *client.DeferralAdjustment
		for i := range f.plan.Adjustments {
			if f.plan.Adjustments[i].ID == parts[3] {
				adj = &f.plan.Adjustments[i]
			}
		}
		if adj == nil {
			fail(404, "not_found")
			return
		}
		via := "token"
		switch {
		case r.Method == http.MethodDelete:
			if adj.RemovedAt != nil {
				fail(409, client.CodeDeferralAdjustmentRemoved)
				return
			}
			adj.RemovedAt = &now
		case len(parts) == 5 && parts[4] == "paid":
			adj.PaidAt, adj.PaidBy = &now, &client.Actor{Kind: "token", Via: &via}
		case len(parts) == 5 && parts[4] == "unpaid":
			adj.PaidAt, adj.PaidBy = nil, nil
		}
		bump()
	default:
		fail(404, "not_found")
	}
}

func newFakeDeferrals(t *testing.T) (*fakeDeferrals, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	requireTerraform(t)
	f := &fakeDeferrals{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return f, map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func planCfg(planType, date, reason, adjustments string) string {
	if reason != "" {
		reason = fmt.Sprintf("reason = %q", reason)
	}
	return fmt.Sprintf(`
resource "ledger_deferral_plan" "p" {
  occurrence_id = %q
  plan_type     = %q
  planned_date  = %q
  %s
}
%s`, fakeOccurrenceID, planType, date, reason, adjustments)
}

const feeCfg = `
resource "ledger_deferral_plan_adjustment" "fee" {
  deferral_plan_id = ledger_deferral_plan.p.id
  kind             = "fee"
  amount           = "5.00"
  currency         = "EUR"
  note             = "late fee"
  paid             = %t
}
`

func TestDeferralPlanLifecycleAgainstFakeLedger(t *testing.T) {
	f, factories := newFakeDeferrals(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: planCfg("personal", "2026-03-20", "waiting for salary", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "state", "active"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "planned_month", "2026-03"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "original_amount", "850.00"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "currency", "EUR"),
				),
			},
			// Revised in place: same plan, new type, date and no reason.
			{
				Config: planCfg("creditor_agreed", "2026-04-02", "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "plan_type", "creditor_agreed"),
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "planned_month", "2026-04"),
					resource.TestCheckNoResourceAttr("ledger_deferral_plan.p", "reason"),
				),
			},
			{ResourceName: "ledger_deferral_plan.p", ImportState: true, ImportStateVerify: true},
			// A fee, added and then marked paid in place; the plan's version moves with it without a plan diff.
			{
				Config: planCfg("creditor_agreed", "2026-04-02", "", fmt.Sprintf(feeCfg, false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "paid", "false"),
					resource.TestCheckNoResourceAttr("ledger_deferral_plan_adjustment.fee", "paid_at"),
					func(*terraform.State) error {
						if got := f.plan.UnpaidAdjustmentTotal.Amount; got != "5.00" {
							return fmt.Errorf("unpaid adjustments = %s, want 5.00", got)
						}
						return nil
					},
				),
			},
			{
				Config: planCfg("creditor_agreed", "2026-04-02", "", fmt.Sprintf(feeCfg, true)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "paid", "true"),
					resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "paid_via", "token"),
					resource.TestCheckResourceAttrSet("ledger_deferral_plan_adjustment.fee", "paid_at"),
				),
			},
			{ResourceName: "ledger_deferral_plan_adjustment.fee", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["ledger_deferral_plan.p"].Primary.ID + "/" + s.RootModule().Resources["ledger_deferral_plan_adjustment.fee"].Primary.ID, nil
				}},
			// Changing the amount replaces the adjustment (the old one is removed, history kept).
			{
				Config: planCfg("creditor_agreed", "2026-04-02", "", strings.Replace(fmt.Sprintf(feeCfg, false), "5.00", "7.50", 1)),
				Check:  resource.TestCheckResourceAttr("ledger_deferral_plan_adjustment.fee", "amount", "7.50"),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.plan.State != "undone" {
				return fmt.Errorf("destroy must undo the plan, state = %s", f.plan.State)
			}
			// The adjustments were removed first: Ledger refuses an undo while one is planned.
			undo := -1
			for i, c := range f.calls {
				if strings.HasSuffix(c, "undo") {
					undo = i
				}
			}
			prev := ""
			for i := undo - 1; i >= 0 && prev == ""; i-- {
				if !strings.HasPrefix(f.calls[i], "GET") {
					prev = f.calls[i]
				}
			}
			if undo < 0 || !strings.HasPrefix(prev, "DELETE adjustments/") {
				return fmt.Errorf("the undo must follow the removal of the adjustment, calls: %v", f.calls)
			}
			for _, a := range f.plan.Adjustments {
				if a.RemovedAt == nil {
					return fmt.Errorf("adjustment %s was not removed", a.ID)
				}
			}
			return nil
		},
	})
}

// A plan undone by a person leaves state on refresh, so the next plan creates it again.
func TestDeferralPlanUndoneOutsideTerraformIsRecreated(t *testing.T) {
	f, factories := newFakeDeferrals(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: planCfg("personal", "2026-03-20", "", "")},
			{
				PreConfig: func() { f.mu.Lock(); f.plan.State = "undone"; f.mu.Unlock() },
				Config:    planCfg("personal", "2026-03-20", "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_deferral_plan.p", "state", "active"),
					func(*terraform.State) error {
						if f.plan.State != "active" || f.plan.Version != 1 {
							return fmt.Errorf("a new plan was expected: %+v", f.plan)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestDeferralHint(t *testing.T) {
	for code, want := range map[string]string{
		"deferral_adjustments_unpaid": "mark it paid",
		"deferral_plan_exists":        "Import it",
		"nothing_outstanding":         "still owes something",
		"occurrence_cancelled":        "ledger_occurrence",
		"deferral_plan_undone":        "undone outside Terraform",
	} {
		got := deferralHint(&client.APIError{Status: 409, Code: code, Message: "m"})
		if !strings.Contains(got, want) {
			t.Errorf("%s: %q does not contain %q", code, got, want)
		}
	}
	if got := deferralHint(&client.APIError{Status: 400, Code: "deferral_plan_exists"}); strings.Contains(got, "Import it") {
		t.Errorf("a hint belongs to a 409 only: %q", got)
	}
}

func TestOccurrenceLookupValidation(t *testing.T) {
	_, factories := newFakeDeferrals(t)
	id := "01JD3Y4C8V9M2Q7W5R6T0XZ900"
	for name, tc := range map[string]struct{ cfg, err string }{
		"neither":    {`data "ledger_occurrence" "o" {}`, "Missing lookup"},
		"only month": {`data "ledger_occurrence" "o" { month = "2026-03" }`, "Missing lookup"},
		"both": {fmt.Sprintf(`data "ledger_occurrence" "o" {
  id = %q
  month = "2026-03"
}`, id), "Conflicting lookup"},
		"bad month": {fmt.Sprintf(`data "ledger_occurrence" "o" {
  commitment_id = %q
  month = "2026-13"
}`, id), "YYYY-MM"},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.err)}},
			})
		})
	}
}

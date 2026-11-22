package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func planJSON(version int64) map[string]any {
	return map[string]any{
		"id": idA, "occurrence_id": idB, "commitment_id": idA, "plan_type": "personal", "state": "active",
		"planned_date": "2026-03-20", "planned_month": "2026-03", "reason": nil, "original_due_date": "2026-03-01",
		"original_amount":          map[string]any{"amount": "850.00", "currency": "EUR"},
		"outstanding_when_planned": map[string]any{"amount": "850.00", "currency": "EUR"},
		"occurrence_status":        "overdue", "outstanding_amount": map[string]any{"amount": "850.00", "currency": "EUR"},
		"overdue_since": "2026-03-01", "grace_until": nil, "within_grace": false, "missed": false, "missed_reason": nil,
		"adjustment_total":        map[string]any{"amount": "0", "currency": "EUR"},
		"unpaid_adjustment_total": map[string]any{"amount": "0", "currency": "EUR"},
		"adjustments":             []any{}, "version": version, "created_at": "2026-03-02T00:00:00Z", "updated_at": "2026-03-02T00:00:00Z", "undone_at": nil,
	}
}

func TestDeferralPlanRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" if-match="+r.Header.Get("If-Match")+" body="+strings.TrimSpace(string(raw)))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/deferral-plans":
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, 200, map[string]any{"items": []any{planJSON(1)}, "next_cursor": "c1"})
				return
			}
			writeJSON(w, 200, map[string]any{"items": []any{planJSON(2)}, "next_cursor": nil})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/history"):
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"action": "created", "plan_version": 1, "plan_type": "personal",
				"planned_date": "2026-03-20", "reason": nil, "adjustment_id": nil, "by": map[string]any{"kind": "user", "user_id": nil, "via": "token"}, "at": "2026-03-02T00:00:00Z"}}, "next_cursor": nil})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/deferral-plans":
			writeJSON(w, 201, planJSON(1))
		default:
			writeJSON(w, 200, planJSON(3))
		}
	})
	ctx := context.Background()
	reason := "waiting for salary"
	if _, err := c.CreateDeferralPlan(ctx, DeferralPlanInput{OccurrenceID: idB, PlanType: "personal", PlannedDate: "2026-03-20"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReviseDeferralPlan(ctx, idA, 1, DeferralPlanInput{PlanType: "creditor_agreed", PlannedDate: "2026-03-25", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UndoDeferralPlan(ctx, idA, 2); err != nil {
		t.Fatal(err)
	}
	plans, err := c.ListDeferralPlans(ctx, DeferralPlanFilter{State: "active", PlannedMonth: "2026-03"})
	if err != nil || len(plans) != 2 || plans[0].OriginalAmount.Amount != "850.00" || plans[1].Version != 2 {
		t.Fatalf("list: %+v %v", plans, err)
	}
	if ev, err := c.ListDeferralPlanHistory(ctx, idA); err != nil || len(ev) != 1 || ev[0].By.Via == nil || *ev[0].By.Via != "token" {
		t.Fatalf("history: %+v %v", ev, err)
	}
	want := []string{
		`POST /api/v1/deferral-plans if-match= body={"occurrence_id":"` + idB + `","plan_type":"personal","planned_date":"2026-03-20","reason":null}`,
		`PATCH /api/v1/deferral-plans/` + idA + ` if-match="1" body={"plan_type":"creditor_agreed","planned_date":"2026-03-25","reason":"waiting for salary"}`,
		`POST /api/v1/deferral-plans/` + idA + `/undo if-match="2" body=`,
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("request %d:\n got %s\nwant %s", i, seen[i], w)
		}
	}
	if !strings.Contains(seen[3], "planned_month=2026-03") || !strings.Contains(seen[3], "state=active") {
		t.Errorf("filters not sent: %s", seen[3])
	}
	if !strings.Contains(seen[4], "cursor=c1") {
		t.Errorf("second page not requested: %s", seen[4])
	}
}

func TestDeferralAdjustmentRoutesKeepAmountExact(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" if-match="+r.Header.Get("If-Match")+" body="+strings.TrimSpace(string(raw)))
		writeJSON(w, 200, planJSON(4))
	})
	ctx := context.Background()
	note := "late fee"
	if _, err := c.AddDeferralAdjustment(ctx, idA, 3, DeferralAdjustmentInput{Kind: "fee", Amount: Money{Amount: "5.00", Currency: "EUR"}, Note: &note}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MarkDeferralAdjustmentPaid(ctx, idA, idB, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UnmarkDeferralAdjustmentPaid(ctx, idA, idB, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveDeferralAdjustment(ctx, idA, idB, 6); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /api/v1/deferral-plans/` + idA + `/adjustments if-match="3" body={"amount":{"amount":"5.00","currency":"EUR"},"kind":"fee","note":"late fee"}`,
		`POST /api/v1/deferral-plans/` + idA + `/adjustments/` + idB + `/paid if-match="4" body=`,
		`POST /api/v1/deferral-plans/` + idA + `/adjustments/` + idB + `/unpaid if-match="5" body=`,
		`DELETE /api/v1/deferral-plans/` + idA + `/adjustments/` + idB + ` if-match="6" body=`,
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("request %d:\n got %s\nwant %s", i, seen[i], w)
		}
	}
	if _, err := c.MarkDeferralAdjustmentPaid(ctx, idA, "../x", 1); err == nil {
		t.Error("a malformed adjustment id must be refused before any request")
	}
}

func TestOccurrenceLookup(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		occ := map[string]any{
			"id": idB, "commitment_id": idA, "month": "2026-03", "due_date": "2026-03-01", "amount": map[string]any{"amount": "850.00", "currency": "EUR"},
			"version": 2, "cancelled_at": nil, "cancelled_reason": nil,
			"payment": map[string]any{"status": "overdue", "timing": "overdue", "paid_amount": map[string]any{"amount": "0", "currency": "EUR"},
				"outstanding_amount": map[string]any{"amount": "850.00", "currency": "EUR"}, "unconverted_currencies": []string{}, "grace_until": nil, "within_grace": false},
		}
		if r.URL.Path == "/api/v1/occurrences" {
			writeJSON(w, 200, map[string]any{"items": []any{occ}, "next_cursor": nil})
			return
		}
		writeJSON(w, 200, occ)
	})
	ctx := context.Background()
	list, err := c.ListOccurrences(ctx, "2026-03", idA, true)
	if err != nil || len(list) != 1 || list[0].Payment.OutstandingAmount.Amount != "850.00" || list[0].Payment.Status != "overdue" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if o, err := c.GetOccurrence(ctx, idB); err != nil || o.ID != idB {
		t.Fatalf("get: %+v %v", o, err)
	}
	if !strings.Contains(seen[0], "month=2026-03") || !strings.Contains(seen[0], "include_cancelled=true") || !strings.Contains(seen[0], "commitment_id="+idA) {
		t.Errorf("query: %s", seen[0])
	}
}

package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func sampleInput() CommitmentInput {
	d := int64(15)
	one := int64(1)
	note := "Lease"
	tol := Money{Amount: "0.50", Currency: "EUR"}
	return CommitmentInput{
		Name: "Rent", Kind: "service", Amount: Money{Amount: "850.00", Currency: "EUR"},
		Schedule:  Schedule{Version: 1, Frequency: "monthly", Interval: &one, Day: d},
		StartDate: "2026-01-01", Notes: &note, MatchTolerance: &tol,
		Leeway: &Leeway{Kind: "period", Days: &d},
	}
}

func TestCreateCommitmentKeepsAmountsExactAndSendsNulls(t *testing.T) {
	var raw []byte
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/commitments" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw, _ = io.ReadAll(r.Body)
		writeJSON(w, 201, map[string]any{
			"id": idA, "name": "Rent", "kind": "service", "amount": map[string]any{"amount": "850.00", "currency": "EUR"},
			"schedule":   map[string]any{"version": 1, "frequency": "monthly", "interval": 1, "month": nil, "day": 15},
			"start_date": "2026-01-01", "end_date": nil, "priority": nil, "notes": "Lease",
			"match_tolerance": map[string]any{"amount": "0.50", "currency": "EUR"}, "leeway": nil,
			"version": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		})
	})
	got, err := c.CreateCommitment(context.Background(), sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount.Amount != "850.00" || got.MatchTolerance == nil || got.MatchTolerance.Amount != "0.50" || got.Schedule.Month != nil {
		t.Fatalf("got %+v", got)
	}
	body := string(raw)
	// Amounts travel as JSON strings with their scale, never as numbers.
	if !strings.Contains(body, `"amount":"850.00"`) || !strings.Contains(body, `"amount":"0.50"`) {
		t.Errorf("amounts not exact strings: %s", body)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"end_date", "priority"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("%s must be an explicit null, got %v (present=%v)", k, v, ok)
		}
	}
	sched := m["schedule"].(map[string]any)
	if _, ok := sched["month"]; ok {
		t.Errorf("a create must not send a member of the other schedule form (the API answers 400), got %v", sched)
	}
	lee := m["leeway"].(map[string]any)
	for _, k := range []string{"until", "conditions", "consequences", "source"} {
		if v, ok := lee[k]; !ok || v != nil {
			t.Errorf("leeway.%s must be an explicit null, got %v", k, lee)
		}
	}
}

func TestUpdateCommitmentSendsIfMatchMergePatchAndWholeForms(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/commitments/"+idA {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("If-Match") != `"4"` || r.Header.Get("Content-Type") != "application/merge-patch+json" {
			t.Errorf("headers: If-Match=%q Content-Type=%q", r.Header.Get("If-Match"), r.Header.Get("Content-Type"))
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		sched := m["schedule"].(map[string]any)
		if v, ok := sched["interval"]; !ok || v != nil || sched["month"].(float64) != 2 {
			t.Errorf("switching to annual must send interval: null and month: %v", sched)
		}
		if v, ok := m["leeway"]; !ok || v != nil {
			t.Errorf("clearing leeway must send null, got %v (present=%v)", v, ok)
		}
		writeJSON(w, 200, Commitment{ID: idA, Version: 5})
	})
	in := sampleInput()
	in.Leeway = nil
	two := int64(2)
	in.Schedule = Schedule{Version: 1, Frequency: "annual", Month: &two, Day: 29}
	got, err := c.UpdateCommitment(context.Background(), idA, 4, in)
	if err != nil || got.Version != 5 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestListCommitmentsFiltersAndPages(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("kind") != "domain" || q.Get("currency") != "EUR" || q.Get("priority") != "" {
			t.Errorf("query = %v", q)
		}
		if calls == 1 {
			writeJSON(w, 200, map[string]any{"items": []Commitment{{ID: idA}}, "next_cursor": "c1"})
			return
		}
		if q.Get("cursor") != "c1" {
			t.Errorf("cursor = %q", q.Get("cursor"))
		}
		writeJSON(w, 200, map[string]any{"items": []Commitment{{ID: idB}}, "next_cursor": nil})
	})
	got, err := c.ListCommitments(context.Background(), CommitmentFilter{Kinds: []string{"domain"}, Currencies: []string{"EUR"}})
	if err != nil || len(got) != 2 || got[1].ID != idB {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestDeleteAndGetCommitmentValidateIDAndMapErrors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != `"2"` {
			t.Errorf("If-Match = %q", r.Header.Get("If-Match"))
		}
		writeJSON(w, 409, map[string]any{"code": "conflict", "message": "has payments", "request_id": "R"})
	})
	err := c.DeleteCommitment(context.Background(), idA, 2)
	var ae *APIError
	if err == nil || !HasCode(err, CodeConflict) || IsVersionConflict(err) {
		t.Fatalf("err = %v (a plain conflict is not a version conflict)", err)
	}
	_ = ae
	if err := c.DeleteCommitment(context.Background(), "../x", 1); err == nil {
		t.Error("an invalid identifier must not reach the URL")
	}
	if _, err := c.GetCommitment(context.Background(), "nope"); err == nil {
		t.Error("an invalid identifier must not reach the URL")
	}
}

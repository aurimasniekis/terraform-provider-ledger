package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRecordManualPaymentSendsExactMoneyAndOmitsAbsentNote(t *testing.T) {
	var raw string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/occurrences/"+idA+"/manual-payments" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("If-Match") != "" {
			t.Error("a create needs no version")
		}
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		writeJSON(w, 201, map[string]any{
			"id": idB, "occurrence_id": idA, "amount": map[string]any{"amount": "40.50", "currency": "EUR"}, "paid_on": "2026-03-01",
			"note": nil, "version": 1, "created_at": "2026-03-01T00:00:00Z", "updated_at": "2026-03-01T00:00:00Z", "replaced_by_match_id": nil,
		})
	})
	got, err := c.RecordManualPayment(context.Background(), idA, ManualPaymentInput{Amount: Money{Amount: "40.50", Currency: "EUR"}, PaidOn: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount.Amount != "40.50" || got.Note != nil || got.ReplacedByMatchID != nil {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(raw, `"amount":"40.50"`) || strings.Contains(raw, "note") {
		t.Errorf("body %s", raw)
	}
}

func TestManualPaymentDeleteAndListUseVersionAndPages(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			if r.URL.Path != "/api/v1/occurrences/"+idA+"/manual-payments/"+idB || r.Header.Get("If-Match") != `"3"` {
				t.Errorf("delete %s If-Match %q", r.URL.Path, r.Header.Get("If-Match"))
			}
			w.WriteHeader(204)
		case http.MethodGet:
			calls++
			if calls == 1 {
				writeJSON(w, 200, map[string]any{"items": []ManualPayment{{ID: idA}}, "next_cursor": "c1"})
				return
			}
			if r.URL.Query().Get("cursor") != "c1" {
				t.Errorf("cursor %q", r.URL.RawQuery)
			}
			writeJSON(w, 200, map[string]any{"items": []ManualPayment{{ID: idB}}, "next_cursor": nil})
		}
	})
	if err := c.DeleteManualPayment(context.Background(), idA, idB, 3); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListManualPayments(context.Background(), idA)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %d", err, len(list))
	}
}

func TestStatusOverrideSendsOccurrenceVersion(t *testing.T) {
	var methods []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.URL.Path != "/api/v1/occurrences/"+idA+"/status-override" || r.Header.Get("If-Match") != `"7"` {
			t.Errorf("%s %s If-Match %q", r.Method, r.URL.Path, r.Header.Get("If-Match"))
		}
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"status":"paid"`) || !strings.Contains(string(b), `"reason":"cash"`) {
				t.Errorf("body %s", b)
			}
		}
		writeJSON(w, 200, map[string]any{"id": idA, "version": 8, "payment": map[string]any{
			"status": "paid", "derived_status": "partially_paid",
			"override": map[string]any{"status": "paid", "reason": "cash", "set_at": "2026-03-01T00:00:00Z", "applied": true, "previous_status": nil},
		}})
	})
	occ, err := c.SetStatusOverride(context.Background(), idA, 7, "paid", "cash")
	if err != nil || occ.Payment.Override == nil || !occ.Payment.Override.Applied || occ.Payment.DerivedStatus != "partially_paid" {
		t.Fatalf("%v %+v", err, occ)
	}
	if _, err := c.UndoStatusOverride(context.Background(), idA, 7); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "PUT,DELETE" {
		t.Errorf("methods %v", methods)
	}
	if _, err := c.GetOccurrence(context.Background(), "bad"); err == nil {
		t.Error("an invalid identifier must be refused before any request")
	}
}

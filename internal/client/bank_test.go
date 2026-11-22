package client

import (
	"context"
	"net/http"
	"testing"
)

func TestBankRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.URL.Path == "/api/v1/bank-applications":
			writeJSON(w, 200, map[string]any{"items": []map[string]any{{"provider": "enable_banking", "name": "main", "mode": "app", "environment": "production",
				"account_access": "all", "capabilities": map[string]any{"balance_types": []string{"booked"}, "pending_transactions": true,
					"max_consent_validity_days": 90, "max_history_days": 0, "background_fetches_per_day": 4}}}})
		case r.URL.Path == "/api/v1/bank-applications/my%20app/institutions" || r.URL.Path == "/api/v1/bank-applications/my app/institutions":
			writeJSON(w, 200, map[string]any{"items": []map[string]any{{"id": "B1", "name": "Bank", "country": "LT", "max_consent_validity_days": 180}}})
		case r.URL.Path == "/api/v1/bank-connections" && r.URL.Query().Get("cursor") == "":
			writeJSON(w, 200, map[string]any{"items": []map[string]any{{"id": idA, "status": "active"}}, "next_cursor": "c1"})
		case r.URL.Path == "/api/v1/bank-connections":
			writeJSON(w, 200, map[string]any{"items": []map[string]any{{"id": idB, "status": "failed", "last_error": map[string]any{"kind": "retryable", "code": ""}}}, "next_cursor": nil})
		case r.URL.Path == "/api/v1/bank-connections/"+idA:
			writeJSON(w, 200, map[string]any{"id": idA, "status": "active", "accounts": []map[string]any{{"id": idB, "currency": "EUR",
				"identifier_hint": "1234", "balances": []map[string]any{{"type": "booked", "amount": map[string]any{"amount": "1.00", "currency": "EUR"}}}}}})
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	apps, err := c.ListBankApplications(ctx)
	if err != nil || len(apps) != 1 || apps[0].Capabilities.MaxConsentValidityDays != 90 || !apps[0].Capabilities.PendingTransactions {
		t.Fatalf("applications %+v %v", apps, err)
	}
	inst, err := c.ListBankInstitutions(ctx, "my app", "LT")
	if err != nil || len(inst) != 1 || inst[0].MaxConsentValidityDays != 180 {
		t.Fatalf("institutions %+v %v", inst, err)
	}
	all, err := c.ListBankConnections(ctx)
	if err != nil || len(all) != 2 || all[1].LastError == nil || all[1].LastError.Kind != "retryable" {
		t.Fatalf("connections %+v %v", all, err)
	}
	one, err := c.GetBankConnection(ctx, idA)
	if err != nil || len(one.Accounts) != 1 || one.Accounts[0].Currency != "EUR" {
		t.Fatalf("connection %+v %v", one, err)
	}
	if _, err := c.GetBankConnection(ctx, "nope"); err == nil {
		t.Error("an invalid identifier must be refused before any request")
	}
	for _, s := range seen {
		if s == "" {
			t.Error("empty request")
		}
	}
}

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const tokenSecretValue = "ldgr_" + "S3cretValueForTestsOnly0123456789abcdefghij"

func TestAPITokenRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" body="+strings.TrimSpace(string(raw)))
		switch {
		case r.Method == http.MethodPost:
			writeJSON(w, 201, map[string]any{"secret": tokenSecretValue, "token": APIToken{ID: idA, Name: "ci", Grants: []string{"commitments:read"}, Status: TokenActive}, "warning": nil})
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case r.URL.Path == "/api/v1/tokens":
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, 200, map[string]any{"items": []APIToken{{ID: idA}}, "next_cursor": "c1"})
				return
			}
			writeJSON(w, 200, map[string]any{"items": []APIToken{{ID: idB}}, "next_cursor": nil})
		default:
			writeJSON(w, 200, APIToken{ID: idA})
		}
	})
	ctx := context.Background()
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	created, err := c.CreateAPIToken(ctx, NewAPIToken{Name: "ci", Grants: []string{"commitments:read"}, ExpiresAt: &exp})
	if err != nil || created.Secret.Reveal() != tokenSecretValue || created.Token.ID != idA {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(fmtAll(created), tokenSecretValue) {
		t.Fatal("formatting a created token must not show the secret")
	}
	if _, err := c.CreateAPIToken(ctx, NewAPIToken{Name: "root", Root: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAPIToken(ctx, idA); err != nil {
		t.Fatal(err)
	}
	all, err := c.ListAPITokens(ctx, APITokenFilter{OwnerID: idB})
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %v %v", all, err)
	}
	if err := c.RevokeAPIToken(ctx, idA); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /api/v1/auth/tokens body={"expires_at":"2030-01-02T03:04:05Z","grants":["commitments:read"],"name":"ci"}`,
		`POST /api/v1/auth/tokens body={"name":"root","root":true}`,
		"GET /api/v1/tokens/" + idA + " body=",
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("request %d = %s, want %s", i, seen[i], w)
		}
	}
	if !strings.Contains(seen[3], "owner_id="+idB) || seen[5] != "DELETE /api/v1/tokens/"+idA+" body=" {
		t.Errorf("list/revoke = %s / %s", seen[3], seen[5])
	}
	if _, err := c.GetAPIToken(ctx, "nope"); err == nil {
		t.Error("an invalid identifier must be refused before any request")
	}
}

func TestCreateAPITokenErrorHasNoSecret(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, map[string]any{"code": "forbidden", "message": "needs the root token", "request_id": "R1"})
	})
	_, err := c.CreateAPIToken(context.Background(), NewAPIToken{Name: "x", Grants: []string{"a:b"}})
	if !HasCode(err, "forbidden") || strings.Contains(err.Error(), "ldgr_") {
		t.Fatalf("err = %v", err)
	}
}

func fmtAll(v any) string { return fmt.Sprintf("%v|%+v|%#v", v, v, v) }

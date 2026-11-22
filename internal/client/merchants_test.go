package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMerchantRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" if-match="+r.Header.Get("If-Match"))
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/merchants":
			if strings.TrimSpace(string(raw)) != `{"display_name":"Lidl"}` {
				t.Errorf("create body = %s", raw)
			}
			writeJSON(w, 201, Merchant{ID: idA, DisplayName: "Lidl", Version: 1})
		case r.Method == http.MethodPatch:
			if r.Header.Get("Content-Type") != "application/merge-patch+json" || strings.TrimSpace(string(raw)) != `{"display_name":"Lidl Vilnius"}` {
				t.Errorf("rename: %s %s", r.Header.Get("Content-Type"), raw)
			}
			writeJSON(w, 200, Merchant{ID: idA, DisplayName: "Lidl Vilnius", Version: 2})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/aliases/"):
			writeJSON(w, 200, Merchant{ID: idA, Version: 3})
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/merchants":
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, 200, map[string]any{"items": []Merchant{{ID: idA}}, "next_cursor": "c1"})
				return
			}
			writeJSON(w, 200, map[string]any{"items": []Merchant{{ID: idB}}, "next_cursor": nil})
		default:
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			writeJSON(w, 200, Merchant{ID: idA, Version: 3})
		}
	})
	ctx := context.Background()
	if m, err := c.CreateMerchant(ctx, "Lidl"); err != nil || m.ID != idA {
		t.Fatalf("create: %+v %v", m, err)
	}
	if _, err := c.RenameMerchant(ctx, idA, 1, "Lidl Vilnius"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ArchiveMerchant(ctx, idA, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UnarchiveMerchant(ctx, idA, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMerchantAlias(ctx, idA, "LIDL"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveMerchantAlias(ctx, idA, idB); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMerchant(ctx, idA, 4); err != nil {
		t.Fatal(err)
	}
	if all, err := c.ListMerchants(ctx, true); err != nil || len(all) != 2 {
		t.Fatalf("list: %v %v", all, err)
	}
	want := []string{
		"POST /api/v1/merchants if-match=",
		"PATCH /api/v1/merchants/" + idA + " if-match=\"1\"",
		"POST /api/v1/merchants/" + idA + "/archive if-match=\"2\"",
		"POST /api/v1/merchants/" + idA + "/unarchive if-match=\"3\"",
		"POST /api/v1/merchants/" + idA + "/aliases if-match=",
		"DELETE /api/v1/merchants/" + idA + "/aliases/" + idB + " if-match=",
		"DELETE /api/v1/merchants/" + idA + " if-match=\"4\"",
		"GET /api/v1/merchants?archived=true&limit=200 if-match=",
		"GET /api/v1/merchants?archived=true&cursor=c1&limit=200 if-match=",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

func TestMerchantListDefaultsToActive(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("archived") {
			t.Errorf("archived must not be sent when archived merchants are not wanted: %s", r.URL.RawQuery)
		}
		writeJSON(w, 200, map[string]any{"items": []Merchant{}, "next_cursor": nil})
	})
	if _, err := c.ListMerchants(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestMerchantErrorCodesAndIDChecks(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 409, map[string]any{"code": "merchant_in_use", "message": "in use", "request_id": "R"})
	})
	err := c.DeleteMerchant(context.Background(), idA, 1)
	if !HasCode(err, CodeMerchantInUse) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"", "../x", "lidl", strings.ToLower(idA)} {
		if _, err := c.GetMerchant(context.Background(), bad); err == nil {
			t.Errorf("GetMerchant(%q) accepted", bad)
		}
	}
}

func TestMerchantObjectRoute(t *testing.T) {
	var path string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		writeJSON(w, 200, ObjectAssignments{ObjectType: "merchant", ObjectID: idA})
	})
	if _, err := c.AddObjectLabel(context.Background(), ObjectMerchant, idA, idB); err != nil {
		t.Fatal(err)
	}
	if want := "PUT /api/v1/organisation/merchant/" + idA + "/labels/" + idB; path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	idA = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
	idB = "01JD3Y4C8V9M2Q7W5R6T0XZBNB"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, Token: Secret("test-token-value")})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestCreateDefinitionSendsBodyAndToken(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/categories" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token-value" {
			t.Error("bearer token missing")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "Rent" || body["parent_id"] != idA {
			t.Errorf("body = %v", body)
		}
		writeJSON(w, 201, Definition{ID: idB, Kind: "category", Name: "Rent", ParentID: &[]string{idA}[0], Version: 1})
	})
	parent := idA
	d, err := c.CreateDefinition(context.Background(), Categories, "Rent", &parent)
	if err != nil || d.ID != idB || d.Version != 1 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestUpdateSendsIfMatchAndMergePatchWithNull(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.Header.Get("If-Match") != `"3"` {
			t.Errorf("method %s if-match %q", r.Method, r.Header.Get("If-Match"))
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/merge-patch+json" {
			t.Errorf("content type %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"parent_id":null`) {
			t.Errorf("patch %s must carry an explicit null", raw)
		}
		writeJSON(w, 200, Definition{ID: idA, Version: 4})
	})
	d, err := c.UpdateDefinition(context.Background(), Categories, idA, 3, map[string]any{"parent_id": nil})
	if err != nil || d.Version != 4 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestListFollowsCursorsAndSendsFilters(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("archived") != "false" || q.Get("name[prefix]") != "ab" || q.Get("parent_id") != "none" {
			t.Errorf("query %v", q)
		}
		if calls == 1 {
			if q.Get("cursor") != "" {
				t.Error("first page must not carry a cursor")
			}
			next := "c1"
			writeJSON(w, 200, map[string]any{"items": []Definition{{ID: idA}}, "next_cursor": next})
			return
		}
		if q.Get("cursor") != "c1" {
			t.Errorf("cursor %q", q.Get("cursor"))
		}
		writeJSON(w, 200, map[string]any{"items": []Definition{{ID: idB}}, "next_cursor": nil})
	})
	f, none := false, "none"
	items, err := c.ListDefinitions(context.Background(), Categories, DefinitionFilter{Archived: &f, ParentID: &none, NamePrefix: "ab"})
	if err != nil || len(items) != 2 || calls != 2 {
		t.Fatalf("items=%v calls=%d err=%v", items, calls, err)
	}
}

func TestDeleteSendsReassignAndVersion(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Query().Get("reassign_to") != idB || r.Header.Get("If-Match") != `"2"` {
			t.Errorf("%s %s if-match %q", r.Method, r.URL.String(), r.Header.Get("If-Match"))
		}
		writeJSON(w, 200, map[string]int{"reassigned": 5})
	})
	n, err := c.DeleteDefinition(context.Background(), Labels, idA, 2, idB)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestErrorEnvelopeAndHelpers(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/labels/" + idA:
			writeJSON(w, 409, map[string]any{"code": "version_conflict", "message": "stale", "request_id": "R1", "reconciliation": map[string]any{"current_version": 7}})
		case "/api/v1/labels/" + idB:
			writeJSON(w, 404, map[string]any{"code": "not_found", "message": "no such label", "request_id": "R2"})
		default:
			w.WriteHeader(502)
			_, _ = w.Write([]byte("<html>bad gateway with token test-token-value</html>"))
		}
	})
	ctx := context.Background()
	_, err := c.GetDefinition(ctx, Labels, idA)
	if !IsVersionConflict(err) {
		t.Fatalf("want a version conflict, got %v", err)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.CurrentVersion != 7 || ae.RequestID != "R1" {
		t.Errorf("%+v", ae)
	}
	if _, err := c.GetDefinition(ctx, Labels, idB); !IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	_, err = c.GetDefinition(ctx, Labels, "01JD3Y4C8V9M2Q7W5R6T0XZBNC")
	if err == nil || strings.Contains(err.Error(), "test-token-value") || strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("a non-JSON error body must not be echoed: %v", err)
	}
}

func TestIdentifiersAreValidatedBeforeTheyReachAPath(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("no request expected") })
	ctx := context.Background()
	for _, bad := range []string{"", "../x", idA + "/members", strings.ToLower(idA), "merchant-key"} {
		if _, err := c.GetDefinition(ctx, Labels, bad); err == nil {
			t.Errorf("GetDefinition(%q) accepted", bad)
		}
		if _, err := c.GetObjectOrganisation(ctx, ObjectCommitment, bad); err == nil {
			t.Errorf("GetObjectOrganisation(%q) accepted", bad)
		}
		if err := c.AddBucketMember(ctx, idA, ObjectAccount, bad); err == nil {
			t.Errorf("AddBucketMember(%q) accepted", bad)
		}
	}
}

func TestAssignmentRoutes(t *testing.T) {
	var seen []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/category") {
			raw, _ := io.ReadAll(r.Body)
			if strings.TrimSpace(string(raw)) != `{"category_id":null}` {
				t.Errorf("none must be an explicit null, got %s", raw)
			}
		}
		switch {
		case strings.Contains(r.URL.Path, "/members/"):
			w.WriteHeader(204)
		case r.Method == http.MethodDelete:
			w.WriteHeader(200)
		default:
			writeJSON(w, 200, ObjectAssignments{ObjectType: "commitment", ObjectID: idA})
		}
	})
	ctx := context.Background()
	if _, err := c.SetObjectCategory(ctx, ObjectCommitment, idA, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.ResetObjectCategory(ctx, ObjectCommitment, idA); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddObjectLabel(ctx, ObjectTransaction, idA, idB); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveObjectLabel(ctx, ObjectTransaction, idA, idB); err != nil {
		t.Fatal(err)
	}
	if err := c.AddBucketMember(ctx, idB, ObjectAccount, idA); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveBucketMember(ctx, idB, ObjectAccount, idA); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PUT /api/v1/organisation/commitment/" + idA + "/category",
		"DELETE /api/v1/organisation/commitment/" + idA + "/category",
		"PUT /api/v1/organisation/transaction/" + idA + "/labels/" + idB,
		"DELETE /api/v1/organisation/transaction/" + idA + "/labels/" + idB,
		"PUT /api/v1/buckets/" + idB + "/members/account/" + idA,
		"DELETE /api/v1/buckets/" + idB + "/members/account/" + idA,
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func reportClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetDocumentKeepsNumbersAndAmountsExact(t *testing.T) {
	c := reportClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"n":12345678901234567890,"money":{"amount":"0.10","currency":"EUR"}}`))
	})
	d, err := c.GetDocument(context.Background(), "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(d["n"]) != "12345678901234567890" {
		t.Fatalf("number changed: %v", d["n"])
	}
	if d["money"].(map[string]any)["amount"] != "0.10" {
		t.Fatalf("amount changed: %v", d["money"])
	}
}

func TestListDocumentsPagesAndTruncates(t *testing.T) {
	var queries []url.Values
	c := reportClient(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"items":[1,2],"next_cursor":"c2"}`))
		case "c2":
			_, _ = w.Write([]byte(`{"items":[3],"next_cursor":null}`))
		}
	})
	items, trunc, err := c.ListDocuments(context.Background(), "/x", url.Values{"a": {"b"}}, 10)
	if err != nil || trunc || len(items) != 3 {
		t.Fatalf("items=%v trunc=%v err=%v", items, trunc, err)
	}
	if queries[0].Get("a") != "b" || queries[0].Get("limit") != "10" || queries[1].Get("cursor") != "c2" {
		t.Fatalf("queries = %v", queries)
	}
	items, trunc, err = c.ListDocuments(context.Background(), "/x", nil, 2)
	if err != nil || !trunc || len(items) != 2 {
		t.Fatalf("a full first page with a next cursor must be truncated: items=%v trunc=%v err=%v", items, trunc, err)
	}
}

func TestListDocumentsReportsAPIError(t *testing.T) {
	c := reportClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"forbidden","message":"no"}`))
	})
	if _, _, err := c.ListDocuments(context.Background(), "/x", nil, 5); !HasCode(err, "forbidden") {
		t.Fatalf("err = %v", err)
	}
}

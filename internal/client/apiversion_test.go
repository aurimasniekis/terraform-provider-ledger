package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJudgeAPIVersion(t *testing.T) {
	for _, v := range []string{"1.0.0", "1.0.7", "1.0.0-rc.1"} {
		got, err := JudgeAPIVersion(v)
		if err != nil || got.Warning != "" || got.Served != v {
			t.Errorf("%s: got %+v, %v; want accepted without a warning", v, got, err)
		}
	}
	if got, err := JudgeAPIVersion("1.3.0"); err != nil || got.Warning == "" {
		t.Errorf("1.3.0: want accepted with a warning, got %+v, %v", got, err)
	}
	for _, v := range []string{"2.0.0", "10.0.0", "0.9.0"} {
		var me *APIMajorError
		if _, err := JudgeAPIVersion(v); !errors.As(err, &me) || me.Served != v {
			t.Errorf("%s: want an *APIMajorError, got %v", v, err)
		}
	}
	for _, v := range []string{"", "latest", "1", "1.0", "v1.0.0", " 1.0.0", "99999999999999999999.0.0", strings.Repeat("1", 70) + ".0.0"} {
		if _, err := JudgeAPIVersion(v); err == nil || errors.As(err, new(*APIMajorError)) {
			t.Errorf("%q: want a plain no-usable-version error, got %v", v, err)
		}
	}
}

func TestCheckAPIVersionReadsThePrefixedDocument(t *testing.T) {
	const token = "api-version-test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The endpoint has a path prefix; the document is under it, at /api/v1/openapi.json.
		if r.URL.Path != "/ledger/api/v1/openapi.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"openapi":"3.1.1","info":{"version":"1.0.0"},"paths":{}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL + "/ledger/", Token: Secret(token)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.CheckAPIVersion(context.Background())
	if err != nil || got.Served != "1.0.0" || got.Warning != "" {
		t.Fatalf("got %+v, %v; want 1.0.0 without a warning", got, err)
	}
}

func TestCheckAPIVersionFailuresAreErrorsWithoutTheToken(t *testing.T) {
	const token = "api-version-failure-token"
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"not found", http.NotFound, "HTTP 404"},
		{"not JSON", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }, "not valid JSON"},
		{"no version", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"info":{}}`)) }, "no usable info.version"},
		{"major 2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"info":{"version":"2.0.0"}}`)) }, "major 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			c, err := New(Config{Endpoint: srv.URL, Token: Secret(token)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.CheckAPIVersion(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("the error leaks the token: %v", err)
			}
		})
	}

	// An unreachable server is an error too: the provider cannot verify it, so it must not use it.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	c, err := New(Config{Endpoint: url, Token: Secret(token)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CheckAPIVersion(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot reach the Ledger server") {
		t.Fatalf("an unreachable server was not reported: %v", err)
	}
}

package ledgertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func meServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyOurs(t *testing.T) {
	open := make(chan struct{})
	closed := make(chan struct{})
	close(closed)

	t.Run("accepts the bootstrap token", func(t *testing.T) {
		srv := meServer(t, "ours")
		if err := VerifyOurs(context.Background(), srv.URL, "ours", open); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects another server that does not know the token", func(t *testing.T) {
		srv := meServer(t, "theirs")
		err := VerifyOurs(context.Background(), srv.URL, "ours", open)
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("want a 401 error, got %v", err)
		}
	})
	t.Run("rejects a process that exited after answering", func(t *testing.T) {
		exiting := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(exiting)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		err := VerifyOurs(context.Background(), srv.URL, "ours", exiting)
		if err == nil || !strings.Contains(err.Error(), "after answering") {
			t.Fatalf("want an exited-after-answering error, got %v", err)
		}
	})
	t.Run("rejects an exited process even when something answers", func(t *testing.T) {
		srv := meServer(t, "ours")
		if err := VerifyOurs(context.Background(), srv.URL, "ours", closed); err == nil {
			t.Fatal("want an error for an exited process")
		}
	})
}

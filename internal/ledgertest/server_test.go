package ledgertest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// A process that exits while a request hangs must be reported as exited, not as a readiness timeout: startLoop retries
// on new ports only for the exit. The listener accepts the connection (the process "exits" then) and never answers.
func TestWaitForAPIReportsExitWhenRequestHangs(t *testing.T) {
	old := readyTimeout
	readyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readyTimeout = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	exited := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(exited)
		// Hold the connection without answering until the client gives up.
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}()

	_, err = waitForAPI("http://"+ln.Addr().String(), exited)
	if err == nil || !strings.Contains(err.Error(), "the server exited") {
		t.Fatalf("want a server-exited error, got %v", err)
	}
	if errors.Is(err, errNotReady) {
		t.Fatalf("an exited server must not be reported as not ready: %v", err)
	}
}

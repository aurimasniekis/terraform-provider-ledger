package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func HasStatus(err error, code int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == code
}

func retryClient(t *testing.T, h http.Handler) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return c, &waits
}

func TestGetRetriesTransientThenSucceeds(t *testing.T) {
	var n atomic.Int32
	c, waits := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	var out struct{ OK bool }
	if err := c.do(context.Background(), request{Method: "GET", Path: "/x", Out: &out}); err != nil || !out.OK {
		t.Fatalf("err=%v out=%v", err, out)
	}
	if n.Load() != 3 || len(*waits) != 2 || (*waits)[0] != 2*time.Second || (*waits)[1] != 2*time.Second {
		t.Fatalf("attempts=%d waits=%v", n.Load(), *waits)
	}
}

func TestGetGivesUpAfterMaxRetries(t *testing.T) {
	var n atomic.Int32
	c, waits := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	err := c.do(context.Background(), request{Method: "GET", Path: "/x"})
	if !HasStatus(err, 502) || n.Load() != MaxRetries+1 || len(*waits) != MaxRetries {
		t.Fatalf("err=%v attempts=%d waits=%v", err, n.Load(), *waits)
	}
	for i, w := range *waits { // exponential, jittered within [step/2, step]
		step := baseBackoff << i
		if w < step/2 || w > step {
			t.Errorf("wait %d = %v outside [%v,%v]", i, w, step/2, step)
		}
	}
}

func TestWritesAreNeverRetried(t *testing.T) {
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		var n atomic.Int32
		c, _ := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		var body any
		if m != "DELETE" {
			body = map[string]string{"a": "b"}
		}
		if err := c.do(context.Background(), request{Method: m, Path: "/x", Body: body}); err == nil || n.Load() != 1 {
			t.Errorf("%s: err=%v attempts=%d", m, err, n.Load())
		}
	}
}

func TestNonTransientStatusNotRetried(t *testing.T) {
	for _, code := range []int{400, 401, 404, 409, 500} {
		var n atomic.Int32
		c, _ := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(code)
		}))
		if err := c.do(context.Background(), request{Method: "GET", Path: "/x"}); err == nil || n.Load() != 1 {
			t.Errorf("%d: err=%v attempts=%d", code, err, n.Load())
		}
	}
}

func TestLongRetryAfterReturnsAnswer(t *testing.T) {
	var n atomic.Int32
	c, waits := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	err := c.do(context.Background(), request{Method: "GET", Path: "/x"})
	if !HasStatus(err, 429) || n.Load() != 1 || len(*waits) != 0 {
		t.Fatalf("err=%v attempts=%d waits=%v", err, n.Load(), *waits)
	}
}

func TestTransportErrorRetriedForGet(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused
	c, err := New(Config{Endpoint: url, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	var waits int
	c.sleep = func(context.Context, time.Duration) error { waits++; return nil }
	if err := c.do(context.Background(), request{Method: "GET", Path: "/x"}); err == nil || waits != MaxRetries {
		t.Fatalf("err=%v waits=%d", err, waits)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	var n atomic.Int32
	c, _ := retryClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	if err := c.do(ctx, request{Method: "GET", Path: "/x"}); err == nil || n.Load() != 1 {
		t.Fatalf("err=%v attempts=%d", err, n.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"5", 5 * time.Second, true},
		{" 1 ", time.Second, true},
		{"0", 0, false},
		{"-3", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{now.Add(7 * time.Second).UTC().Format(http.TimeFormat), 7 * time.Second, true},
		{now.Add(-time.Minute).UTC().Format(http.TimeFormat), 0, false},
		{"99999999", time.Hour, true},
	}
	for _, c := range cases {
		got, ok := parseRetryAfter(c.in, now)
		if got != c.want || ok != c.ok {
			t.Errorf("%q = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestD17Timeouts(t *testing.T) {
	c, err := New(Config{Endpoint: "https://x.example", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != 30*time.Second || DefaultConnectTimeout != 10*time.Second || MaxRetries != 3 {
		t.Fatal("D17 limits changed")
	}
	if backoff(10) > maxBackoff {
		t.Fatal("backoff exceeds cap")
	}
}

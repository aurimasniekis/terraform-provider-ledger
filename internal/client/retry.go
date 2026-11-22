package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Outbound limits from decision D17: a 10 second connect timeout, a 30 second total timeout per attempt, and retries
// only for idempotent reads (GET and HEAD), at most three of them, with exponential backoff and jitter that honours
// Retry-After. Writes are never retried: a repeated POST, PATCH or DELETE could apply twice.
const (
	// DefaultConnectTimeout bounds dialing the server.
	DefaultConnectTimeout = 10 * time.Second
	// MaxRetries is the number of retries after the first attempt, so a read makes at most MaxRetries+1 attempts.
	MaxRetries = 3
	// baseBackoff is the backoff before the first retry; it doubles each retry.
	baseBackoff = 250 * time.Millisecond
	// maxBackoff caps one wait, whether it comes from the backoff or from Retry-After. A server that asks for a
	// longer wait is not waited for: its answer is returned instead of stalling a Terraform run.
	maxBackoff = 10 * time.Second
)

// retryable reports whether a method is an idempotent read.
func retryable(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// retryableStatus reports whether an HTTP status is a transient answer worth retrying.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableError reports whether a transport error is transient. A cancelled or expired caller context is not.
func retryableError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

// parseRetryAfter reads a Retry-After header, either delay-seconds or an HTTP date. Missing, malformed, non-positive
// and past values report false.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 {
			return 0, false
		}
		if secs > int64(time.Hour/time.Second) {
			return time.Hour, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
	}
	return 0, false
}

// backoff returns the wait before retry number attempt (0 for the first retry): exponential with full jitter in the
// upper half, so concurrent clients spread out but never wait less than half the step.
func backoff(attempt int) time.Duration {
	d := baseBackoff << attempt
	if d > maxBackoff || d <= 0 {
		d = maxBackoff
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// retryWait decides how long to wait before the next attempt. A valid Retry-After wins over the backoff; one longer
// than maxBackoff reports false, meaning the caller should give up and return what it has.
func retryWait(attempt int, resp *http.Response, now time.Time) (time.Duration, bool) {
	if resp != nil {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
			if d > maxBackoff {
				return 0, false
			}
			return d, true
		}
	}
	return backoff(attempt), true
}

// sleepCtx waits for d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

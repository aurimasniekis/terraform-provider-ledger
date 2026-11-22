package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// APIPrefix is the Management API base path (the /api/v1 prefix; see ADR-0002 on the supported major version).
const APIPrefix = "/api/v1"

// maxResponseBytes bounds what the client reads from any response.
const maxResponseBytes = 8 << 20

// Error codes the provider reacts to. They are the stable codes of the Management API error envelope.
const (
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeVersionConflict = "version_conflict"
	CodeDefinitionInUse = "definition_in_use"
	CodeDefinitionArch  = "definition_archived"
	CodeDuplicateName   = "duplicate_name"
)

// APIError is an error answer of the Management API. It carries the code, the message the server chose and the request
// ID, never a request header or the token.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	// CurrentVersion is the server's current version of the object, when the answer was a version conflict.
	CurrentVersion int64
}

// Error implements error.
func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ledger API answered HTTP %d", e.Status)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request %s)", e.RequestID)
	}
	return b.String()
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// HasCode reports whether err is an API error with the given code.
func HasCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// IsVersionConflict reports whether err means the object changed since the version the caller sent. The API sends
// code version_conflict (ADR-0015); older builds sent conflict with a reconciliation block, which is accepted too.
func IsVersionConflict(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
		return false
	}
	return ae.Code == CodeVersionConflict || (ae.Code == CodeConflict && ae.CurrentVersion > 0)
}

var ulidPattern = regexp.MustCompile("^[0-7][0-9A-HJKMNP-TV-Z]{25}$")

// ValidID reports whether s is a canonical Ledger identifier (a ULID).
func ValidID(s string) bool { return ulidPattern.MatchString(s) }

func checkID(what, s string) error {
	if !ValidID(s) {
		return fmt.Errorf("%s is not a valid Ledger identifier", what)
	}
	return nil
}

// request describes one call. Body is JSON-encoded when not nil; Version, when positive, is sent as If-Match.
type request struct {
	Method  string
	Path    string // relative to APIPrefix, already escaped
	Query   url.Values
	Version int64
	Body    any
	Out     any
}

// do performs a request and decodes a JSON answer into r.Out when it is not nil.
func (c *Client) do(ctx context.Context, r request) error {
	u := c.BaseURL()
	u.Path = strings.TrimRight(u.Path, "/") + APIPrefix + r.Path
	if len(r.Query) > 0 {
		u.RawQuery = r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), body)
	if err != nil {
		return errors.New("building request failed")
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		ct := "application/json"
		if r.Method == http.MethodPatch {
			ct = "application/merge-patch+json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if r.Version > 0 {
		req.Header.Set("If-Match", `"`+strconv.FormatInt(r.Version, 10)+`"`)
	}
	resp, err := c.send(ctx, req)
	if err != nil {
		// *url.Error would repeat the URL; the path is harmless but the message is kept short and token-free.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("calling the Ledger API (%s %s): %w", r.Method, APIPrefix+r.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading the Ledger API answer: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return errors.New("the Ledger API answer is larger than the client accepts")
	}
	if resp.StatusCode >= 300 {
		return decodeError(resp, raw)
	}
	if r.Out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, r.Out); err != nil {
		return errors.New("the Ledger API answer is not the JSON the client expects")
	}
	return nil
}

// send issues req. Idempotent reads are retried on transient failures (D17); everything else is sent once. Each attempt
// has its own total timeout from the http.Client, and the caller's context bounds the whole call including the waits.
func (c *Client) send(ctx context.Context, req *http.Request) (*http.Response, error) {
	if !retryable(req.Method) {
		return c.http.Do(req)
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.http.Do(req)
		transient := (err != nil && retryableError(ctx, err)) || (err == nil && retryableStatus(resp.StatusCode))
		if !transient || attempt >= MaxRetries {
			return resp, err
		}
		wait, ok := retryWait(attempt, resp, time.Now())
		if !ok {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			_ = resp.Body.Close()
		}
		if serr := sleep(ctx, wait); serr != nil {
			return nil, serr
		}
	}
}

func decodeError(resp *http.Response, raw []byte) error {
	ae := &APIError{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID")}
	var env struct {
		Code           string `json:"code"`
		Message        string `json:"message"`
		RequestID      string `json:"request_id"`
		Reconciliation *struct {
			CurrentVersion int64 `json:"current_version"`
		} `json:"reconciliation"`
	}
	if json.Unmarshal(raw, &env) == nil {
		ae.Code, ae.Message = env.Code, env.Message
		if env.RequestID != "" {
			ae.RequestID = env.RequestID
		}
		if env.Reconciliation != nil {
			ae.CurrentVersion = env.Reconciliation.CurrentVersion
		}
	}
	return ae
}

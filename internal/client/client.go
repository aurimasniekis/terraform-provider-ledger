// Package client holds the HTTP client the provider uses to reach a Ledger server.
//
// This file carries the connection settings and an authenticated *http.Client. api.go holds the request helper and the
// error type, and one file per area of the Management API holds its typed methods (organisation.go so far). The
// methods are hand-written against the served OpenAPI document; each resource task adds its own.
package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds every request made through the client. Timeout and retry limits are still an open product
// decision; this is a conservative placeholder, not a decided value.
const DefaultTimeout = 30 * time.Second

// Secret is a credential. It never prints its value: fmt verbs, %v, %+v and %#v all show a redaction marker.
type Secret string

const redacted = "[REDACTED]"

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return redacted }

// Reveal returns the credential. Call it only where the value is put on the wire.
func (s Secret) Reveal() string { return string(s) }

// Config is the resolved connection configuration.
type Config struct {
	// Endpoint is the base URL of the Ledger server, with no trailing slash.
	Endpoint string
	// Token is the API token sent as a bearer credential.
	Token Secret
	// Timeout overrides DefaultTimeout when positive.
	Timeout time.Duration
}

// Client is a connection to one Ledger server.
type Client struct {
	base *url.URL
	http *http.Client
}

// New validates cfg and builds a Client. It makes no network call.
func New(cfg Config) (*Client, error) {
	base, err := ParseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if cfg.Token.Reveal() == "" {
		return nil, errors.New("token is empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		base: base,
		http: &http.Client{
			Timeout:   timeout,
			Transport: &authTransport{host: base.Host, token: cfg.Token, next: http.DefaultTransport},
			// Never follow a redirect: the token must only go to the configured server.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// BaseURL returns a copy of the server's base URL.
func (c *Client) BaseURL() *url.URL {
	u := *c.base
	return &u
}

// HTTPClient returns the authenticated HTTP client. It only sends requests to the configured host.
func (c *Client) HTTPClient() *http.Client { return c.http }

// ParseEndpoint validates a Ledger endpoint and returns it without a trailing slash. The endpoint must be an
// absolute http or https URL with a host, and must not carry credentials, a query or a fragment.
func ParseEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		// url.Error would echo the input, which may contain credentials: report only that it is malformed.
		return nil, errors.New("endpoint is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("endpoint must use the http or https scheme")
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("endpoint must include a host")
	}
	if u.User != nil {
		return nil, errors.New("endpoint must not contain credentials; use the token attribute")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("endpoint must not contain a query or a fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// IsPlainHTTPRemote reports whether the endpoint sends the token unencrypted to a non-loopback host.
func IsPlainHTTPRemote(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	switch h := u.Hostname(); h {
	case "localhost", "127.0.0.1", "::1":
		return false
	default:
		return !strings.HasSuffix(h, ".localhost")
	}
}

// authTransport adds the bearer token, and only for requests to the configured host.
type authTransport struct {
	host  string
	token Secret
	next  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.host {
		return nil, fmt.Errorf("refusing to send credentials to a host other than the configured Ledger endpoint")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token.Reveal())
	return t.next.RoundTrip(clone)
}

package client

import (
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
)

// SupportedAPIMajor and SupportedAPIMinor are the Management API version this provider was written against (ADR-0008).
// A server on another major version is refused. A server on a newer minor version is used with a warning, because a minor
// version only adds to the contract. Patch versions are not compared.
const (
	SupportedAPIMajor = 1
	SupportedAPIMinor = 0
)

// openAPIPath is the OpenAPI document of the Management API, relative to APIPrefix. It is public: no token is needed.
const openAPIPath = "/openapi.json"

// maxOpenAPIBytes bounds the OpenAPI document the check reads.
const maxOpenAPIBytes = 16 << 20

// maxVersionLength bounds the version string the server may report.
const maxVersionLength = 64

var versionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)([-+][0-9A-Za-z.+-]*)?$`)

// errNoVersion is returned when the OpenAPI document has no usable info.version. The message never echoes the document.
var errNoVersion = errors.New("the Ledger server's OpenAPI document has no usable info.version")

// APIMajorError reports a server whose Management API major version this provider does not support.
type APIMajorError struct {
	// Served is the info.version the server reports.
	Served string
	// Major is the served major version.
	Major int
}

// Error implements error.
func (e *APIMajorError) Error() string {
	return fmt.Sprintf("the Ledger server serves API version %s (major %d), but this provider supports major %d (the %s prefix). "+
		"Use a provider release for that API version, or a Ledger server on API major %d.",
		e.Served, e.Major, SupportedAPIMajor, APIPrefix, SupportedAPIMajor)
}

// APIVersionCheck is the outcome of a compatible check.
type APIVersionCheck struct {
	// Served is the info.version the server reports, for example 1.0.0.
	Served string
	// Warning is set when the server is on a newer minor version than this provider was written against.
	Warning string
}

// CheckAPIVersion reads info.version from the OpenAPI document the server serves and judges it (ADR-0008). An unreachable
// server, an answer that is not HTTP 200 and a document without a usable version are errors, not passes: the provider
// cannot tell that the server is compatible, so it must not use it.
func (c *Client) CheckAPIVersion(ctx context.Context) (APIVersionCheck, error) {
	served, err := c.servedAPIVersion(ctx)
	if err != nil {
		return APIVersionCheck{}, err
	}
	return JudgeAPIVersion(served)
}

// JudgeAPIVersion applies the compatibility policy to a reported version. An unsupported major is an *APIMajorError.
func JudgeAPIVersion(served string) (APIVersionCheck, error) {
	out := APIVersionCheck{Served: served}
	m := versionPattern.FindStringSubmatch(served)
	if m == nil || len(served) > maxVersionLength {
		return out, errNoVersion
	}
	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])
	if errMajor != nil || errMinor != nil {
		return out, errNoVersion
	}
	if major != SupportedAPIMajor {
		return out, &APIMajorError{Served: served, Major: major}
	}
	if minor > SupportedAPIMinor {
		out.Warning = fmt.Sprintf("The Ledger server serves API version %s, a newer minor version than %d.%d, which this provider "+
			"was written against. The major version is supported, so the provider continues; check the new operations before you rely on them.",
			served, SupportedAPIMajor, SupportedAPIMinor)
	}
	return out, nil
}

// servedAPIVersion fetches the OpenAPI document. The request goes through the authenticated client, which only sends
// it to the configured host; the document itself needs no token.
func (c *Client) servedAPIVersion(ctx context.Context) (string, error) {
	u := c.BaseURL()
	u.Path = strings.TrimRight(u.Path, "/") + APIPrefix + openAPIPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.New("building the API version request failed")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error repeats the URL; keep the message short and token-free, as do() does.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("cannot reach the Ledger server to read its API version (GET %s): %w", APIPrefix+openAPIPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the Ledger server answered HTTP %d for GET %s; check that the endpoint is a Ledger server",
			resp.StatusCode, APIPrefix+openAPIPath)
	}
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOpenAPIBytes)).Decode(&doc); err != nil {
		return "", errors.New("the Ledger server's OpenAPI document is not valid JSON")
	}
	return doc.Info.Version, nil
}

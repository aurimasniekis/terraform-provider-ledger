package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// stubAPIVersion stands in for the real check in the tests that are not about the API version: they must not reach a server.
func stubAPIVersion(context.Context, *client.Client) (client.APIVersionCheck, error) {
	return client.APIVersionCheck{Served: "1.0.0"}, nil
}

// openAPIServer serves the OpenAPI document at the path Configure reads, with the given status and info.version.
func openAPIServer(t *testing.T, status int, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/openapi.json" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"openapi":"3.1.1","info":{"version":%q},"paths":{}}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withAPIDocument serves the OpenAPI document that Configure checks (ADR-0008), at the supported version, in front of a fake
// that only serves the resource endpoints.
func withAPIDocument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/openapi.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openapi":"3.1.1","info":{"version":"1.0.0"},"paths":{}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// realAPIProvider runs the real version check; the other tests stub it.
func realAPIProvider(env map[string]string) *ledgerProvider {
	return &ledgerProvider{version: "test", getenv: envOf(env), readFile: os.ReadFile}
}

// assertErrorDiag checks for an error with the summary whose detail mentions want, and that no diagnostic shows secret.
func assertErrorDiag(t *testing.T, resp *provider.ConfigureResponse, summary, want, secret string) {
	t.Helper()
	found := false
	for _, d := range resp.Diagnostics.Errors() {
		if d.Summary() == summary && strings.Contains(d.Detail(), want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want error %q mentioning %q, got %v", summary, want, resp.Diagnostics)
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Summary()+d.Detail(), secret) {
			t.Errorf("diagnostic leaks the token: %s / %s", d.Summary(), d.Detail())
		}
	}
}

func TestConfigureRefusesUnsupportedAPIMajor(t *testing.T) {
	const secret = "api-major-secret"
	srv := openAPIServer(t, http.StatusOK, "2.0.0")
	p := realAPIProvider(nil)
	resp := configure(t, p, config(t, p, srv.URL, secret, nil))
	assertErrorDiag(t, resp, "Unsupported Ledger API version", "2.0.0", secret)
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Error("no client may reach the resources after a refused API version")
	}
}

func TestConfigureAcceptsSupportedAPI(t *testing.T) {
	srv := openAPIServer(t, http.StatusOK, "1.0.0")
	p := realAPIProvider(nil)
	resp := configure(t, p, config(t, p, srv.URL, "tok", nil))
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("want no diagnostics, got %v", resp.Diagnostics)
	}
	if _, ok := resp.ResourceData.(*client.Client); !ok {
		t.Fatalf("no client after a supported API version: %T", resp.ResourceData)
	}
}

func TestConfigureWarnsOnNewerMinorAPI(t *testing.T) {
	srv := openAPIServer(t, http.StatusOK, "1.4.0")
	p := realAPIProvider(nil)
	resp := configure(t, p, config(t, p, srv.URL, "tok", nil))
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("want exactly one warning and no error, got %v", resp.Diagnostics)
	}
	if _, ok := resp.ResourceData.(*client.Client); !ok {
		t.Fatal("a newer minor version must still configure the provider")
	}
}

func TestConfigureCannotVerifyAPIVersion(t *testing.T) {
	const secret = "unverified-secret"
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	cases := []struct {
		name     string
		endpoint string
	}{
		{"document missing", openAPIServer(t, http.StatusNotFound, "").URL},
		{"no usable version", openAPIServer(t, http.StatusOK, "latest").URL},
		{"unreachable", deadURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := realAPIProvider(nil)
			resp := configure(t, p, config(t, p, tc.endpoint, secret, nil))
			assertErrorDiag(t, resp, "Cannot verify the Ledger API version", "Ledger", secret)
			if resp.ResourceData != nil {
				t.Error("an unverified server must not be configured")
			}
		})
	}
}

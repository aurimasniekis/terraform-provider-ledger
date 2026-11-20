package provider

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func filesOf(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrNotExist}
	}
}

func TestResolvePrecedence(t *testing.T) {
	files := map[string]string{
		"/cfg/token": "file-config-token\n",
		"/env/token": "  file-env-token  \r\n",
	}
	cases := []struct {
		name               string
		cfg                rawConfig
		env                map[string]string
		wantEndpoint       string
		wantToken          string
		wantTokenSource    string
		wantEndpointSource string
	}{
		{
			name:         "config beats env for endpoint and token",
			cfg:          rawConfig{Endpoint: "https://cfg.example", Token: "cfg-token"},
			env:          map[string]string{EnvEndpoint: "https://env.example", EnvToken: "env-token", EnvTokenFile: "/env/token"},
			wantEndpoint: "https://cfg.example", wantToken: "cfg-token",
			wantEndpointSource: SourceConfig, wantTokenSource: "token in the provider configuration",
		},
		{
			name:         "token attribute beats token_file attribute",
			cfg:          rawConfig{Endpoint: "https://cfg.example", Token: "cfg-token", TokenFile: "/cfg/token"},
			wantEndpoint: "https://cfg.example", wantToken: "cfg-token",
			wantEndpointSource: SourceConfig, wantTokenSource: "token in the provider configuration",
		},
		{
			name:         "token_file attribute beats LEDGER_TOKEN",
			cfg:          rawConfig{Endpoint: "https://cfg.example", TokenFile: "/cfg/token"},
			env:          map[string]string{EnvToken: "env-token"},
			wantEndpoint: "https://cfg.example", wantToken: "file-config-token",
			wantEndpointSource: SourceConfig, wantTokenSource: SourceTokenFile,
		},
		{
			name:         "LEDGER_TOKEN beats LEDGER_TOKEN_FILE",
			cfg:          rawConfig{},
			env:          map[string]string{EnvEndpoint: "https://env.example", EnvToken: "env-token", EnvTokenFile: "/env/token"},
			wantEndpoint: "https://env.example", wantToken: "env-token",
			wantEndpointSource: SourceEnv + " " + EnvEndpoint, wantTokenSource: SourceEnv + " " + EnvToken,
		},
		{
			name:         "LEDGER_TOKEN_FILE alone, trimmed",
			env:          map[string]string{EnvEndpoint: "https://env.example", EnvTokenFile: "/env/token"},
			wantEndpoint: "https://env.example", wantToken: "file-env-token",
			wantEndpointSource: SourceEnv + " " + EnvEndpoint, wantTokenSource: SourceEnv + " " + EnvTokenFile,
		},
		{
			name:         "blank values count as unset",
			cfg:          rawConfig{Endpoint: "  ", Token: "\t", TokenFile: ""},
			env:          map[string]string{EnvEndpoint: "https://env.example", EnvToken: "env-token"},
			wantEndpoint: "https://env.example", wantToken: "env-token",
			wantEndpointSource: SourceEnv + " " + EnvEndpoint, wantTokenSource: SourceEnv + " " + EnvToken,
		},
		{
			name:         "mixed: endpoint from config, token from env",
			cfg:          rawConfig{Endpoint: "https://cfg.example"},
			env:          map[string]string{EnvEndpoint: "https://env.example", EnvToken: "env-token"},
			wantEndpoint: "https://cfg.example", wantToken: "env-token",
			wantEndpointSource: SourceConfig, wantTokenSource: SourceEnv + " " + EnvToken,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.cfg, envOf(tc.env), filesOf(files))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Endpoint != tc.wantEndpoint || got.Token != tc.wantToken {
				t.Errorf("endpoint/token mismatch: endpoint=%q (want %q), token matches=%v", got.Endpoint, tc.wantEndpoint, got.Token == tc.wantToken)
			}
			if got.EndpointSource != tc.wantEndpointSource || got.TokenSource != tc.wantTokenSource {
				t.Errorf("sources = %q / %q, want %q / %q", got.EndpointSource, got.TokenSource, tc.wantEndpointSource, tc.wantTokenSource)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	files := map[string]string{"/empty": " \n", "/multi": "a\nb\n"}
	cases := []struct {
		name     string
		cfg      rawConfig
		env      map[string]string
		wantAttr string
		wantText string
	}{
		{"missing endpoint", rawConfig{Token: "tok"}, nil, "endpoint", "LEDGER_ENDPOINT"},
		{"missing token", rawConfig{Endpoint: "https://x.example"}, nil, "token", "LEDGER_TOKEN_FILE"},
		{"missing token file", rawConfig{Endpoint: "https://x.example", TokenFile: "/nope"}, nil, "token_file", "does not exist"},
		{"unusable token_file does not fall through", rawConfig{Endpoint: "https://x.example", TokenFile: "/nope"}, map[string]string{EnvToken: "env-token"}, "token_file", "/nope"},
		{"empty token file", rawConfig{Endpoint: "https://x.example", TokenFile: "/empty"}, nil, "token_file", "contains no token"},
		{"multi-line token file", rawConfig{Endpoint: "https://x.example", TokenFile: "/multi"}, nil, "token_file", "single line"},
		{"unusable LEDGER_TOKEN_FILE is reported as its variable, not an attribute", rawConfig{Endpoint: "https://x.example"}, map[string]string{EnvTokenFile: "/nope"}, "", EnvTokenFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolve(tc.cfg, envOf(tc.env), filesOf(files))
			if err == nil {
				t.Fatal("expected an error")
			}
			if err.Attribute != tc.wantAttr || !strings.Contains(err.Detail, tc.wantText) {
				t.Errorf("got attr=%q detail=%q", err.Attribute, err.Detail)
			}
		})
	}
}

func TestResolveNeverEchoesFileContents(t *testing.T) {
	const secret = "super-secret-token-value"
	_, err := resolve(rawConfig{Endpoint: "https://x.example", TokenFile: "/multi"}, envOf(nil),
		func(string) ([]byte, error) { return []byte(secret + "\nsecond line"), nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks token file contents: %v", err)
	}
}

func TestReadTokenFileReadError(t *testing.T) {
	_, err := readTokenFile("token_file", SourceTokenFile, "/x", func(string) ([]byte, error) { return nil, errors.New("boom with /secret/detail") })
	if err == nil || strings.Contains(err.Error(), "boom") {
		t.Errorf("unexpected error text: %v", err)
	}
}

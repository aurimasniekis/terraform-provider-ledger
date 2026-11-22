// Package acceptance holds the tests that run the provider's client against a real, local Ledger.
//
// They are off unless TF_ACC=1 and LEDGER_ACC_SOURCE (a go-ledger checkout at the commit in
// internal/ledgertest/ledger.pin) are set; "make acceptance" sets both up. See docs/decisions/0002.
package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

func get(t *testing.T, s *ledgertest.Server, path, bearer string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

// TestIgnoresWorkingDirectoryDotEnv checks that a .env in the caller's directory cannot reach the Ledger under test.
func TestIgnoresWorkingDirectoryDotEnv(t *testing.T) {
	if src := os.Getenv(ledgertest.EnvSource); src != "" {
		abs, err := filepath.Abs(src)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(ledgertest.EnvSource, abs)
	}
	t.Chdir(t.TempDir())
	poison := "LEDGER_LOG_LEVEL=not-a-level\nLEDGER_HTTP_LISTEN=256.256.256.256:1\nLEDGER_KEK_VALUE=poisoned\n"
	if err := os.WriteFile(".env", []byte(poison), 0o600); err != nil {
		t.Fatal(err)
	}
	s := ledgertest.Start(t)
	if code, _ := get(t, s, "/api/v1/me", s.Token.Reveal()); code != http.StatusOK {
		t.Fatalf("GET /api/v1/me: HTTP %d", code)
	}
}

// TestLocalLedger starts the pinned Ledger once and checks what every resource test relies on.
func TestLocalLedger(t *testing.T) {
	s := ledgertest.Start(t)

	t.Run("serves the pinned API version", func(t *testing.T) {
		if err := ledgertest.CheckCompatible(s.Pin, s.APIVersion); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bootstrap token authenticates as a root token", func(t *testing.T) {
		code, body := get(t, s, "/api/v1/me", s.Token.Reveal())
		if code != http.StatusOK {
			t.Fatalf("GET /api/v1/me: HTTP %d", code)
		}
		var me struct {
			Kind string `json:"kind"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal(body, &me); err != nil {
			t.Fatal(err)
		}
		if me.Kind != "token" || me.Role != "administrator" {
			t.Fatalf("got kind %q role %q, want a token held by an administrator", me.Kind, me.Role)
		}
	})

	t.Run("API refuses a missing or wrong token", func(t *testing.T) {
		if code, _ := get(t, s, "/api/v1/me", ""); code != http.StatusUnauthorized {
			t.Errorf("no token: HTTP %d, want 401", code)
		}
		wrong := strings.Repeat("x", len(s.Token.Reveal()))
		if code, _ := get(t, s, "/api/v1/me", wrong); code != http.StatusUnauthorized {
			t.Errorf("wrong token: HTTP %d, want 401", code)
		}
	})
}

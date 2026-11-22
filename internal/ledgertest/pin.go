// Package ledgertest builds and starts a real, local Ledger server for the provider's acceptance tests.
//
// Everything stays on the machine: the server is built from a pinned source checkout, listens on loopback only,
// uses a temporary database and a synthetic key-encryption key, and the first API token is created with the
// "ledger token create" command. No bank, model, rate provider or other network service is involved, and no
// real Ledger instance or credential is ever used.
package ledgertest

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// SupportedAPIMajor is the Management API major version (the /api/v1 prefix) this provider speaks.
const SupportedAPIMajor = client.SupportedAPIMajor

//go:embed ledger.pin
var pinFile string

// Pin names the Ledger build the acceptance tests run against.
type Pin struct {
	// Commit is the full go-ledger commit hash.
	Commit string
	// APIVersion is the OpenAPI info.version that commit serves.
	APIVersion string
}

// EmbeddedPin returns the pin committed in ledger.pin.
func EmbeddedPin() (Pin, error) { return ParsePin(pinFile) }

// ParsePin reads key=value lines; blank lines and lines starting with # are ignored.
func ParsePin(text string) (Pin, error) {
	var p Pin
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Pin{}, fmt.Errorf("pin: malformed line %q", line)
		}
		switch strings.TrimSpace(k) {
		case "commit":
			p.Commit = strings.TrimSpace(v)
		case "api_version":
			p.APIVersion = strings.TrimSpace(v)
		default:
			return Pin{}, fmt.Errorf("pin: unknown key %q", strings.TrimSpace(k))
		}
	}
	if len(p.Commit) != 40 || strings.Trim(p.Commit, "0123456789abcdef") != "" {
		return Pin{}, fmt.Errorf("pin: commit must be a full lowercase 40-character hash, got %q", p.Commit)
	}
	if _, err := apiMajor(p.APIVersion); err != nil {
		return Pin{}, fmt.Errorf("pin: api_version: %w", err)
	}
	return p, nil
}

func apiMajor(v string) (int, error) {
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a semantic version", v)
	}
	return n, nil
}

// CheckCompatible reports whether a server that serves the OpenAPI info.version served is usable by this
// provider: it must be on the supported major (the /api/v1 prefix) and must be the version the pin recorded,
// so a change of the API contract in a new Ledger build is a deliberate pin update, not a silent drift.
func CheckCompatible(p Pin, served string) error {
	major, err := apiMajor(served)
	if err != nil {
		return fmt.Errorf("served API version: %w", err)
	}
	if major != SupportedAPIMajor {
		return fmt.Errorf("served API version %s is major %d; this provider supports /api/v%d", served, major, SupportedAPIMajor)
	}
	if served != p.APIVersion {
		return fmt.Errorf("served API version %s differs from the pinned %s: update ledger.pin deliberately", served, p.APIVersion)
	}
	return nil
}

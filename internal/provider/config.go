package provider

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Environment variable names read by the provider.
const (
	EnvEndpoint  = "LEDGER_ENDPOINT"
	EnvToken     = "LEDGER_TOKEN"
	EnvTokenFile = "LEDGER_TOKEN_FILE"
)

// Source names where a resolved value came from. They are reported in diagnostics and never contain a value.
const (
	SourceConfig    = "provider configuration"
	SourceTokenFile = "token_file in the provider configuration"
	SourceEnv       = "environment variable"
)

// rawConfig holds the provider block values after unwrapping; an empty string means "not set".
type rawConfig struct {
	Endpoint  string
	Token     string
	TokenFile string
}

// resolved is the final connection setting.
type resolved struct {
	Endpoint       string
	Token          string
	EndpointSource string
	TokenSource    string
}

// configError names the attribute a problem belongs to. Attribute is empty when the problem comes from an environment
// variable, which has no attribute to point at. Its message never contains a token value.
type configError struct {
	Attribute string
	Summary   string
	Detail    string
}

func (e *configError) Error() string { return e.Summary + ": " + e.Detail }

// resolve applies the documented precedence. Explicit provider configuration always wins over the environment:
//
//	endpoint: endpoint attribute, then LEDGER_ENDPOINT.
//	token:    token attribute, then token_file attribute, then LEDGER_TOKEN, then LEDGER_TOKEN_FILE.
//
// Blank values (empty or only whitespace) count as unset. The first source that is set is the only one used; a set
// but unusable source (for example an unreadable token file) is an error and does not fall through to the next.
// getenv and readFile are injected so the precedence can be tested without touching the process environment.
func resolve(cfg rawConfig, getenv func(string) string, readFile func(string) ([]byte, error)) (resolved, *configError) {
	var out resolved

	switch {
	case blank(cfg.Endpoint) == "" && blank(getenv(EnvEndpoint)) == "":
		return out, &configError{"endpoint", "Missing Ledger endpoint",
			"Set the endpoint attribute or the " + EnvEndpoint + " environment variable."}
	case blank(cfg.Endpoint) != "":
		out.Endpoint, out.EndpointSource = blank(cfg.Endpoint), SourceConfig
	default:
		out.Endpoint, out.EndpointSource = blank(getenv(EnvEndpoint)), SourceEnv+" "+EnvEndpoint
	}

	switch {
	case blank(cfg.Token) != "":
		out.Token, out.TokenSource = strings.TrimSpace(cfg.Token), "token in the provider configuration"
	case blank(cfg.TokenFile) != "":
		tok, err := readTokenFile("token_file", SourceTokenFile, cfg.TokenFile, readFile)
		if err != nil {
			return out, err
		}
		out.Token, out.TokenSource = tok, SourceTokenFile
	case blank(getenv(EnvToken)) != "":
		out.Token, out.TokenSource = strings.TrimSpace(getenv(EnvToken)), SourceEnv+" "+EnvToken
	case blank(getenv(EnvTokenFile)) != "":
		tok, err := readTokenFile("", SourceEnv+" "+EnvTokenFile, getenv(EnvTokenFile), readFile)
		if err != nil {
			return out, err
		}
		out.Token, out.TokenSource = tok, SourceEnv+" "+EnvTokenFile
	default:
		return out, &configError{"token", "Missing Ledger API token",
			"Set the token or token_file attribute, or the " + EnvToken + " or " + EnvTokenFile + " environment variable."}
	}
	return out, nil
}

func blank(s string) string { return strings.TrimSpace(s) }

// readTokenFile reads a token from path. Surrounding whitespace, including the final newline, is removed.
// attr is the attribute to report, or empty for an environment source; source says where path came from.
// The error mentions the path, the source and the reason but never file contents.
func readTokenFile(attr, source, path string, readFile func(string) ([]byte, error)) (string, *configError) {
	path = strings.TrimSpace(path)
	data, err := readFile(path)
	if err != nil {
		reason := "the file could not be read"
		switch {
		case errors.Is(err, os.ErrNotExist):
			reason = "the file does not exist"
		case errors.Is(err, os.ErrPermission):
			reason = "permission denied"
		}
		return "", &configError{attr, "Cannot read Ledger token file", fmt.Sprintf("%q (from %s): %s.", path, source, reason)}
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", &configError{attr, "Empty Ledger token file", fmt.Sprintf("%q (from %s) contains no token.", path, source)}
	}
	if strings.ContainsAny(tok, "\r\n") {
		return "", &configError{attr, "Invalid Ledger token file", fmt.Sprintf("%q (from %s) must contain a single line.", path, source)}
	}
	return tok, nil
}

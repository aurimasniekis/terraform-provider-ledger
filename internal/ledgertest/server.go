package ledgertest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

const (
	// EnvAcc switches the acceptance tests on, as in terraform-plugin-testing.
	EnvAcc = "TF_ACC"
	// EnvSource names a go-ledger checkout at the pinned commit. The harness verifies the commit and builds from it.
	EnvSource = "LEDGER_ACC_SOURCE"

	// syntheticKEK is a throwaway key-encryption key for a throwaway database. It protects nothing.
	syntheticKEK = "acceptance-only-not-a-secret-0123456789abcdef"
	readyTimeout = 60 * time.Second
)

// Server is a running local Ledger.
type Server struct {
	// URL is the base URL of the server, for example http://127.0.0.1:41234.
	URL string
	// Token is a root API token created through the ledger CLI. It is only valid for this server's temporary database.
	Token client.Secret
	// Pin is the build the server was made from.
	Pin Pin
	// APIVersion is the OpenAPI info.version the running server serves.
	APIVersion string
}

// Start builds the pinned Ledger, creates a temporary database, bootstraps a root token with the CLI and starts the
// server on loopback. It skips the test unless TF_ACC is set, fails when the source checkout is missing or not at the
// pin, and stops the server and removes the database when the test ends.
func Start(t *testing.T) *Server {
	t.Helper()
	if os.Getenv(EnvAcc) == "" {
		t.Skipf("acceptance test: set %s=1 and %s to a go-ledger checkout at the pinned commit", EnvAcc, EnvSource)
	}
	pin, err := EmbeddedPin()
	if err != nil {
		t.Fatal(err)
	}
	src := os.Getenv(EnvSource)
	if src == "" {
		t.Fatalf("%s is set but %s is not: point it at a go-ledger checkout at commit %s", EnvAcc, EnvSource, pin.Commit)
	}
	if err := verifyCommit(EnvSource, src, pin.Commit); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "ledger")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/ledger")
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building ledger from %s: %v\n%s", pin.Commit, err, out)
	}

	listen, err := freeAddr()
	if err != nil {
		t.Fatal(err)
	}
	mgmt, err := freeAddr()
	if err != nil {
		t.Fatal(err)
	}
	env := isolatedEnv(dir,
		"LEDGER_ENV_FILE="+filepath.Join(dir, "no-such.env"),
		"LEDGER_HTTP_LISTEN="+listen,
		"LEDGER_HTTP_MANAGEMENT_LISTEN="+mgmt,
		"LEDGER_KEK_VALUE="+syntheticKEK,
		"LEDGER_DATABASE_PATH="+filepath.Join(dir, "ledger.db"),
	)

	// The first token comes from the CLI, before the server starts (it holds the database lock).
	tokenCmd := exec.Command(bin, "token", "create", "--create", "administrator", "--root", "--name", "acceptance", "acceptance-admin")
	tokenCmd.Env = env
	tokenCmd.Dir = dir
	var stdout, stderr bytes.Buffer
	tokenCmd.Stdout, tokenCmd.Stderr = &stdout, &stderr
	if err := tokenCmd.Run(); err != nil {
		// stderr describes the failure and never carries the token (it is printed once, on stdout).
		t.Fatalf("ledger token create: %v\n%s", err, stderr.String())
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		t.Fatal("ledger token create printed no token")
	}

	logPath := filepath.Join(dir, "serve.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	serve := exec.Command(bin, "serve")
	serve.Env = env
	serve.Dir = dir
	dieWithParent(serve)
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatalf("starting ledger serve: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = serve.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = serve.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = serve.Process.Kill()
			<-exited
		}
		_ = logFile.Close()
	})

	base := "http://" + listen
	served, err := waitForAPI(base, exited)
	if err != nil {
		logs, _ := os.ReadFile(logPath)
		t.Fatalf("ledger serve did not become ready: %v\nserver output:\n%s", err, logs)
	}
	if err := CheckCompatible(pin, served); err != nil {
		t.Fatal(err)
	}
	return &Server{URL: base, Token: client.Secret(token), Pin: pin, APIVersion: served}
}

// verifyCommit fails unless the checkout at dir is exactly at want and has no local changes to tracked files. env names
// the environment variable that points at dir, so the message tells the person which setting to fix.
func verifyCommit(env, dir, want string) error {
	got, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("%s=%s is not a git checkout: %w", env, dir, err)
	}
	if got != want {
		return fmt.Errorf("%s is at %s, but it must be at %s: check out that commit", env, got, want)
	}
	if dirty, err := git(dir, "status", "--porcelain", "--untracked-files=no"); err != nil || dirty != "" {
		return fmt.Errorf("%s has local changes to tracked files; the build would not be the commit %s", env, want)
	}
	return nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// isolatedEnv is the environment of the CLI and the server: no inherited LEDGER_* settings, credentials or proxies,
// HOME inside the temporary directory, and LEDGER_ENV_FILE (passed by the caller) naming a file that does not exist,
// so nothing from the developer's own Ledger can leak in and nothing is configured to reach a network service.
func isolatedEnv(home string, extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TZ=UTC"}
	return append(env, extra...)
}

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String(), nil
}

// waitForAPI polls the public OpenAPI document until the server answers and returns its info.version.
func waitForAPI(base string, exited <-chan struct{}) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	for {
		select {
		case <-exited:
			return "", errors.New("the server exited")
		default:
		}
		if v, err := fetchAPIVersion(ctx, base); err == nil {
			return v, nil
		} else if ctx.Err() != nil {
			return "", fmt.Errorf("timed out after %s: %w", readyTimeout, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fetchAPIVersion reads info.version from the served OpenAPI document (public, no token).
func fetchAPIVersion(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/openapi.json", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openapi.json: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&doc); err != nil {
		return "", fmt.Errorf("openapi.json: %w", err)
	}
	return doc.Info.Version, nil
}

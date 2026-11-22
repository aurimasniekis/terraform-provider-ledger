package ledgertest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestStartRecoversFromPortCollision needs the acceptance setup (TF_ACC=1, LEDGER_ACC_SOURCE). A fake server holds the
// first port and answers like another Ledger would: the public OpenAPI document, 401 elsewhere. launch must fail and
// stop its process; the retry loop must then succeed on fresh ports with the same directory and token.
func TestStartRecoversFromPortCollision(t *testing.T) {
	if os.Getenv(EnvAcc) == "" {
		t.Skipf("acceptance test: set %s=1 and %s to a go-ledger checkout at the pinned commit", EnvAcc, EnvSource)
	}
	pin, err := EmbeddedPin()
	if err != nil {
		t.Fatal(err)
	}
	src := os.Getenv(EnvSource)
	if src == "" {
		t.Fatalf("%s is set but %s is not", EnvAcc, EnvSource)
	}
	if err := verifyCommit(EnvSource, src, pin.Commit); err != nil {
		t.Fatal(err)
	}
	p := prepare(t, pin, src)

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/openapi.json" {
			_, _ = w.Write([]byte(`{"info":{"version":"` + pin.APIVersion + `"}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(fake.Close)
	taken := strings.TrimPrefix(fake.URL, "http://")

	// 1. launch on the held port fails, and says why.
	mgmt, err := freeAddr()
	if err != nil {
		t.Fatal(err)
	}
	env := append(append([]string{}, p.baseEnv...), "LEDGER_HTTP_LISTEN="+taken, "LEDGER_HTTP_MANAGEMENT_LISTEN="+mgmt)
	if _, _, err := launch(t, p.bin, p.dir, env, fake.URL, p.token, 1); err == nil {
		t.Fatal("launch succeeded on a port held by another server")
	}

	// 2. Start's loop: the first pick is the held port, later picks are free. It must recover on the second attempt.
	picks := 0
	orig := pickAddr
	pickAddr = func() (string, error) {
		picks++
		if picks == 1 {
			return taken, nil
		}
		return orig()
	}
	t.Cleanup(func() { pickAddr = orig })
	srv := startLoop(t, p, 2)
	if srv.URL == fake.URL {
		t.Fatal("the server claims the held port")
	}
	if picks != 4 {
		t.Fatalf("want 2 attempts of 2 picks each, got %d picks", picks)
	}
}

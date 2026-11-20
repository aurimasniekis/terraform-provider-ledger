package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretNeverPrints(t *testing.T) {
	s := Secret("abc123")
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s), fmt.Sprint(Config{Token: s}), fmt.Sprintf("%+v", Config{Token: s})} {
		if strings.Contains(out, "abc123") {
			t.Errorf("secret leaked in %q", out)
		}
	}
}

func TestParseEndpoint(t *testing.T) {
	ok := map[string]string{
		"https://ledger.example":           "https://ledger.example",
		"https://ledger.example/":          "https://ledger.example",
		" http://127.0.0.1:8080/base/api/": "http://127.0.0.1:8080/base/api",
	}
	for in, want := range ok {
		u, err := ParseEndpoint(in)
		if err != nil || u.String() != want {
			t.Errorf("ParseEndpoint(%q) = %v, %v; want %q", in, u, err, want)
		}
	}
	for _, in := range []string{"", "ledger.example", "ftp://x.example", "https://", "https://u:p@x.example", "https://x.example?a=1", "https://x.example#f", "https://x.example?", "://bad"} {
		if _, err := ParseEndpoint(in); err == nil {
			t.Errorf("ParseEndpoint(%q) accepted", in)
		}
	}
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Config{Endpoint: "https://x.example"}); err == nil {
		t.Error("empty token accepted")
	}
}

func TestBearerOnlyToConfiguredHostAndNoRedirects(t *testing.T) {
	var gotAuth string
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect target was contacted") }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL, http.StatusFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.HTTPClient().Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}

	resp, err = c.HTTPClient().Get(srv.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("redirect was followed: status %d", resp.StatusCode)
	}

	if _, err := c.HTTPClient().Get(other.URL); err == nil {
		t.Error("request to another host must be refused")
	}
}

func TestIsPlainHTTPRemote(t *testing.T) {
	for in, want := range map[string]bool{
		"http://ledger.example": true, "https://ledger.example": false, "http://localhost:1": false,
		"http://127.0.0.1": false, "http://[::1]:80": false, "http://a.localhost": false,
	} {
		u, err := ParseEndpoint(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := IsPlainHTTPRemote(u); got != want {
			t.Errorf("%s: got %v want %v", in, got, want)
		}
	}
}

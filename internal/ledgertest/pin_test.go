package ledgertest

import (
	"strings"
	"testing"
)

func TestEmbeddedPinIsValid(t *testing.T) {
	p, err := EmbeddedPin()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckCompatible(p, p.APIVersion); err != nil {
		t.Fatalf("the pin must be compatible with itself: %v", err)
	}
}

func TestParsePinRejects(t *testing.T) {
	good := strings.Repeat("a", 40)
	for name, text := range map[string]string{
		"short commit": "commit=abc\napi_version=1.0.0",
		"upper commit": "commit=" + strings.ToUpper(good) + "\napi_version=1.0.0",
		"no commit":    "api_version=1.0.0",
		"bad version":  "commit=" + good + "\napi_version=latest",
		"no version":   "commit=" + good,
		"unknown key":  "commit=" + good + "\napi_version=1.0.0\nbranch=work",
		"malformed":    "commit " + good,
	} {
		if _, err := ParsePin(text); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParsePinIgnoresCommentsAndBlankLines(t *testing.T) {
	good := strings.Repeat("b", 40)
	p, err := ParsePin("# note\n\ncommit=" + good + "\n  api_version = 1.2.3 \n")
	if err != nil || p.Commit != good || p.APIVersion != "1.2.3" {
		t.Fatalf("got %+v, %v", p, err)
	}
}

func TestCheckCompatible(t *testing.T) {
	p := Pin{Commit: strings.Repeat("a", 40), APIVersion: "1.0.0"}
	if err := CheckCompatible(p, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	for name, served := range map[string]string{
		"next major":   "2.0.0",
		"minor drift":  "1.1.0",
		"not a number": "dev",
		"empty":        "",
	} {
		if err := CheckCompatible(p, served); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

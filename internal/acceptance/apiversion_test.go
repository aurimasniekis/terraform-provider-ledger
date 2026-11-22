package acceptance

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

// TestProviderChecksAPIVersionOfLocalLedger runs the API version check (ADR-0008) against the pinned local Ledger, both
// directly and through Terraform: configuring the provider for a data source read runs the real check first. A
// mismatching major cannot be served by the pinned build, so that refusal is covered by the offline unit tests only.
func TestProviderChecksAPIVersionOfLocalLedger(t *testing.T) {
	s := ledgertest.Start(t)

	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	check, err := c.CheckAPIVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Served != s.Pin.APIVersion || check.Warning != "" {
		t.Fatalf("served %q with warning %q; want the pinned %s and no warning", check.Served, check.Warning, s.Pin.APIVersion)
	}

	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{{
			Config: `data "ledger_categories" "all" {}`,
			Check:  resource.TestCheckResourceAttr("data.ledger_categories.all", "items.#", "0"),
		}},
	})
}

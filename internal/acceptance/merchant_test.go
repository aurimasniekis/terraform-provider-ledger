package acceptance

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

// merchantFixture starts a local Ledger and points the provider at it. Merchant records need no bank connection: they
// are created through the Management API, so this fixture needs nothing else.
func merchantFixture(t *testing.T) *client.Client {
	t.Helper()
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func merchantConfig(displayName, alias string, archived bool, withLabel bool) string {
	cfg := fmt.Sprintf(`
resource "ledger_merchant" "shop" {
  display_name = %[1]q
  archived     = %[3]t
}

resource "ledger_merchant_alias" "branch" {
  merchant_id = ledger_merchant.shop.id
  name        = %[2]q
}

resource "ledger_category" "groceries" {
  name = "Groceries"
}

resource "ledger_label" "reviewed" {
  name = "Reviewed"
}

resource "ledger_category_assignment" "shop" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  category_id = ledger_category.groceries.id
}

data "ledger_merchant" "by_name" {
  display_name = %[4]q
  depends_on   = [ledger_merchant_alias.branch]
}

data "ledger_merchants" "all" {
  include_archived = true
  depends_on       = [ledger_merchant_alias.branch]
}
`, displayName, alias, archived, strings.ToLower(displayName))
	if withLabel {
		cfg += `
resource "ledger_label_assignment" "shop" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  label_id    = ledger_label.reviewed.id
}

data "ledger_object_organisation" "shop" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  depends_on  = [ledger_category_assignment.shop, ledger_label_assignment.shop]
}
`
	}
	return cfg
}

func TestMerchantResourcesAndTargets(t *testing.T) {
	c := merchantFixture(t)
	ctx := context.Background()
	base := merchantConfig("Lidl", "LIDL VILNIUS", false, true)
	// The alias resource bumps the merchant's version and update time after the merchant was created.
	ignore := []string{"version", "updated_at"}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: base,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_merchant.shop", "display_name", "Lidl"),
					resource.TestCheckResourceAttr("ledger_merchant.shop", "archived", "false"),
					resource.TestCheckResourceAttr("ledger_merchant.shop", "version", "1"),
					resource.TestCheckResourceAttrSet("ledger_merchant.shop", "id"),
					resource.TestCheckResourceAttr("ledger_merchant_alias.branch", "origin", "user"),
					resource.TestCheckResourceAttr("ledger_merchant_alias.branch", "key", "lidl vilnius"),
					resource.TestCheckResourceAttrPair("data.ledger_merchant.by_name", "id", "ledger_merchant.shop", "id"),
					resource.TestCheckResourceAttr("data.ledger_merchant.by_name", "display_name", "lidl"),
					resource.TestCheckResourceAttr("data.ledger_merchant.by_name", "aliases.#", "2"),
					resource.TestCheckResourceAttr("data.ledger_merchants.all", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.shop", "category_origin", "user"),
					resource.TestCheckResourceAttrPair("data.ledger_object_organisation.shop", "category_id", "ledger_category.groceries", "id"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.shop", "label_ids.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.shop", "bucket_ids.#", "0"),
				),
			},
			{Config: base, PlanOnly: true},
			{ResourceName: "ledger_merchant.shop", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignore},
			{ResourceName: "ledger_merchant_alias.branch", ImportState: true, ImportStateVerify: true},
			{ResourceName: "ledger_category_assignment.shop", ImportState: true, ImportStateVerify: true},
			{ResourceName: "ledger_label_assignment.shop", ImportState: true, ImportStateVerify: true},
			// A rename keeps the identifier and therefore the assignments; the alias is replaced by name.
			{
				Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", false, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_merchant.shop", "display_name", "Lidl Lietuva"),
					resource.TestCheckResourceAttr("ledger_merchant_alias.branch", "key", "lidl kaunas"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.shop", "label_ids.#", "1"),
				),
			},
			// Drift: the person's category decision reset outside Terraform comes back on apply.
			{
				PreConfig: func() {
					ms, err := c.ListMerchants(ctx, true)
					if err != nil || len(ms) != 1 {
						t.Fatalf("listing merchants: %v (%d)", err, len(ms))
					}
					if err := c.ResetObjectCategory(ctx, client.ObjectMerchant, ms[0].ID); err != nil {
						t.Fatal(err)
					}
				},
				Config:             merchantConfig("Lidl Lietuva", "LIDL KAUNAS", false, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", false, true)},
			// Archive in place: the merchant keeps its assignments, and a new one by id is refused.
			{
				Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", true, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_merchant.shop", "archived", "true"),
					resource.TestCheckResourceAttrSet("ledger_merchant.shop", "archived_at"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.shop", "label_ids.#", "1"),
				),
			},
			{Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", true, true), PlanOnly: true},
			{
				Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", true, true) + `
resource "ledger_label" "second" {
  name = "Second"
}

resource "ledger_label_assignment" "second" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  label_id    = ledger_label.second.id
}
`,
				ExpectError: regexp.MustCompile(`(?is)archived`),
			},
			// Unarchive again so the final destroy can proceed.
			{Config: merchantConfig("Lidl Lietuva", "LIDL KAUNAS", false, true), Check: resource.TestCheckResourceAttr("ledger_merchant.shop", "archived", "false")},
		},
		CheckDestroy: func(*terraform.State) error {
			ms, err := c.ListMerchants(ctx, true)
			if err != nil {
				return err
			}
			if len(ms) != 0 {
				return fmt.Errorf("%d merchants left after destroy", len(ms))
			}
			return nil
		},
	})
}

// TestMerchantAliasSpellings covers how Ledger matches an alias by key: the exact display name is taken over, a case
// variant of an alias the merchant already has is refused with the stored spelling named (and adds nothing), and a new
// spelling is added.
func TestMerchantAliasSpellings(t *testing.T) {
	c := merchantFixture(t)
	cfg := func(alias string) string {
		return fmt.Sprintf(`
resource "ledger_merchant" "shop" {
  display_name = "Lidl"
}

resource "ledger_merchant_alias" "branch" {
  merchant_id = ledger_merchant.shop.id
  name        = %q
}
`, alias)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: cfg("LIDL"), ExpectError: regexp.MustCompile(`already has this alias under another\s+spelling`)},
			{
				PreConfig: func() {
					ms, err := c.ListMerchants(context.Background(), true)
					if err != nil || len(ms) != 1 || len(ms[0].Aliases) != 1 {
						t.Errorf("the refused alias must not have added anything: %v %+v", err, ms)
					}
				},
				// The display-name alias is taken over, not duplicated.
				Config: cfg("Lidl"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_merchant_alias.branch", "origin", "display_name"),
					resource.TestCheckResourceAttr("ledger_merchant_alias.branch", "key", "lidl"),
				),
			},
		},
	})
}

// TestMerchantNotABucketMember checks that a merchant is refused as a bucket member before any request is made.
func TestMerchantNotABucketMember(t *testing.T) {
	merchantFixture(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{{
			Config: `
resource "ledger_bucket" "b" {
  name = "Fixed"
}

resource "ledger_bucket_member" "m" {
  bucket_id   = ledger_bucket.b.id
  object_type = "merchant"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}
`,
			ExpectError: regexp.MustCompile(`(?is)object_type.*(commitment|account)`),
		}},
	})
}

// TestMerchantInUseDestroy covers the lifecycle rule: a merchant that is still referred to cannot be deleted.
func TestMerchantInUseDestroy(t *testing.T) {
	c := merchantFixture(t)
	ctx := context.Background()
	cfg := `
resource "ledger_merchant" "old" {
  display_name = "Old shop"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr("ledger_merchant.old", "version", "1")},
			{
				PreConfig: func() {
					ms, _ := c.ListMerchants(ctx, true)
					cat, err := c.CreateDefinition(ctx, client.Categories, "Misc", nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := c.SetObjectCategory(ctx, client.ObjectMerchant, ms[0].ID, &cat.ID); err != nil {
						t.Fatal(err)
					}
				},
				Config:      cfg,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?is)still in use.*archive`),
			},
			// Free it again so the harness destroy succeeds.
			{
				PreConfig: func() {
					ms, _ := c.ListMerchants(ctx, true)
					if err := c.ResetObjectCategory(ctx, client.ObjectMerchant, ms[0].ID); err != nil {
						t.Fatal(err)
					}
				},
				Config: cfg, PlanOnly: true,
			},
		},
	})
}

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/provider"
)

var providerFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"ledger": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// orgFixture starts a local Ledger, points the provider at it through the environment (the token never appears in
// a configuration) and creates one commitment to organise: commitments are the only object a fresh Ledger has that
// needs no bank connection.
func orgFixture(t *testing.T) (*client.Client, string) {
	t.Helper()
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"Rent","kind":"service","amount":{"amount":"850.00","currency":"EUR"},` +
		`"schedule":{"version":1,"frequency":"monthly","interval":1,"day":1},"start_date":"2026-01-01"}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.URL+"/api/v1/commitments", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token.Reveal())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating the fixture commitment: HTTP %d %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("fixture commitment answer: %v", err)
	}
	return c, created.ID
}

func organisationConfig(commitment, labelName, bucketName, childName, categoryOfCommitment string) string {
	return fmt.Sprintf(`
resource "ledger_category" "parent" {
  name = "Housing"
}

resource "ledger_category" "child" {
  name      = %[4]q
  parent_id = ledger_category.parent.id
}

resource "ledger_label" "reviewed" {
  name = %[2]q
}

resource "ledger_bucket" "fixed" {
  name = %[3]q
}

resource "ledger_bucket_member" "rent" {
  bucket_id   = ledger_bucket.fixed.id
  object_type = "commitment"
  object_id   = %[1]q
}

resource "ledger_category_assignment" "rent" {
  object_type = "commitment"
  object_id   = %[1]q
  category_id = ledger_category.%[5]s.id
}

resource "ledger_label_assignment" "rent" {
  object_type = "commitment"
  object_id   = %[1]q
  label_id    = ledger_label.reviewed.id
}

data "ledger_category" "by_name" {
  name       = "housing"
  parent_id  = "none"
  depends_on = [ledger_category.parent]
}

data "ledger_category" "by_id" {
  id         = ledger_category.parent.id
  parent_id  = "none"
  depends_on = [ledger_category.parent]
}

data "ledger_labels" "all" {
  depends_on = [ledger_label.reviewed]
}

data "ledger_buckets" "active" {
  archived   = false
  depends_on = [ledger_bucket.fixed]
}

data "ledger_categories" "children" {
  parent_id  = ledger_category.parent.id
  depends_on = [ledger_category.child]
}

data "ledger_bucket_members" "fixed" {
  bucket_id  = ledger_bucket.fixed.id
  depends_on = [ledger_bucket_member.rent]
}

data "ledger_object_organisation" "rent" {
  object_type = "commitment"
  object_id   = %[1]q
  depends_on  = [ledger_category_assignment.rent, ledger_label_assignment.rent, ledger_bucket_member.rent]
}
`, commitment, labelName, bucketName, childName, categoryOfCommitment)
}

func TestOrganisationResources(t *testing.T) {
	c, commitment := orgFixture(t)
	ctx := context.Background()
	importStep := func(name string) resource.TestStep {
		return resource.TestStep{ResourceName: name, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"reassign_to"}}
	}
	base := organisationConfig(commitment, "Reviewed", "Fixed costs", "Rent", "child")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: base,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_category.child", "name", "Rent"),
					resource.TestCheckResourceAttrPair("ledger_category.child", "parent_id", "ledger_category.parent", "id"),
					resource.TestCheckResourceAttr("ledger_category.parent", "archived", "false"),
					resource.TestCheckResourceAttr("ledger_category.parent", "version", "1"),
					resource.TestCheckResourceAttrSet("ledger_label.reviewed", "id"),
					resource.TestCheckResourceAttrPair("ledger_bucket_member.rent", "bucket_id", "ledger_bucket.fixed", "id"),
					resource.TestCheckResourceAttrSet("ledger_bucket_member.rent", "added_at"),
					// data sources
					resource.TestCheckResourceAttrPair("data.ledger_category.by_name", "id", "ledger_category.parent", "id"),
					resource.TestCheckResourceAttr("data.ledger_category.by_name", "name", "housing"),
					// id together with parent_id = "none" is a valid lookup of a top-level category.
					resource.TestCheckResourceAttrPair("data.ledger_category.by_id", "id", "ledger_category.parent", "id"),
					resource.TestCheckResourceAttr("data.ledger_category.by_id", "parent_id", "none"),
					resource.TestCheckResourceAttr("data.ledger_labels.all", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_buckets.active", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_categories.children", "items.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_bucket_members.fixed", "members.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.rent", "category_origin", "user"),
					resource.TestCheckResourceAttrPair("data.ledger_object_organisation.rent", "category_id", "ledger_category.child", "id"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.rent", "label_ids.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.rent", "bucket_ids.#", "1"),
				),
			},
			// An unchanged reapply plans nothing.
			{Config: base, PlanOnly: true},
			importStep("ledger_category.parent"),
			importStep("ledger_category.child"),
			importStep("ledger_label.reviewed"),
			importStep("ledger_bucket.fixed"),
			{ResourceName: "ledger_bucket_member.rent", ImportState: true, ImportStateVerify: true},
			{ResourceName: "ledger_category_assignment.rent", ImportState: true, ImportStateVerify: true},
			{ResourceName: "ledger_label_assignment.rent", ImportState: true, ImportStateVerify: true},
			// Update in place: renames keep the identifier, and the category assignment moves to the parent.
			{
				Config: organisationConfig(commitment, "Checked", "Fixed monthly costs", "Rent and utilities", "parent"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_label.reviewed", "name", "Checked"),
					resource.TestCheckResourceAttr("ledger_label.reviewed", "version", "2"),
					resource.TestCheckResourceAttr("ledger_bucket.fixed", "name", "Fixed monthly costs"),
					resource.TestCheckResourceAttr("ledger_category.child", "name", "Rent and utilities"),
					resource.TestCheckResourceAttrPair("ledger_category_assignment.rent", "category_id", "ledger_category.parent", "id"),
				),
			},
			// Drift: a rename made outside Terraform is shown in the plan, and applying restores the configuration.
			{
				PreConfig: func() {
					labels, err := c.ListDefinitions(ctx, client.Labels, client.DefinitionFilter{})
					if err != nil || len(labels) != 1 {
						t.Fatalf("listing labels: %v (%d)", err, len(labels))
					}
					if _, err := c.UpdateDefinition(ctx, client.Labels, labels[0].ID, labels[0].Version, map[string]any{"name": "Renamed elsewhere"}); err != nil {
						t.Fatal(err)
					}
				},
				Config:             organisationConfig(commitment, "Checked", "Fixed monthly costs", "Rent and utilities", "parent"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: organisationConfig(commitment, "Checked", "Fixed monthly costs", "Rent and utilities", "parent"),
				Check:  resource.TestCheckResourceAttr("ledger_label.reviewed", "name", "Checked"),
			},
			// Drift: a bucket member and a label removed outside Terraform come back on apply.
			{
				PreConfig: func() {
					buckets, err := c.ListDefinitions(ctx, client.Buckets, client.DefinitionFilter{})
					if err != nil || len(buckets) != 1 {
						t.Fatalf("listing buckets: %v", err)
					}
					if err := c.RemoveBucketMember(ctx, buckets[0].ID, client.ObjectCommitment, commitment); err != nil {
						t.Fatal(err)
					}
					labels, _ := c.ListDefinitions(ctx, client.Labels, client.DefinitionFilter{})
					if err := c.RemoveObjectLabel(ctx, client.ObjectCommitment, commitment, labels[0].ID); err != nil {
						t.Fatal(err)
					}
				},
				Config:             organisationConfig(commitment, "Checked", "Fixed monthly costs", "Rent and utilities", "parent"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: organisationConfig(commitment, "Checked", "Fixed monthly costs", "Rent and utilities", "parent"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ledger_bucket_members.fixed", "members.#", "1"),
					resource.TestCheckResourceAttr("data.ledger_object_organisation.rent", "label_ids.#", "1"),
				),
			},
		},
	})
}

// TestArchiveReassignAndDestroy covers the lifecycle rules: archive first, and delete only when unused or reassigned.
func TestArchiveReassignAndDestroy(t *testing.T) {
	c, commitment := orgFixture(t)
	ctx := context.Background()
	cfg := func(archived bool, reassign string) string {
		return fmt.Sprintf(`
resource "ledger_label" "target" {
  name = "Target"
}

resource "ledger_label" "old" {
  name        = "Old"
  archived    = %t
  reassign_to = %s
}
`, archived, reassign)
	}
	targetOnly := `
resource "ledger_label" "target" {
  name = "Target"
}
`
	old := func() client.Definition {
		ls, err := c.ListDefinitions(ctx, client.Labels, client.DefinitionFilter{NamePrefix: "Old"})
		if err != nil || len(ls) != 1 {
			t.Fatalf("listing labels: %v", err)
		}
		return ls[0]
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			ls, err := c.ListDefinitions(ctx, client.Labels, client.DefinitionFilter{})
			if err != nil {
				return err
			}
			if len(ls) != 0 {
				return fmt.Errorf("%d labels left after destroy", len(ls))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg(false, "null"), Check: resource.TestCheckResourceAttr("ledger_label.old", "archived", "false")},
			// Archive in place: the label keeps existing and keeps its assignments.
			{
				PreConfig: func() {
					if _, err := c.AddObjectLabel(ctx, client.ObjectCommitment, commitment, old().ID); err != nil {
						t.Fatal(err)
					}
				},
				Config: cfg(true, "null"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ledger_label.old", "archived", "true"),
					resource.TestCheckResourceAttrSet("ledger_label.old", "archived_at"),
				),
			},
			{Config: cfg(true, "null"), PlanOnly: true},
			// Destroying an assigned label without reassign_to is refused.
			{
				Config:      cfg(true, "null"),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)still assigned`),
			},
			// With reassign_to the assignment moves to the target and the label goes.
			{Config: cfg(true, "ledger_label.target.id"), Check: resource.TestCheckResourceAttrPair("ledger_label.old", "reassign_to", "ledger_label.target", "id")},
			// Removing the resource deletes the label with the reassign_to that state holds: the assignment moves.
			{
				Config: targetOnly,
				Check: func(*terraform.State) error {
					a, err := c.GetObjectOrganisation(ctx, client.ObjectCommitment, commitment)
					if err != nil {
						return err
					}
					if len(a.Labels) != 1 || a.Labels[0].Label.Name != "Target" || a.Labels[0].Origin != "user" {
						return fmt.Errorf("expected the assignment to move to Target as the person's, got %+v", a.Labels)
					}
					return nil
				},
			},
			// Free the target so the final destroy is allowed.
			{
				PreConfig: func() {
					ls, _ := c.ListDefinitions(ctx, client.Labels, client.DefinitionFilter{})
					if err := c.RemoveObjectLabel(ctx, client.ObjectCommitment, commitment, ls[0].ID); err != nil {
						t.Fatal(err)
					}
				},
				Config:   targetOnly,
				PlanOnly: true,
			},
		},
	})
}

// TestArchiveParentAndChildInOneApply documents that a parent category cannot be archived in the same apply as its
// children: Ledger refuses (409, active children) and Terraform orders the parent first. Archiving the child in an
// earlier apply makes the parent's archive succeed.
func TestArchiveParentAndChildInOneApply(t *testing.T) {
	orgFixture(t)
	cfg := func(parentArchived, childArchived bool) string {
		return fmt.Sprintf(`
resource "ledger_category" "parent" {
  name     = "Housing"
  archived = %t
}

resource "ledger_category" "child" {
  name      = "Rent"
  parent_id = ledger_category.parent.id
  archived  = %t
}
`, parentArchived, childArchived)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: cfg(false, false)},
			{Config: cfg(true, true), ExpectError: regexp.MustCompile(`(?s)Archiving the category failed.*409.*active children`)},
			{Config: cfg(false, true), Check: resource.TestCheckResourceAttr("ledger_category.child", "archived", "true")},
			{Config: cfg(true, true), Check: resource.TestCheckResourceAttr("ledger_category.parent", "archived", "true")},
		},
	})
}

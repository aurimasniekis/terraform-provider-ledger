package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOrganisationSchemasAreValid(t *testing.T) {
	ctx := context.Background()
	p := newTestProvider(nil)
	for _, mk := range p.Resources(ctx) {
		r := mk()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "ledger"}, &meta)
		var resp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("%s: %v", meta.TypeName, resp.Diagnostics)
			continue
		}
		if d := resp.Schema.ValidateImplementation(ctx); d.HasError() {
			t.Errorf("%s: %v", meta.TypeName, d)
		}
		if resp.Schema.MarkdownDescription == "" {
			t.Errorf("%s: no description", meta.TypeName)
		}
	}
	for _, mk := range p.DataSources(ctx) {
		d := mk()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "ledger"}, &meta)
		var resp datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &resp)
		if d := resp.Schema.ValidateImplementation(ctx); d.HasError() {
			t.Errorf("%s: %v", meta.TypeName, d)
		}
	}
}

func TestOrganisationTypeNames(t *testing.T) {
	ctx := context.Background()
	p := newTestProvider(nil)
	var res, ds []string
	for _, mk := range p.Resources(ctx) {
		var m resource.MetadataResponse
		mk().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "ledger"}, &m)
		res = append(res, m.TypeName)
	}
	for _, mk := range p.DataSources(ctx) {
		var m datasource.MetadataResponse
		mk().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "ledger"}, &m)
		ds = append(ds, m.TypeName)
	}
	wantRes := "ledger_category ledger_label ledger_bucket ledger_category_assignment ledger_label_assignment ledger_bucket_member ledger_merchant ledger_merchant_alias ledger_commitment ledger_manual_payment ledger_occurrence_status_override"
	wantDS := "ledger_category ledger_label ledger_bucket ledger_categories ledger_labels ledger_buckets ledger_object_organisation ledger_bucket_members ledger_merchant ledger_merchants ledger_commitment ledger_commitments ledger_manual_payment ledger_manual_payments ledger_occurrence_status_override"
	if got := strings.Join(res, " "); got != wantRes {
		t.Errorf("resources %q", got)
	}
	if got := strings.Join(ds, " "); got != wantDS {
		t.Errorf("data sources %q", got)
	}
}

func TestNameValidator(t *testing.T) {
	long := strings.Repeat("é", maxDefinitionNameLength)
	cases := []struct {
		name string
		ok   bool
	}{
		{"Groceries", true}, {"Fixed  costs", false}, {" Fixed", false}, {"Fixed ", false}, {"a\tb", false}, {"", false},
		{long, true}, {long + "x", false}, {"Café au lait", true},
	}
	for _, tc := range cases {
		var resp validator.StringResponse
		nameValidator().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(tc.name)}, &resp)
		if got := !resp.Diagnostics.HasError(); got != tc.ok {
			t.Errorf("%q: valid=%v want %v", tc.name, got, tc.ok)
		}
	}
	var resp validator.StringResponse
	nameValidator().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringUnknown()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Error("an unknown name must pass validation")
	}
}

func TestMerchantNameValidatorRefusesNonNFC(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"Caf\xc3\xa9 Lidl", true},   // composed (NFC)
		{"Cafe\xcc\x81 Lidl", false}, // e + combining acute: Ledger would store the NFC form
		{"Lidl", true},
	}
	for _, tc := range cases {
		var resp validator.StringResponse
		merchantNameValidator().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(tc.name)}, &resp)
		if got := !resp.Diagnostics.HasError(); got != tc.ok {
			t.Errorf("%q: valid=%v want %v", tc.name, got, tc.ok)
		}
	}
}

func TestAliasKey(t *testing.T) {
	for in, want := range map[string]string{"LIDL": "lidl", "Cafe\xcc\x81  Lidl": "caf\xc3\xa9 lidl", " a\tb ": "a b"} {
		if got := aliasKey(in); got != want {
			t.Errorf("aliasKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIDValidator(t *testing.T) {
	for id, ok := range map[string]bool{"01JD3Y4C8V9M2Q7W5R6T0XZBNA": true, "01jd3y4c8v9m2q7w5r6t0xzbna": false, "none": false, "": false} {
		var resp validator.StringResponse
		idValidator().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(id)}, &resp)
		if got := !resp.Diagnostics.HasError(); got != ok {
			t.Errorf("%q: valid=%v want %v", id, got, ok)
		}
	}
}

func TestSplitImportID(t *testing.T) {
	if parts, err := splitImportID("a/b/c", 3, "x"); err != nil || len(parts) != 3 {
		t.Fatal(err)
	}
	if _, err := splitImportID("a/b", 3, "<a>/<b>/<c>"); err == nil || !strings.Contains(err.Error(), "<a>/<b>/<c>") {
		t.Fatalf("err = %v", err)
	}
}

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// maxDefinitionNameLength is the longest category, label or bucket name the Management API accepts (characters).
const maxDefinitionNameLength = 80

var idRegexp = regexp.MustCompile("^[0-7][0-9A-HJKMNP-TV-Z]{25}$")

// idValidator accepts a Ledger identifier (an upper-case ULID).
func idValidator() validator.String {
	return stringvalidator.RegexMatches(idRegexp, "must be a Ledger identifier: 26 upper-case Crockford base32 characters")
}

// clientFromProviderData extracts the configured client, or adds nothing when the provider is not configured yet
// (Terraform calls Configure on resources before the provider has data during validation).
func clientFromProviderData(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. This is a provider bug.", data))
		return nil
	}
	return c
}

// normalisedNameValidator rejects a name the server would change: Ledger trims names and collapses white space, so
// a name that differs after that could never match what it reads back and would show a permanent difference.
type normalisedNameValidator struct{}

func (normalisedNameValidator) Description(context.Context) string {
	return "a name of 1 to 80 characters without leading, trailing or repeated white space"
}

func (v normalisedNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (normalisedNameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if strings.Join(strings.Fields(s), " ") != s {
		resp.Diagnostics.AddAttributeError(req.Path, "Name is not normalised",
			"Ledger trims names and collapses white space, so the name must not start or end with white space or contain "+
				"runs of white space. Write it the way Ledger stores it.")
		return
	}
	if n := utf8.RuneCountInString(s); n < 1 || n > maxDefinitionNameLength {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid name length",
			fmt.Sprintf("The name must be 1 to %d characters, got %d.", maxDefinitionNameLength, n))
	}
}

func nameValidator() validator.String { return normalisedNameValidator{} }

// attrGetter is satisfied by tfsdk.Plan, tfsdk.State and tfsdk.Config.
type attrGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target any) diag.Diagnostics
}

func strOrNull(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// nullableString returns nil for a null or unknown value.
func nullableString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

// splitImportID splits an import identifier made of n parts separated by "/".
func splitImportID(id string, n int, shape string) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		return nil, fmt.Errorf("expected an import identifier of the form %q, got %q", shape, id)
	}
	return parts, nil
}

// objectTypeValidator accepts the object types an assignment can target.
func objectTypeValidator(allowed ...client.ObjectType) validator.String {
	vals := make([]string, len(allowed))
	for i, a := range allowed {
		vals[i] = string(a)
	}
	return stringvalidator.OneOf(vals...)
}

func versionConflictDetail(err error) string {
	return err.Error() + " The object changed outside Terraform since it was last read; run terraform plan (refresh) and apply again."
}

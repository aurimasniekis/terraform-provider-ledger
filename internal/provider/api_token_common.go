package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// apiTokenGrantNote is the access note shared by the token resource and data sources.
const apiTokenGrantNote = "Reading token metadata needs an API token with the `tokens:read` grant; revoking needs `tokens:write`. " +
	"**Creating a token needs the root token of the root account** (the first user, normally created with `ledger token create --create administrator --root` on the Ledger host): " +
	"Ledger refuses `POST /auth/tokens` for any other API token with 403 `forbidden`. Revoking a root token needs the root token too (`root_token_required`)."

// apiTokenReadHint names the grant a read needs when Ledger refuses it with 403. It formats only the API error, which never carries the secret.
func apiTokenReadHint(err error) string {
	if client.IsForbidden(err) {
		return err.Error() + " Reading API tokens needs an API token with the tokens:read grant."
	}
	return err.Error()
}

// grantsSet converts grants to a set value; a root token holds none of its own, so it maps to null.
func grantsSet(t client.APIToken) (types.Set, diag.Diagnostics) {
	if t.Root || len(t.Grants) == 0 {
		return types.SetNull(types.StringType), nil
	}
	return types.SetValueFrom(context.Background(), types.StringType, t.Grants)
}

// apiTokenAttrTypes is the shape of one token in the data sources. There is no secret in it: Ledger never serves it again.
var apiTokenAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "owner_id": types.StringType, "root": types.BoolType,
	"grants": types.SetType{ElemType: types.StringType}, "hint": types.StringType, "prefix": types.StringType, "last_chars": types.StringType,
	"status": types.StringType, "created_at": types.StringType, "expires_at": types.StringType, "last_used_at": types.StringType,
	"revoked_at": types.StringType, "parent_token_id": types.StringType,
}

func apiTokenValue(t client.APIToken) (attr.Value, diag.Diagnostics) {
	g, diags := types.SetValueFrom(context.Background(), types.StringType, nonNilStrings(t.Grants))
	if diags.HasError() {
		return nil, diags
	}
	v, d := types.ObjectValue(apiTokenAttrTypes, map[string]attr.Value{
		"id": types.StringValue(t.ID), "name": types.StringValue(t.Name), "owner_id": types.StringValue(t.OwnerID), "root": types.BoolValue(t.Root),
		"grants": g, "hint": types.StringValue(t.Hint), "prefix": types.StringValue(t.Prefix), "last_chars": types.StringValue(t.LastChars),
		"status": types.StringValue(t.Status), "created_at": types.StringValue(t.CreatedAt), "expires_at": strOrNull(t.ExpiresAt),
		"last_used_at": strOrNull(t.LastUsedAt), "revoked_at": strOrNull(t.RevokedAt), "parent_token_id": strOrNull(t.ParentTokenID),
	})
	return v, append(diags, d...)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

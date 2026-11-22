package provider

import (
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var overrideStatuses = []string{"paid", "unpaid"}

// manualPaymentModel is the Terraform view of a manual payment, shared by the resource and both data sources.
type manualPaymentModel struct {
	ID                types.String `tfsdk:"id"`
	OccurrenceID      types.String `tfsdk:"occurrence_id"`
	Amount            types.String `tfsdk:"amount"`
	Currency          types.String `tfsdk:"currency"`
	PaidOn            types.String `tfsdk:"paid_on"`
	Note              types.String `tfsdk:"note"`
	Version           types.Int64  `tfsdk:"version"`
	ReplacedByMatchID types.String `tfsdk:"replaced_by_match_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

func manualPaymentFromAPI(p client.ManualPayment) manualPaymentModel {
	return manualPaymentModel{
		ID: types.StringValue(p.ID), OccurrenceID: types.StringValue(p.OccurrenceID),
		Amount: types.StringValue(p.Amount.Amount), Currency: types.StringValue(p.Amount.Currency),
		PaidOn: types.StringValue(p.PaidOn), Note: strOrNull(p.Note), Version: types.Int64Value(p.Version),
		ReplacedByMatchID: strOrNull(p.ReplacedByMatchID),
		CreatedAt:         types.StringValue(p.CreatedAt), UpdatedAt: types.StringValue(p.UpdatedAt),
	}
}

func (m manualPaymentModel) input() client.ManualPaymentInput {
	return client.ManualPaymentInput{
		Amount: client.Money{Amount: m.Amount.ValueString(), Currency: m.Currency.ValueString()},
		PaidOn: m.PaidOn.ValueString(), Note: nullableString(m.Note),
	}
}

// validateManualPayment checks what the schema cannot: that paid_on is a real calendar date. The upper bound (not after
// today in Ledger's reporting zone) is Ledger's rule and is reported by it, because the provider does not know the zone.
func validateManualPayment(m manualPaymentModel) diag.Diagnostics {
	var d diag.Diagnostics
	if known(m.PaidOn) {
		t, err := time.Parse(dateLayout, m.PaidOn.ValueString())
		switch {
		case err != nil:
			d.AddAttributeError(path.Root("paid_on"), "Invalid date", "paid_on must be a real calendar date written YYYY-MM-DD.")
		case t.Before(mustDate(minStartDate)):
			d.AddAttributeError(path.Root("paid_on"), "Date too early", fmt.Sprintf("paid_on must not be before %s.", minStartDate))
		}
	}
	return d
}

// statusOverrideModel is the Terraform view of an occurrence's manual status override. A bank fact is never part of it:
// derived_status is what the evidence and dates say before the override, read back for comparison only.
type statusOverrideModel struct {
	ID            types.String `tfsdk:"id"`
	OccurrenceID  types.String `tfsdk:"occurrence_id"`
	Status        types.String `tfsdk:"status"`
	Reason        types.String `tfsdk:"reason"`
	Applied       types.Bool   `tfsdk:"applied"`
	SetAt         types.String `tfsdk:"set_at"`
	DerivedStatus types.String `tfsdk:"derived_status"`
	PaymentStatus types.String `tfsdk:"payment_status"`
}

// statusOverrideFromAPI maps an occurrence with an active override. ok is false when no override is active.
func statusOverrideFromAPI(o client.Occurrence) (m statusOverrideModel, ok bool) {
	ov := o.Payment.Override
	if ov == nil {
		return statusOverrideModel{}, false
	}
	return statusOverrideModel{
		ID: types.StringValue(o.ID), OccurrenceID: types.StringValue(o.ID),
		Status: types.StringValue(ov.Status), Reason: types.StringValue(ov.Reason), Applied: types.BoolValue(ov.Applied),
		SetAt: types.StringValue(ov.SetAt), DerivedStatus: types.StringValue(o.Payment.DerivedStatus),
		PaymentStatus: types.StringValue(o.Payment.Status),
	}, true
}

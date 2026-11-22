package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

func TestValidateManualPayment(t *testing.T) {
	ok := manualPaymentModel{PaidOn: types.StringValue("2026-03-01")}
	if d := validateManualPayment(ok); d.HasError() {
		t.Fatalf("valid date rejected: %v", d)
	}
	for paidOn, want := range map[string]string{"2026-02-30": "Invalid date", "1899-12-31": "Date too early"} {
		d := validateManualPayment(manualPaymentModel{PaidOn: types.StringValue(paidOn)})
		if !d.HasError() || !strings.Contains(d[0].Summary(), want) {
			t.Errorf("%s: %v", paidOn, d)
		}
	}
	if d := validateManualPayment(manualPaymentModel{PaidOn: types.StringUnknown()}); d.HasError() {
		t.Error("an unknown date must not be judged")
	}
}

func TestManualPaymentMappingKeepsAmountScaleAndNulls(t *testing.T) {
	m := manualPaymentFromAPI(client.ManualPayment{ID: "a", OccurrenceID: "b", Amount: client.Money{Amount: "40.500", Currency: "EUR"}, PaidOn: "2026-03-01"})
	if m.Amount.ValueString() != "40.500" || m.Currency.ValueString() != "EUR" || !m.Note.IsNull() || !m.ReplacedByMatchID.IsNull() {
		t.Fatalf("%+v", m)
	}
	in := m.input()
	if in.Amount.Amount != "40.500" || in.Note != nil {
		t.Fatalf("%+v", in)
	}
}

func TestStatusOverrideMapping(t *testing.T) {
	if _, ok := statusOverrideFromAPI(client.Occurrence{ID: "x"}); ok {
		t.Fatal("no override must map to not ok")
	}
	m, ok := statusOverrideFromAPI(client.Occurrence{ID: "x", Payment: client.PaymentState{
		Status: "unpaid", DerivedStatus: "paid", Override: &client.OverrideState{Status: "unpaid", Reason: "r", Applied: true, SetAt: "t"},
	}})
	if !ok || m.Status.ValueString() != "unpaid" || m.DerivedStatus.ValueString() != "paid" || m.ID.ValueString() != "x" {
		t.Fatalf("%+v", m)
	}
}

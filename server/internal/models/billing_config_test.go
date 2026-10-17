package models

import (
	"encoding/json"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
)

// BillingConfig() 解析 billing_config JSON：正常解出 flatFee/freezeEstimate/items。
func TestBillingConfig(t *testing.T) {
	m := ModelConfig{
		BillingConfigRaw: json.RawMessage(`{"flatFee":3,"freezeEstimate":20,"items":[{"unit":"token","qty":"resp:usage.total_tokens","price":{"fixed":0.002}}]}`),
	}
	got := m.BillingConfig()
	want := billing.ModelBilling{
		FlatFee:        3,
		FreezeEstimate: 20,
		Items: []billing.ChargeItem{
			{Unit: "token", Qty: "resp:usage.total_tokens", Price: billing.Price{Fixed: 0.002}},
		},
	}
	if got.FlatFee != want.FlatFee || got.FreezeEstimate != want.FreezeEstimate || len(got.Items) != len(want.Items) {
		t.Fatalf("BillingConfig() = %+v, want %+v", got, want)
	}
	if len(got.Items) == 1 {
		gi, wi := got.Items[0], want.Items[0]
		if gi.Unit != wi.Unit || gi.Qty != wi.Qty || gi.Price.Fixed != wi.Price.Fixed {
			t.Fatalf("BillingConfig() item = %+v, want %+v", gi, wi)
		}
	}
}

// 空 billing_config → 零值（flatFee=0，无项），不 panic。
func TestBillingConfigEmpty(t *testing.T) {
	m := ModelConfig{}
	got := m.BillingConfig()
	if got.FlatFee != 0 || len(got.Items) != 0 {
		t.Fatalf("empty BillingConfig() = %+v, want zero", got)
	}
}

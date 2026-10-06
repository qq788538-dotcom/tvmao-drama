package skillruntime

import (
	"encoding/json"
	"testing"
)

func TestDecodeAndRedactTraceJSONOnlyRedactsCredentialFields(t *testing.T) {
	value := decodeAndRedactTraceJSON([]byte(`{
		"sourceText":"business plaintext",
		"nested":{"api_key":"sk-secret","password":"p-secret"}
	}`))
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["sourceText"] != "business plaintext" {
		t.Fatalf("business plaintext was changed: %#v", decoded)
	}
	nested := decoded["nested"].(map[string]any)
	if nested["api_key"] != "[REDACTED]" || nested["password"] != "[REDACTED]" {
		t.Fatalf("credential fields were not redacted: %#v", nested)
	}
}

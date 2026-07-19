package milvus

import (
	"encoding/json"
	"testing"
)

func TestStringArrayAcceptsMilvusFieldDataEnvelope(t *testing.T) {
	var value stringArray
	payload := `{"Data":{"StringData":{"data":["tenant_a","tenant_b"]}}}`
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		t.Fatal(err)
	}
	if len(value) != 2 || value[0] != "tenant_a" {
		t.Fatalf("unexpected value: %#v", value)
	}
}

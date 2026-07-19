package milvus

import "testing"

func TestBuildFilterDefaultsToActiveAndAllowsTenantPublicData(t *testing.T) {
	filter := buildFilter(Query{Tenant: "tenant_a", Product: "identity"})
	want := `(tenant_id == "public" or tenant_id == "tenant_a") and product == "identity" and status == "active"`
	if filter != want {
		t.Fatalf("filter=%q want=%q", filter, want)
	}
}

func TestBuildFilterEscapesUntrustedValues(t *testing.T) {
	filter := buildFilter(Query{Tenant: `bad" or status == "deprecated`})
	if filter != `(tenant_id == "public" or tenant_id == "bad\" or status == \"deprecated") and status == "active"` {
		t.Fatalf("filter was not escaped: %q", filter)
	}
}

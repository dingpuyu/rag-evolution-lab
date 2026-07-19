package milvus

import "testing"

func TestBuildFilterDefaultsToActiveAndAllowsTenantPublicData(t *testing.T) {
	filter := buildFilter(Query{Tenant: "tenant_a", Role: "admin", Product: "identity"})
	want := `(visibility == "public" or (array_contains(allowed_tenants, "tenant_a") and array_contains(allowed_roles, "admin"))) and product == "identity" and status == "active"`
	if filter != want {
		t.Fatalf("filter=%q want=%q", filter, want)
	}
}

func TestBuildFilterEscapesUntrustedValues(t *testing.T) {
	filter := buildFilter(Query{Tenant: `bad" or status == "deprecated`, Role: "admin"})
	if filter != `(visibility == "public" or (array_contains(allowed_tenants, "bad\" or status == \"deprecated") and array_contains(allowed_roles, "admin"))) and status == "active"` {
		t.Fatalf("filter was not escaped: %q", filter)
	}
}

func TestBuildFilterFailsClosedWithoutRole(t *testing.T) {
	filter := buildFilter(Query{Tenant: "tenant_a"})
	if filter != `visibility == "public" and status == "active"` {
		t.Fatalf("filter should expose only public data: %q", filter)
	}
}

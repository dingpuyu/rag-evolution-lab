package milvus

import "testing"

func TestExactIdentifierBoostRewardsLiteralModelAndErrorCode(t *testing.T) {
	hits := []SearchHit{
		{ChunkID: "semantic", Content: "通用网络故障说明", Distance: 0},
		{ChunkID: "exact", Content: "VSM-100 软件 2.6 的 SYS-NET-042 处理步骤", Distance: 1},
	}
	applyExactIdentifierBoost("VSM-100 2.6 出现 SYS-NET-042", hits)
	if hits[0].ChunkID != "exact" || len(hits[0].ExactMatches) < 2 {
		t.Fatalf("literal identifiers were not boosted: %#v", hits)
	}
	if hits[0].RecallSources[len(hits[0].RecallSources)-1] != "exact_identifier" {
		t.Fatalf("exact recall source missing: %#v", hits[0])
	}
}

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

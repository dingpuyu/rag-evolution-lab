package datasetaccess

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
)

func TestPostgresQueryTraceRoundTripIsTenantScoped(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RAGLAB_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("set RAGLAB_TEST_POSTGRES_URL to run PostgreSQL query trace integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := auth.Identity{Subject: "trace-test-a", TenantID: "tenant_a", Roles: []string{"admin"}}
	record := querytrace.Record{
		TraceID: "gw_trace_integration_" + time.Now().UTC().Format("20060102150405.000000000"), AppID: "tenant_a-support-agent",
		EnvironmentID: "tenant_a-support-agent-dev", TenantID: identity.TenantID, Subject: identity.Subject,
		Query: "如何处理单点登录？", RewrittenQuery: "如何处理单点登录？\nsso", Status: "completed",
		IndexVersion: "v1", IndexCollection: "raglab_lifecycle_v1", TopK: 5, CandidateCount: 12, HitCount: 5,
		RerankApplied: true, RewriteApplied: true, Metadata: map[string]any{"stage": "gateway"}, StartedAt: time.Now().UTC(),
	}
	answerable := true
	record.Answerable = &answerable
	if err := store.UpsertQueryTrace(ctx, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetQueryTrace(ctx, identity, record.AppID, record.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TraceID != record.TraceID || loaded.RewrittenQuery != record.RewrittenQuery || !loaded.RerankApplied || loaded.Metadata["stage"] != "gateway" {
		t.Fatalf("trace did not round-trip: %#v", loaded)
	}
	if _, err := store.GetQueryTrace(ctx, auth.Identity{Subject: "trace-test-b", TenantID: "tenant_b", Roles: []string{"admin"}}, record.AppID, record.TraceID); err != querytrace.ErrDenied {
		t.Fatalf("cross-tenant trace access error=%v", err)
	}
}

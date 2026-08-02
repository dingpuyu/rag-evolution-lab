package datasetaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

func TestPostgresTenantDatasetLifecycleAndRevocation(t *testing.T) {
	databaseURL := os.Getenv("RAGLAB_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("RAGLAB_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := fmt.Sprint(time.Now().UnixNano())
	tenantA, tenantB := "itest_a_"+suffix, "itest_b_"+suffix
	alice := auth.Identity{Subject: "alice_" + suffix, TenantID: tenantA, Roles: []string{"admin"}}
	bob := auth.Identity{Subject: "bob_" + suffix, TenantID: tenantB, Roles: []string{"admin"}}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM control_plane_audit WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM datasets WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM memberships WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM users WHERE subject IN ($1,$2)`, alice.Subject, bob.Subject)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	}()

	if err := store.ProvisionIdentity(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := store.ProvisionIdentity(ctx, bob); err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(ctx, alice, CreateDataset{
		Name: "Alice Private Runbook", Slug: "private-runbook",
		Description: "integration fixture", Visibility: "tenant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(ctx, created.ID, alice); err != nil {
		t.Fatalf("owner authorization failed: %v", err)
	}
	scopedStatus, err := store.Status(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if scopedStatus.Tenants != 1 || scopedStatus.Users != 1 || scopedStatus.Memberships != 1 {
		t.Fatalf("tenant admin received unscoped control-plane counts: %#v", scopedStatus)
	}
	if _, err := store.Authorize(ctx, created.ID, bob); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("cross-tenant authorization error=%v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE memberships SET status='revoked', updated_at=now()
		WHERE tenant_id=$1 AND subject=$2`, tenantA, alice.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(ctx, created.ID, alice); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("revoked membership authorization error=%v", err)
	}
	var auditCount int
	if err := store.db.QueryRowContext(ctx, `
		SELECT count(*) FROM control_plane_audit
		WHERE tenant_id=$1 AND resource_id=$2`, tenantA, created.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("dataset mutation audit count=%d", auditCount)
	}
}

// Security regression: a signed identity is not, by itself, a membership
// grant. Before the control-plane hardening, EnsureIdentity inserted a new
// admin membership on every request, so a never-invited subject could claim a
// tenant in its token and immediately see that tenant's datasets.
func TestPostgresUnprovisionedIdentityCannotSelfAssignTenantAccess(t *testing.T) {
	databaseURL := os.Getenv("RAGLAB_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("RAGLAB_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	identity := auth.Identity{Subject: "uninvited_" + fmt.Sprint(time.Now().UnixNano()), TenantID: "tenant_a", Roles: []string{"admin"}}
	if _, err := store.Visible(ctx, identity); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("unprovisioned identity access error=%v, want ErrDatasetDenied", err)
	}
}

// Security regression: removing admin from the current token must not leave
// an old database role usable. The control plane must require the DB binding
// and the current signed role to agree.
func TestPostgresRoleDowngradeCannotReuseStaleAdminMembership(t *testing.T) {
	databaseURL := os.Getenv("RAGLAB_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("RAGLAB_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := fmt.Sprint(time.Now().UnixNano())
	identity := auth.Identity{Subject: "downgrade_" + suffix, TenantID: "tenant_a", Roles: []string{"admin"}}
	if err := store.ProvisionIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM memberships WHERE tenant_id=$1 AND subject=$2`, identity.TenantID, identity.Subject)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM users WHERE subject=$1`, identity.Subject)
	}()

	if _, err := store.Visible(ctx, identity); err != nil {
		t.Fatalf("provisioned admin should be visible: %v", err)
	}
	downgraded := identity
	downgraded.Roles = []string{"viewer"}
	if _, err := store.Visible(ctx, downgraded); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("downgraded identity access error=%v, want ErrDatasetDenied", err)
	}
}

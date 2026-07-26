package datasetaccess

import (
	"context"
	"errors"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

func TestCatalogHidesCrossTenantDatasets(t *testing.T) {
	catalog := Defaults()
	alice := auth.Identity{TenantID: "tenant_a", Roles: []string{"admin"}}
	visible, err := catalog.Visible(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, dataset := range visible {
		if dataset.ID == "tenant-b-operations" {
			t.Fatal("tenant A saw tenant B dataset")
		}
	}
	if _, err := catalog.Authorize(context.Background(), "tenant-a-operations", alice); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Authorize(context.Background(), "tenant-b-operations", alice); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("cross-tenant authorization error=%v", err)
	}
}

func TestCatalogRequiresDatasetRole(t *testing.T) {
	catalog := Defaults()
	viewer := auth.Identity{TenantID: "tenant_a", Roles: []string{"viewer"}}
	if _, err := catalog.Authorize(context.Background(), "tenant-a-operations", viewer); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("viewer authorization error=%v", err)
	}
	if _, err := catalog.Authorize(context.Background(), "public-identity", viewer); err != nil {
		t.Fatal(err)
	}
}

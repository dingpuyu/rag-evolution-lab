package datasetaccess

import (
	"errors"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

func TestCatalogHidesCrossTenantDatasets(t *testing.T) {
	catalog := Defaults()
	alice := auth.Identity{TenantID: "tenant_a", Roles: []string{"admin"}}
	visible := catalog.Visible(alice)
	for _, dataset := range visible {
		if dataset.ID == "tenant-b-operations" {
			t.Fatal("tenant A saw tenant B dataset")
		}
	}
	if _, err := catalog.Authorize("tenant-a-operations", alice); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Authorize("tenant-b-operations", alice); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("cross-tenant authorization error=%v", err)
	}
}

func TestCatalogRequiresDatasetRole(t *testing.T) {
	catalog := Defaults()
	viewer := auth.Identity{TenantID: "tenant_a", Roles: []string{"viewer"}}
	if _, err := catalog.Authorize("tenant-a-operations", viewer); !errors.Is(err, ErrDatasetDenied) {
		t.Fatalf("viewer authorization error=%v", err)
	}
	if _, err := catalog.Authorize("public-identity", viewer); err != nil {
		t.Fatal(err)
	}
}

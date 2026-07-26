package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAccountRegistrationLoginAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := NewAccountStore(path)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := store.Register(Registration{
		Email: "Alice@Example.com", Password: "correct horse battery", Organization: "Acme Beijing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if registered.TenantID == "" || registered.PrimaryRole() != "admin" {
		t.Fatalf("unexpected registered identity %#v", registered)
	}
	loggedIn, err := store.Authenticate("alice@example.com", "correct horse battery")
	if err != nil || loggedIn.Subject != registered.Subject || loggedIn.TenantID != registered.TenantID {
		t.Fatalf("unexpected login identity=%#v err=%v", loggedIn, err)
	}
	reloaded, err := NewAccountStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Authenticate("alice@example.com", "wrong password"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password error=%v", err)
	}
	if _, err := reloaded.Authenticate("alice@example.com", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("account file permissions=%o", info.Mode().Perm())
	}
}

func TestRegistrationDoesNotAllowRoleOrTenantSelection(t *testing.T) {
	store, err := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Register(Registration{
		Email: "alice@example.com", Password: "a sufficiently long password", Organization: "Shared Name",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Register(Registration{
		Email: "bob@example.com", Password: "another long password", Organization: "Shared Name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.TenantID == second.TenantID {
		t.Fatal("self-registration must create isolated tenants instead of joining by organization name")
	}
	if _, err := store.Register(Registration{
		Email: "alice@example.com", Password: "a sufficiently long password", Organization: "Other",
	}); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("duplicate registration error=%v", err)
	}
}

func TestEnsureDemoCanProvisionPlatformAdministrator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := NewAccountStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDemo(
		"admin@raglab.local", "RagLab-Platform-2026!", "platform", []string{"platform_admin"},
	); err != nil {
		t.Fatal(err)
	}
	identity, err := store.Authenticate("admin@raglab.local", "RagLab-Platform-2026!")
	if err != nil {
		t.Fatal(err)
	}
	if identity.TenantID != "platform" || !identity.HasRole("platform_admin") || identity.HasRole("admin") {
		t.Fatalf("unexpected platform identity %#v", identity)
	}

	reloaded, err := NewAccountStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.EnsureDemo(
		"admin@raglab.local", "a different ignored password", "tenant_evil", []string{"viewer"},
	); err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Authenticate("admin@raglab.local", "RagLab-Platform-2026!")
	if err != nil || persisted.TenantID != "platform" || !persisted.HasRole("platform_admin") {
		t.Fatalf("idempotent provisioning changed administrator: identity=%#v err=%v", persisted, err)
	}
}

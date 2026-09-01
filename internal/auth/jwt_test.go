package auth

import (
	"strings"
	"testing"
	"time"
)

func TestJWTVerifiesTrustedIdentityAndRejectsTampering(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	manager, err := NewManager(Config{
		Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "raglab-api",
		TTL: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(Identity{Subject: "alice", TenantID: "tenant_037", Roles: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.VerifyAuthorization("Bearer " + token)
	if err != nil || identity.Subject != "alice" || identity.TenantID != "tenant_037" || !identity.HasRole("admin") {
		t.Fatalf("unexpected identity=%#v err=%v", identity, err)
	}
	tampered := token[:len(token)-1] + map[bool]string{true: "a", false: "b"}[strings.HasSuffix(token, "a")]
	if _, err := manager.VerifyAuthorization("Bearer " + tampered); err == nil {
		t.Fatal("tampered token must be rejected")
	}
}

func TestJWTRejectsExpiredAndWrongAudience(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	issuer, err := NewManager(Config{
		Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "wrong-api",
		TTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.Issue(Identity{Subject: "alice", TenantID: "tenant_037", Roles: []string{"viewer"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewManager(Config{
		Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "raglab-api",
		Now: func() time.Time { return now.Add(2 * time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAuthorization("Bearer " + token); err == nil {
		t.Fatal("wrong audience or expired token must be rejected")
	}
}

func TestJWTPreservesApplicationScope(t *testing.T) {
	manager, err := NewManager(Config{Secret: []byte("01234567890123456789012345678901"), Issuer: "raglab", Audience: "api"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.Issue(Identity{Subject: "app:demo", TenantID: "tenant_a", Roles: []string{"viewer"}, ApplicationID: "tenant_a-support-agent", Scopes: []string{"rag:query"}})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.VerifyAuthorization("Bearer " + token)
	if err != nil || identity.ApplicationID != "tenant_a-support-agent" || !identity.HasScope("rag:query") || identity.HasScope("rag:answer") {
		t.Fatalf("unexpected scoped identity=%#v err=%v", identity, err)
	}
}

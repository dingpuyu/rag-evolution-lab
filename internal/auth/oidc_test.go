package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type rotatingJWKS struct {
	mu        sync.Mutex
	key       *rsa.PrivateKey
	kid       string
	discovery int
	jwks      int
}

func (state *rotatingJWKS) set(key *rsa.PrivateKey, kid string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.key = key
	state.kid = kid
}

func (state *rotatingJWKS) counts() (int, int) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.discovery, state.jwks
}

func TestOIDCDiscoveryCacheAndKeyRotation(t *testing.T) {
	key1 := mustRSAKey(t)
	key2 := mustRSAKey(t)
	state := &rotatingJWKS{key: key1, kid: "key-1"}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			state.discovery++
			writeTestJSON(t, writer, map[string]any{"issuer": server.URL, "jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			state.jwks++
			writeTestJSON(t, writer, map[string]any{"keys": []any{testJWK(&state.key.PublicKey, state.kid)}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	verifier, err := NewOIDCVerifier(OIDCConfig{
		Issuer: server.URL, Audience: "raglab-api", CacheTTL: time.Hour,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token1 := signRS256(t, key1, "key-1", map[string]any{
		"sub": "alice", "tenant_id": "tenant_037", "roles": []string{"viewer"},
		"realm_access": map[string]any{"roles": []string{"admin", "viewer"}},
		"iss":          server.URL, "aud": []string{"account", "raglab-api"},
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	for range 2 {
		identity, verifyErr := verifier.VerifyAuthorization("Bearer " + token1)
		if verifyErr != nil {
			t.Fatal(verifyErr)
		}
		if identity.Subject != "alice" || identity.TenantID != "tenant_037" || !identity.HasRole("admin") || len(identity.Roles) != 2 {
			t.Fatalf("unexpected identity: %#v", identity)
		}
	}
	discoveryCalls, jwksCalls := state.counts()
	if discoveryCalls != 1 || jwksCalls != 1 {
		t.Fatalf("cached verification fetched discovery=%d jwks=%d", discoveryCalls, jwksCalls)
	}

	state.set(key2, "key-2")
	token2 := signRS256(t, key2, "key-2", map[string]any{
		"sub": "bob", "tenant_id": "tenant_038", "roles": []string{"viewer"},
		"iss": server.URL, "aud": "raglab-api", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if _, err := verifier.VerifyAuthorization("Bearer " + token2); err != nil {
		t.Fatalf("rotated key must be discovered after unknown kid: %v", err)
	}
	discoveryCalls, jwksCalls = state.counts()
	if discoveryCalls != 1 || jwksCalls != 2 {
		t.Fatalf("rotation should reuse discovery and refresh JWKS: discovery=%d jwks=%d", discoveryCalls, jwksCalls)
	}

	unknownToken := signRS256(t, key1, "attacker-kid", map[string]any{
		"sub": "mallory", "tenant_id": "tenant_evil", "roles": []string{"admin"},
		"iss": server.URL, "aud": "raglab-api", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if _, err := verifier.VerifyAuthorization("Bearer " + unknownToken); err == nil {
		t.Fatal("unknown kid must fail closed")
	}
	_, jwksCallsAfterUnknown := state.counts()
	if jwksCallsAfterUnknown != jwksCalls {
		t.Fatalf("unknown-kid flood protection should suppress immediate refresh: before=%d after=%d", jwksCalls, jwksCallsAfterUnknown)
	}
}

func TestOIDCRejectsWrongClaimsAlgorithmAndUnavailableProvider(t *testing.T) {
	key := mustRSAKey(t)
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/jwks" {
			writeTestJSON(t, writer, map[string]any{"keys": []any{testJWK(&key.PublicKey, "key-1")}})
			return
		}
		http.NotFound(writer, request)
	}))
	issuer := server.URL
	verifier, err := NewOIDCVerifier(OIDCConfig{
		Issuer: issuer, Audience: "raglab-api", JWKSURL: server.URL + "/jwks",
		ClockSkew: time.Second, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"sub": "alice", "tenant_id": "tenant_037", "roles": []string{"viewer"},
		"iss": issuer, "aud": "wrong-api", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	if _, err := verifier.VerifyAuthorization("Bearer " + signRS256(t, key, "key-1", base)); err == nil {
		t.Fatal("wrong audience must be rejected")
	}
	base["aud"] = "raglab-api"
	base["exp"] = now.Add(-2 * time.Second).Unix()
	if _, err := verifier.VerifyAuthorization("Bearer " + signRS256(t, key, "key-1", base)); err == nil {
		t.Fatal("expired token must be rejected")
	}
	algNone := encodeJSONSegment(t, map[string]any{"alg": "none", "kid": "key-1"}) + "." +
		encodeJSONSegment(t, base) + "."
	if _, err := verifier.VerifyAuthorization("Bearer " + algNone); err == nil {
		t.Fatal("algorithm confusion must be rejected")
	}
	server.Close()
	base["exp"] = now.Add(time.Hour).Unix()
	if _, err := verifier.VerifyAuthorization("Bearer " + signRS256(t, key, "key-unknown", base)); err == nil {
		t.Fatal("unavailable JWKS provider must fail closed")
	}
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	unsigned := encodeJSONSegment(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}) + "." + encodeJSONSegment(t, claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodeJSONSegment(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func testJWK(key *rsa.PublicKey, kid string) map[string]any {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func TestProviderURLRequiresHTTPSOutsideLoopback(t *testing.T) {
	if _, err := NewOIDCVerifier(OIDCConfig{Issuer: "http://id.example.com", Audience: "api"}); err == nil {
		t.Fatal("non-loopback HTTP issuer must be rejected")
	}
	if _, err := NewOIDCVerifier(OIDCConfig{Issuer: "https://id.example.com", Audience: "api"}); err != nil {
		t.Fatal(err)
	}
}

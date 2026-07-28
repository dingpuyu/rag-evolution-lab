package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxOIDCResponseBytes = 1 << 20

type OIDCConfig struct {
	Issuer                string
	Audience              string
	JWKSURL               string
	HTTPClient            *http.Client
	CacheTTL              time.Duration
	ClockSkew             time.Duration
	ForcedRefreshInterval time.Duration
	Now                   func() time.Time
}

// OIDCVerifier validates RS256 access tokens against an OIDC provider's JWKS.
// Keys are cached and an unknown kid triggers a rate-limited refresh so normal
// provider key rotation does not require restarting the RAG service.
type OIDCVerifier struct {
	config OIDCConfig

	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	keysExpireAt      time.Time
	discoveredJWKSURL string
	lastForcedRefresh time.Time
}

type oidcTokenClaims struct {
	Subject         string        `json:"sub"`
	TenantID        string        `json:"tenant_id"`
	ApplicationID   string        `json:"app_id"`
	ClientID        string        `json:"client_id"`
	AuthorizedParty string        `json:"azp"`
	Scope           scopeClaim    `json:"scope"`
	SCP             []string      `json:"scp"`
	Roles           []string      `json:"roles"`
	RealmAccess     realmAccess   `json:"realm_access"`
	Issuer          string        `json:"iss"`
	Audience        audienceClaim `json:"aud"`
	IssuedAt        int64         `json:"iat"`
	NotBefore       int64         `json:"nbf"`
	Expires         int64         `json:"exp"`
}

type realmAccess struct {
	Roles []string `json:"roles"`
}

type scopeClaim []string

func (claim *scopeClaim) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*claim = strings.Fields(value)
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("scope must be a string or string array")
	}
	*claim = scopeClaim(values)
	return nil
}

type audienceClaim []string

func (claim *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*claim = audienceClaim{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return fmt.Errorf("aud must be a string or string array")
	}
	*claim = audienceClaim(multiple)
	return nil
}

func NewOIDCVerifier(config OIDCConfig) (*OIDCVerifier, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.Audience = strings.TrimSpace(config.Audience)
	config.JWKSURL = strings.TrimSpace(config.JWKSURL)
	if config.Issuer == "" || config.Audience == "" {
		return nil, fmt.Errorf("OIDC issuer and audience are required")
	}
	if err := validateProviderURL(config.Issuer); err != nil {
		return nil, fmt.Errorf("invalid OIDC issuer: %w", err)
	}
	if config.JWKSURL != "" {
		if err := validateProviderURL(config.JWKSURL); err != nil {
			return nil, fmt.Errorf("invalid JWKS URL: %w", err)
		}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if config.CacheTTL <= 0 {
		config.CacheTTL = 15 * time.Minute
	}
	if config.ClockSkew < 0 {
		return nil, fmt.Errorf("OIDC clock skew must not be negative")
	}
	if config.ClockSkew == 0 {
		config.ClockSkew = time.Minute
	}
	if config.ForcedRefreshInterval < 0 {
		return nil, fmt.Errorf("OIDC forced refresh interval must not be negative")
	}
	if config.ForcedRefreshInterval == 0 {
		config.ForcedRefreshInterval = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &OIDCVerifier{config: config, keys: make(map[string]*rsa.PublicKey)}, nil
}

// Warmup validates discovery and loads the first JWKS before serving traffic.
// Production startup can therefore fail closed instead of waiting for a request.
func (verifier *OIDCVerifier) Warmup(ctx context.Context) error {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	if err := verifier.refreshKeysLocked(ctx, verifier.config.Now()); err != nil {
		return fmt.Errorf("warm up OIDC verifier: %w", err)
	}
	return nil
}

func (verifier *OIDCVerifier) VerifyAuthorization(value string) (Identity, error) {
	token, err := bearerToken(value)
	if err != nil {
		return Identity{}, err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, fmt.Errorf("malformed JWT")
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, fmt.Errorf("decode JWT header: %w", err)
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerData, &header); err != nil {
		return Identity{}, fmt.Errorf("decode JWT header: %w", err)
	}
	if header.Algorithm != "RS256" {
		return Identity{}, fmt.Errorf("JWT algorithm must be RS256")
	}
	if strings.TrimSpace(header.KeyID) == "" {
		return Identity{}, fmt.Errorf("JWT kid is required")
	}
	if header.Type != "" && !strings.EqualFold(header.Type, "JWT") && !strings.EqualFold(header.Type, "at+jwt") {
		return Identity{}, fmt.Errorf("unsupported JWT typ")
	}

	key, err := verifier.keyFor(context.Background(), header.KeyID)
	if err != nil {
		return Identity{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, fmt.Errorf("decode JWT signature: %w", err)
	}
	unsigned := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(unsigned))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Identity{}, fmt.Errorf("invalid JWT signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, fmt.Errorf("decode JWT claims: %w", err)
	}
	var claims oidcTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Identity{}, fmt.Errorf("decode JWT claims: %w", err)
	}
	return verifier.validateClaims(claims)
}

func (verifier *OIDCVerifier) validateClaims(claims oidcTokenClaims) (Identity, error) {
	now := verifier.config.Now().UTC().Unix()
	skew := int64(verifier.config.ClockSkew / time.Second)
	if claims.Expires == 0 || claims.Expires <= now-skew {
		return Identity{}, fmt.Errorf("JWT expired")
	}
	if claims.NotBefore > 0 && claims.NotBefore > now+skew {
		return Identity{}, fmt.Errorf("JWT is not active")
	}
	if claims.IssuedAt > now+skew {
		return Identity{}, fmt.Errorf("JWT issued in the future")
	}
	if claims.Issuer != verifier.config.Issuer || !claims.Audience.contains(verifier.config.Audience) {
		return Identity{}, fmt.Errorf("JWT issuer or audience mismatch")
	}
	roles := uniqueNonEmpty(append(append([]string(nil), claims.Roles...), claims.RealmAccess.Roles...))
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.TenantID) == "" || len(roles) == 0 {
		return Identity{}, fmt.Errorf("JWT identity claims are incomplete")
	}
	return Identity{
		Subject: claims.Subject, TenantID: claims.TenantID, Roles: roles,
		Issuer: claims.Issuer, Audience: verifier.config.Audience, Expires: claims.Expires,
		ApplicationID: firstNonEmpty(claims.ApplicationID, claims.ClientID, claims.AuthorizedParty),
		Scopes:        uniqueNonEmpty(append(append([]string(nil), claims.Scope...), claims.SCP...)),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (verifier *OIDCVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()

	now := verifier.config.Now()
	if now.Before(verifier.keysExpireAt) {
		if key := verifier.keys[kid]; key != nil {
			return key, nil
		}
		if !verifier.lastForcedRefresh.IsZero() && now.Sub(verifier.lastForcedRefresh) < verifier.config.ForcedRefreshInterval {
			return nil, fmt.Errorf("unknown JWT kid")
		}
		verifier.lastForcedRefresh = now
	}
	if err := verifier.refreshKeysLocked(ctx, now); err != nil {
		return nil, fmt.Errorf("refresh OIDC keys: %w", err)
	}
	key := verifier.keys[kid]
	if key == nil {
		return nil, fmt.Errorf("unknown JWT kid")
	}
	return key, nil
}

func (verifier *OIDCVerifier) refreshKeysLocked(ctx context.Context, now time.Time) error {
	jwksURL := verifier.config.JWKSURL
	if jwksURL == "" {
		var err error
		jwksURL, err = verifier.discoverJWKSLocked(ctx)
		if err != nil {
			return err
		}
	}
	var document struct {
		Keys []struct {
			KeyType   string `json:"kty"`
			KeyID     string `json:"kid"`
			Use       string `json:"use"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := verifier.fetchJSON(ctx, jwksURL, &document); err != nil {
		return err
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, item := range document.Keys {
		if item.KeyType != "RSA" || item.KeyID == "" || (item.Use != "" && item.Use != "sig") || (item.Algorithm != "" && item.Algorithm != "RS256") {
			continue
		}
		key, err := rsaKey(item.Modulus, item.Exponent)
		if err != nil {
			continue
		}
		keys[item.KeyID] = key
	}
	if len(keys) == 0 {
		return fmt.Errorf("JWKS contains no usable RS256 keys")
	}
	verifier.keys = keys
	verifier.keysExpireAt = now.Add(verifier.config.CacheTTL)
	return nil
}

func (verifier *OIDCVerifier) discoverJWKSLocked(ctx context.Context) (string, error) {
	if verifier.discoveredJWKSURL != "" {
		return verifier.discoveredJWKSURL, nil
	}
	var document struct {
		Issuer  string `json:"issuer"`
		JWKSURL string `json:"jwks_uri"`
	}
	discoveryURL := strings.TrimSuffix(verifier.config.Issuer, "/") + "/.well-known/openid-configuration"
	if err := verifier.fetchJSON(ctx, discoveryURL, &document); err != nil {
		return "", fmt.Errorf("OIDC discovery: %w", err)
	}
	if document.Issuer != verifier.config.Issuer {
		return "", fmt.Errorf("OIDC discovery issuer mismatch")
	}
	if err := validateProviderURL(document.JWKSURL); err != nil {
		return "", fmt.Errorf("OIDC discovery returned invalid jwks_uri: %w", err)
	}
	verifier.discoveredJWKSURL = document.JWKSURL
	return document.JWKSURL, nil
}

func (verifier *OIDCVerifier) fetchJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := verifier.config.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", endpoint, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", endpoint, err)
	}
	if len(data) > maxOIDCResponseBytes {
		return fmt.Errorf("%s response exceeds %d bytes", endpoint, maxOIDCResponseBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s returned multiple JSON values", endpoint)
		}
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, fmt.Errorf("invalid RSA exponent")
	}
	exponentValue := 0
	for _, value := range eBytes {
		exponentValue = exponentValue<<8 | int(value)
	}
	n := new(big.Int).SetBytes(nBytes)
	if n.BitLen() < 2048 || exponentValue < 3 || exponentValue%2 == 0 {
		return nil, fmt.Errorf("unsafe RSA public key")
	}
	return &rsa.PublicKey{N: n, E: exponentValue}, nil
}

func validateProviderURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("absolute URL is required")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("userinfo and fragments are not allowed")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("HTTPS is required except for loopback development")
}

func (claim audienceClaim) contains(expected string) bool {
	for _, candidate := range claim {
		if candidate == expected {
			return true
		}
	}
	return false
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

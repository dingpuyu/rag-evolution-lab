package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Identity struct {
	Subject       string   `json:"subject"`
	TenantID      string   `json:"tenant_id"`
	Roles         []string `json:"roles"`
	Issuer        string   `json:"issuer"`
	Audience      string   `json:"audience"`
	ApplicationID string   `json:"application_id,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	Expires       int64    `json:"expires_at"`
}

// Verifier is the trust boundary between HTTP authentication and an identity
// provider. Both the local HMAC lab and enterprise OIDC implementations satisfy it.
type Verifier interface {
	VerifyAuthorization(value string) (Identity, error)
}

func (identity Identity) HasRole(role string) bool {
	for _, candidate := range identity.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func (identity Identity) HasScope(scope string) bool {
	for _, candidate := range identity.Scopes {
		if candidate == scope || candidate == "*" {
			return true
		}
	}
	return false
}

func (identity Identity) PrimaryRole() string {
	for _, role := range []string{"platform_admin", "admin", "viewer"} {
		if identity.HasRole(role) {
			return role
		}
	}
	return ""
}

type Config struct {
	Secret   []byte
	Issuer   string
	Audience string
	TTL      time.Duration
	Now      func() time.Time
}

type Manager struct {
	config Config
}

type tokenClaims struct {
	Subject       string   `json:"sub"`
	TenantID      string   `json:"tenant_id"`
	Roles         []string `json:"roles"`
	Issuer        string   `json:"iss"`
	Audience      string   `json:"aud"`
	ApplicationID string   `json:"app_id,omitempty"`
	Scopes        []string `json:"scope,omitempty"`
	IssuedAt      int64    `json:"iat"`
	Expires       int64    `json:"exp"`
}

func NewManager(config Config) (*Manager, error) {
	if len(config.Secret) < 32 {
		return nil, fmt.Errorf("JWT HMAC secret must contain at least 32 bytes")
	}
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("JWT issuer and audience are required")
	}
	if config.TTL <= 0 {
		config.TTL = time.Hour
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Manager{config: config}, nil
}

func (manager *Manager) Issue(identity Identity) (string, error) {
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.TenantID) == "" || len(identity.Roles) == 0 {
		return "", fmt.Errorf("subject, tenant_id and at least one role are required")
	}
	now := manager.config.Now().UTC()
	claims := tokenClaims{
		Subject: identity.Subject, TenantID: identity.TenantID, Roles: append([]string(nil), identity.Roles...),
		Issuer: manager.config.Issuer, Audience: manager.config.Audience,
		ApplicationID: identity.ApplicationID, Scopes: append([]string(nil), identity.Scopes...),
		IssuedAt: now.Unix(), Expires: now.Add(manager.config.TTL).Unix(),
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode JWT claims: %w", err)
	}
	unsigned := encodeSegment(header) + "." + encodeSegment(payload)
	return unsigned + "." + encodeSegment(manager.sign(unsigned)), nil
}

func (manager *Manager) VerifyAuthorization(value string) (Identity, error) {
	token, err := bearerToken(value)
	if err != nil {
		return Identity{}, err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, fmt.Errorf("malformed JWT")
	}
	unsigned := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, manager.sign(unsigned)) {
		return Identity{}, fmt.Errorf("invalid JWT signature")
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, fmt.Errorf("decode JWT header: %w", err)
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	if json.Unmarshal(headerData, &header) != nil || header.Algorithm != "HS256" {
		return Identity{}, fmt.Errorf("JWT algorithm must be HS256")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, fmt.Errorf("decode JWT claims: %w", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Identity{}, fmt.Errorf("decode JWT claims: %w", err)
	}
	now := manager.config.Now().UTC().Unix()
	if claims.Expires <= now {
		return Identity{}, fmt.Errorf("JWT expired")
	}
	if claims.IssuedAt > now+60 {
		return Identity{}, fmt.Errorf("JWT issued in the future")
	}
	if claims.Issuer != manager.config.Issuer || claims.Audience != manager.config.Audience {
		return Identity{}, fmt.Errorf("JWT issuer or audience mismatch")
	}
	if claims.Subject == "" || claims.TenantID == "" || len(claims.Roles) == 0 {
		return Identity{}, fmt.Errorf("JWT identity claims are incomplete")
	}
	return Identity{
		Subject: claims.Subject, TenantID: claims.TenantID, Roles: append([]string(nil), claims.Roles...),
		Issuer: claims.Issuer, Audience: claims.Audience, Expires: claims.Expires,
		ApplicationID: claims.ApplicationID, Scopes: append([]string(nil), claims.Scopes...),
	}, nil
}

func bearerToken(value string) (string, error) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("missing Bearer token")
	}
	if strings.ContainsAny(strings.TrimSpace(token), " \t\r\n") {
		return "", fmt.Errorf("malformed Bearer token")
	}
	return strings.TrimSpace(token), nil
}

func (manager *Manager) sign(unsigned string) []byte {
	mac := hmac.New(sha256.New, manager.config.Secret)
	_, _ = mac.Write([]byte(unsigned))
	return mac.Sum(nil)
}

func encodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

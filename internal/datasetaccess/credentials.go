package datasetaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

type ApplicationCredential struct {
	CredentialID  string     `json:"credential_id"`
	ApplicationID string     `json:"app_id"`
	Name          string     `json:"name"`
	Scopes        []string   `json:"scopes"`
	Status        string     `json:"status"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}

type CredentialStore interface {
	auth.CredentialVerifier
	CreateApplicationCredential(context.Context, auth.Identity, string, string, []string, *time.Time) (ApplicationCredential, string, error)
	ListApplicationCredentials(context.Context, auth.Identity, string) ([]ApplicationCredential, error)
	RevokeApplicationCredential(context.Context, auth.Identity, string, string) error
}

func credentialHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (store *PostgresStore) CreateApplicationCredential(ctx context.Context, identity auth.Identity, appID, name string, scopes []string, expiresAt *time.Time) (ApplicationCredential, string, error) {
	application, err := store.authorizeApplication(ctx, identity, appID)
	if err != nil {
		return ApplicationCredential{}, "", err
	}
	if strings.TrimSpace(name) == "" {
		return ApplicationCredential{}, "", fmt.Errorf("credential name is required")
	}
	if len(scopes) == 0 {
		scopes = []string{"rag:query", "rag:answer"}
	}
	secretBytes := make([]byte, 24)
	if _, err := rand.Read(secretBytes); err != nil {
		return ApplicationCredential{}, "", err
	}
	secret := "ragc_" + hex.EncodeToString(secretBytes)
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return ApplicationCredential{}, "", err
	}
	credential := ApplicationCredential{CredentialID: "cred_" + hex.EncodeToString(idBytes), ApplicationID: application.ID,
		Name: strings.TrimSpace(name), Scopes: uniqueStrings(scopes), Status: "active", CreatedBy: identity.Subject, CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt}
	scopeJSON, _ := json.Marshal(credential.Scopes)
	_, err = store.db.ExecContext(ctx, `INSERT INTO application_credentials (credential_id,app_id,tenant_id,name,secret_hash,scopes,status,created_by,expires_at) VALUES ($1,$2,$3,$4,$5,$6,'active',$7,$8)`, credential.CredentialID, application.ID, application.TenantID, credential.Name, credentialHash(secret), scopeJSON, identity.Subject, expiresAt)
	if err != nil {
		return ApplicationCredential{}, "", err
	}
	return credential, secret, nil
}

func (store *PostgresStore) ListApplicationCredentials(ctx context.Context, identity auth.Identity, appID string) ([]ApplicationCredential, error) {
	application, err := store.authorizeApplication(ctx, identity, appID)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT credential_id,app_id,name,scopes,status,created_by,created_at,expires_at,last_used_at FROM application_credentials WHERE app_id=$1 ORDER BY created_at DESC`, application.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := make([]ApplicationCredential, 0)
	for rows.Next() {
		var item ApplicationCredential
		var scopes []byte
		var expires, last sql.NullTime
		if err := rows.Scan(&item.CredentialID, &item.ApplicationID, &item.Name, &scopes, &item.Status, &item.CreatedBy, &item.CreatedAt, &expires, &last); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(scopes, &item.Scopes)
		if expires.Valid {
			item.ExpiresAt = &expires.Time
		}
		if last.Valid {
			item.LastUsedAt = &last.Time
		}
		credentials = append(credentials, item)
	}
	return credentials, rows.Err()
}

func (store *PostgresStore) RevokeApplicationCredential(ctx context.Context, identity auth.Identity, appID, credentialID string) error {
	application, err := store.authorizeApplication(ctx, identity, appID)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE application_credentials SET status='revoked' WHERE credential_id=$1 AND app_id=$2`, credentialID, application.ID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrDatasetNotFound
	}
	return nil
}

func (store *PostgresStore) VerifyApplicationCredential(ctx context.Context, secret string) (auth.Identity, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return auth.Identity{}, fmt.Errorf("application credential is empty")
	}
	var identity auth.Identity
	var appStatus, tenantStatus string
	var scopesJSON []byte
	var expires sql.NullTime
	err := store.db.QueryRowContext(ctx, `SELECT c.app_id,c.tenant_id,c.scopes,c.expires_at,a.status,t.status FROM application_credentials c JOIN applications a ON a.app_id=c.app_id JOIN tenants t ON t.id=c.tenant_id WHERE c.secret_hash=$1 AND c.status='active'`, credentialHash(secret)).Scan(&identity.ApplicationID, &identity.TenantID, &scopesJSON, &expires, &appStatus, &tenantStatus)
	if err == sql.ErrNoRows {
		return auth.Identity{}, fmt.Errorf("invalid application credential")
	}
	if err != nil {
		return auth.Identity{}, err
	}
	if appStatus != "active" || tenantStatus != "active" {
		return auth.Identity{}, fmt.Errorf("application or tenant is inactive")
	}
	if expires.Valid && time.Now().After(expires.Time) {
		return auth.Identity{}, fmt.Errorf("application credential expired")
	}
	_ = json.Unmarshal(scopesJSON, &identity.Scopes)
	identity.Subject = "app:" + identity.ApplicationID
	identity.Roles = []string{"viewer"}
	identity.Expires = time.Now().Add(time.Hour).Unix()
	_, _ = store.db.ExecContext(ctx, `UPDATE application_credentials SET last_used_at=now() WHERE secret_hash=$1`, credentialHash(secret))
	return identity, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	return result
}

var _ CredentialStore = (*PostgresStore)(nil)

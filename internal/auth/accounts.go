package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	passwordIterations = 120_000
	passwordKeyBytes   = 32
)

var (
	ErrAccountExists     = errors.New("account already exists")
	ErrInvalidCredential = errors.New("invalid email or password")
	emailPattern         = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

type Account struct {
	Email        string    `json:"email"`
	Subject      string    `json:"subject"`
	TenantID     string    `json:"tenant_id"`
	Roles        []string  `json:"roles"`
	PasswordSalt string    `json:"password_salt"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

type Registration struct {
	Email        string
	Password     string
	Organization string
}

// AccountStore exists only for the local lab. Production authentication is
// delegated to an OIDC identity provider and never reads this file.
type AccountStore struct {
	mu       sync.RWMutex
	path     string
	accounts map[string]Account
}

func NewAccountStore(path string) (*AccountStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("account store path is required")
	}
	store := &AccountStore{path: path, accounts: make(map[string]Account)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read account store: %w", err)
	}
	var accounts []Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("decode account store: %w", err)
	}
	for _, account := range accounts {
		store.accounts[normalizeEmail(account.Email)] = account
	}
	return store, nil
}

func (store *AccountStore) Register(input Registration) (Identity, error) {
	email := normalizeEmail(input.Email)
	if !emailPattern.MatchString(email) {
		return Identity{}, fmt.Errorf("a valid email is required")
	}
	if len(input.Password) < 12 {
		return Identity{}, fmt.Errorf("password must contain at least 12 characters")
	}
	organization := strings.TrimSpace(input.Organization)
	if organization == "" {
		return Identity{}, fmt.Errorf("organization is required")
	}
	subject := stableID("usr", email)
	tenantID := stableID("tenant", email+"\x00"+organization)
	return store.create(Account{
		Email: email, Subject: subject, TenantID: tenantID, Roles: []string{"admin"},
	}, input.Password)
}

func (store *AccountStore) EnsureDemo(email, password, tenantID string, roles []string) error {
	email = normalizeEmail(email)
	store.mu.RLock()
	_, exists := store.accounts[email]
	store.mu.RUnlock()
	if exists {
		return nil
	}
	_, err := store.create(Account{
		Email: email, Subject: stableID("demo", email), TenantID: strings.TrimSpace(tenantID),
		Roles: append([]string(nil), roles...),
	}, password)
	if errors.Is(err, ErrAccountExists) {
		return nil
	}
	return err
}

func (store *AccountStore) Authenticate(email, password string) (Identity, error) {
	store.mu.RLock()
	account, ok := store.accounts[normalizeEmail(email)]
	store.mu.RUnlock()
	if !ok {
		// Perform equivalent work so an unknown email is not a cheap timing oracle.
		_ = derivePassword([]byte(password), make([]byte, 16), passwordIterations)
		return Identity{}, ErrInvalidCredential
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(account.PasswordSalt)
	expected, hashErr := base64.RawStdEncoding.DecodeString(account.PasswordHash)
	if saltErr != nil || hashErr != nil {
		return Identity{}, ErrInvalidCredential
	}
	actual := derivePassword([]byte(password), salt, passwordIterations)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return Identity{}, ErrInvalidCredential
	}
	return Identity{
		Subject: account.Subject, TenantID: account.TenantID, Roles: append([]string(nil), account.Roles...),
	}, nil
}

func (store *AccountStore) create(account Account, password string) (Identity, error) {
	if strings.TrimSpace(account.TenantID) == "" || len(account.Roles) == 0 {
		return Identity{}, fmt.Errorf("tenant and roles are required")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Identity{}, fmt.Errorf("generate password salt: %w", err)
	}
	account.PasswordSalt = base64.RawStdEncoding.EncodeToString(salt)
	account.PasswordHash = base64.RawStdEncoding.EncodeToString(derivePassword([]byte(password), salt, passwordIterations))
	account.CreatedAt = time.Now().UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.accounts[account.Email]; exists {
		return Identity{}, ErrAccountExists
	}
	store.accounts[account.Email] = account
	if err := store.persistLocked(); err != nil {
		delete(store.accounts, account.Email)
		return Identity{}, err
	}
	return Identity{
		Subject: account.Subject, TenantID: account.TenantID, Roles: append([]string(nil), account.Roles...),
	}, nil
}

func (store *AccountStore) persistLocked() error {
	accounts := make([]Account, 0, len(store.accounts))
	for _, account := range store.accounts {
		accounts = append(accounts, account)
	}
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("encode account store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return fmt.Errorf("create account directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(store.path), ".accounts-*.json")
	if err != nil {
		return fmt.Errorf("create account temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("protect account file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write account file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync account file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close account file: %w", err)
	}
	if err := os.Rename(tempPath, store.path); err != nil {
		return fmt.Errorf("commit account file: %w", err)
	}
	return nil
}

func derivePassword(password, salt []byte, iterations int) []byte {
	block := make([]byte, 4)
	block[3] = 1
	mac := hmac.New(sha256.New, password)
	_, _ = mac.Write(salt)
	_, _ = mac.Write(block)
	sum := mac.Sum(nil)
	result := append([]byte(nil), sum...)
	for index := 1; index < iterations; index++ {
		mac = hmac.New(sha256.New, password)
		_, _ = mac.Write(sum)
		sum = mac.Sum(nil)
		for offset := range result {
			result[offset] ^= sum[offset]
		}
	}
	return result[:passwordKeyBytes]
}

func stableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + fmt.Sprintf("%x", sum[:8])
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

package datasetaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

var indexCollectionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// Application is an independently deployable Agent product. It is deliberately
// separate from Dataset so one knowledge base can be reused by many products.
type Application struct {
	ID          string    `json:"app_id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Environment struct {
	ID            string    `json:"environment_id"`
	ApplicationID string    `json:"app_id"`
	Name          string    `json:"name"`
	ConfigVersion string    `json:"config_version"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type RetrievalPolicy struct {
	TopK          int  `json:"top_k"`
	CandidateK    int  `json:"candidate_k"`
	Rerank        bool `json:"rerank"`
	QueryRewrite  bool `json:"query_rewrite"`
	TokenBudget   int  `json:"token_budget"`
	AllowFallback bool `json:"allow_fallback"`
}

type KnowledgeBinding struct {
	ApplicationID string          `json:"app_id"`
	EnvironmentID string          `json:"environment_id"`
	DatasetID     string          `json:"dataset_id"`
	DatasetName   string          `json:"dataset_name,omitempty"`
	Purpose       string          `json:"purpose,omitempty"`
	Priority      int             `json:"priority"`
	Status        string          `json:"status"`
	Policy        RetrievalPolicy `json:"policy"`
	CreatedBy     string          `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type CreateApplication struct {
	TenantID    string `json:"tenant_id,omitempty"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

type CreateEnvironment struct {
	Name          string `json:"name"`
	ConfigVersion string `json:"config_version,omitempty"`
}

type CreateBinding struct {
	EnvironmentID string          `json:"environment_id"`
	DatasetID     string          `json:"dataset_id"`
	Purpose       string          `json:"purpose,omitempty"`
	Priority      int             `json:"priority,omitempty"`
	Policy        RetrievalPolicy `json:"policy,omitempty"`
}

type ApplicationStore interface {
	VisibleApplications(context.Context, auth.Identity) ([]Application, error)
	CreateApplication(context.Context, auth.Identity, CreateApplication) (Application, error)
	Environments(context.Context, auth.Identity, string) ([]Environment, error)
	CreateEnvironment(context.Context, auth.Identity, string, CreateEnvironment) (Environment, error)
	Bindings(context.Context, auth.Identity, string, string) ([]KnowledgeBinding, error)
	CreateBinding(context.Context, auth.Identity, string, CreateBinding) (KnowledgeBinding, error)
}

func (store *PostgresStore) VisibleApplications(ctx context.Context, identity auth.Identity) ([]Application, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT app_id, tenant_id, name, slug, description, status, created_by, created_at
		FROM applications
		WHERE status = 'active' AND ($1 OR tenant_id = $2)
		ORDER BY tenant_id, app_id`, identity.HasRole("platform_admin"), identity.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer rows.Close()
	applications := make([]Application, 0)
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.TenantID, &application.Name, &application.Slug,
			&application.Description, &application.Status, &application.CreatedBy, &application.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		applications = append(applications, application)
	}
	return applications, rows.Err()
}

func (store *PostgresStore) CreateApplication(ctx context.Context, identity auth.Identity, input CreateApplication) (Application, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return Application{}, err
	}
	name, slug := strings.TrimSpace(input.Name), strings.TrimSpace(strings.ToLower(input.Slug))
	if name == "" || len(name) > 120 {
		return Application{}, fmt.Errorf("application name is required and must not exceed 120 characters")
	}
	if !datasetSlugPattern.MatchString(slug) || len(slug) > 48 {
		return Application{}, fmt.Errorf("application slug must contain lowercase letters, numbers and single hyphens")
	}
	tenantID := strings.TrimSpace(input.TenantID)
	if !identity.HasRole("platform_admin") {
		tenantID = identity.TenantID
	} else if tenantID == "" {
		tenantID = identity.TenantID
	}
	if tenantID == "" {
		return Application{}, fmt.Errorf("application tenant_id is required")
	}
	var tenantStatus string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM tenants WHERE id = $1`, tenantID).Scan(&tenantStatus); err != nil {
		if err == sql.ErrNoRows {
			return Application{}, ErrDatasetNotFound
		}
		return Application{}, fmt.Errorf("check application tenant: %w", err)
	}
	if tenantStatus != "active" {
		return Application{}, fmt.Errorf("application tenant is not active")
	}
	application := Application{
		ID: tenantID + "-" + slug, TenantID: tenantID, Name: name, Slug: slug,
		Description: strings.TrimSpace(input.Description), Status: "active", CreatedBy: identity.Subject,
	}
	environment := Environment{ID: application.ID + "-dev", ApplicationID: application.ID, Name: "dev", ConfigVersion: "v1", Status: "active"}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Application{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO applications (app_id, tenant_id, name, slug, description, status, created_by)
		VALUES ($1,$2,$3,$4,$5,'active',$6)`, application.ID, application.TenantID, application.Name,
		application.Slug, application.Description, application.CreatedBy); err != nil {
		return Application{}, fmt.Errorf("create application: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO app_environments (environment_id, app_id, name, config_version, status)
		VALUES ($1,$2,$3,$4,'active')`, environment.ID, environment.ApplicationID, environment.Name, environment.ConfigVersion); err != nil {
		return Application{}, fmt.Errorf("create default application environment: %w", err)
	}
	after, _ := json.Marshal(application)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO control_plane_audit (actor_subject, tenant_id, action, resource_type, resource_id, after_state)
		VALUES ($1,$2,'application.create','application',$3,$4)`, identity.Subject, identity.TenantID, application.ID, after); err != nil {
		return Application{}, fmt.Errorf("audit application creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Application{}, err
	}
	if err := store.db.QueryRowContext(ctx, `SELECT created_at FROM applications WHERE app_id = $1`, application.ID).Scan(&application.CreatedAt); err != nil {
		return Application{}, err
	}
	return application, nil
}

func (store *PostgresStore) Environments(ctx context.Context, identity auth.Identity, applicationID string) ([]Environment, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT environment_id, app_id, name, config_version, status, created_at
		FROM app_environments WHERE app_id = $1 ORDER BY name`, application.ID)
	if err != nil {
		return nil, fmt.Errorf("list application environments: %w", err)
	}
	defer rows.Close()
	environments := make([]Environment, 0)
	for rows.Next() {
		var environment Environment
		if err := rows.Scan(&environment.ID, &environment.ApplicationID, &environment.Name, &environment.ConfigVersion,
			&environment.Status, &environment.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan application environment: %w", err)
		}
		environments = append(environments, environment)
	}
	return environments, rows.Err()
}

func (store *PostgresStore) CreateEnvironment(ctx context.Context, identity auth.Identity, applicationID string, input CreateEnvironment) (Environment, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return Environment{}, err
	}
	name := strings.TrimSpace(strings.ToLower(input.Name))
	if !datasetSlugPattern.MatchString(name) || len(name) > 32 {
		return Environment{}, fmt.Errorf("environment name must contain lowercase letters, numbers and single hyphens")
	}
	configVersion := strings.TrimSpace(input.ConfigVersion)
	if configVersion == "" {
		configVersion = "v1"
	}
	environment := Environment{ID: application.ID + "-" + name, ApplicationID: application.ID, Name: name, ConfigVersion: configVersion, Status: "active"}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO app_environments (environment_id, app_id, name, config_version, status)
		VALUES ($1,$2,$3,$4,'active')`, environment.ID, environment.ApplicationID, environment.Name, environment.ConfigVersion); err != nil {
		return Environment{}, fmt.Errorf("create application environment: %w", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT created_at FROM app_environments WHERE environment_id = $1`, environment.ID).Scan(&environment.CreatedAt); err != nil {
		return Environment{}, err
	}
	return environment, nil
}

func (store *PostgresStore) Bindings(ctx context.Context, identity auth.Identity, applicationID, environmentID string) ([]KnowledgeBinding, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT b.app_id, b.environment_id, b.dataset_id, d.name, b.purpose, b.priority, b.status,
		       b.policy, b.created_by, b.created_at
		FROM knowledge_bindings b JOIN datasets d ON d.id = b.dataset_id
		WHERE b.app_id = $1`
	args := []any{application.ID}
	if strings.TrimSpace(environmentID) != "" {
		query += ` AND b.environment_id = $2`
		args = append(args, strings.TrimSpace(environmentID))
	}
	query += ` ORDER BY b.priority DESC, b.dataset_id`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list knowledge bindings: %w", err)
	}
	defer rows.Close()
	bindings := make([]KnowledgeBinding, 0)
	for rows.Next() {
		var binding KnowledgeBinding
		var policyJSON []byte
		if err := rows.Scan(&binding.ApplicationID, &binding.EnvironmentID, &binding.DatasetID, &binding.DatasetName,
			&binding.Purpose, &binding.Priority, &binding.Status, &policyJSON, &binding.CreatedBy, &binding.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan knowledge binding: %w", err)
		}
		if len(policyJSON) > 0 && string(policyJSON) != "null" {
			if err := json.Unmarshal(policyJSON, &binding.Policy); err != nil {
				return nil, fmt.Errorf("decode knowledge binding policy: %w", err)
			}
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (store *PostgresStore) CreateBinding(ctx context.Context, identity auth.Identity, applicationID string, input CreateBinding) (KnowledgeBinding, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" || strings.TrimSpace(input.DatasetID) == "" {
		return KnowledgeBinding{}, fmt.Errorf("environment_id and dataset_id are required")
	}
	var environmentStatus string
	if err := store.db.QueryRowContext(ctx, `
		SELECT status FROM app_environments WHERE environment_id = $1 AND app_id = $2`, environmentID, application.ID).Scan(&environmentStatus); err != nil {
		if err == sql.ErrNoRows {
			return KnowledgeBinding{}, ErrDatasetNotFound
		}
		return KnowledgeBinding{}, fmt.Errorf("check application environment: %w", err)
	}
	if environmentStatus != "active" {
		return KnowledgeBinding{}, fmt.Errorf("application environment is not active")
	}
	var datasetTenant, datasetVisibility, datasetStatus, datasetName string
	if err := store.db.QueryRowContext(ctx, `
		SELECT tenant_id, visibility, status, name FROM datasets WHERE id = $1`, strings.TrimSpace(input.DatasetID)).Scan(
		&datasetTenant, &datasetVisibility, &datasetStatus, &datasetName); err != nil {
		if err == sql.ErrNoRows {
			return KnowledgeBinding{}, ErrDatasetNotFound
		}
		return KnowledgeBinding{}, fmt.Errorf("check binding dataset: %w", err)
	}
	if datasetStatus != "active" || (!identity.HasRole("platform_admin") && datasetVisibility != "public" && datasetTenant != application.TenantID) {
		return KnowledgeBinding{}, ErrDatasetDenied
	}
	policy := normalizeRetrievalPolicy(input.Policy)
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	binding := KnowledgeBinding{
		ApplicationID: application.ID, EnvironmentID: environmentID, DatasetID: strings.TrimSpace(input.DatasetID),
		DatasetName: datasetName, Purpose: strings.TrimSpace(input.Purpose), Priority: input.Priority,
		Status: "active", Policy: policy, CreatedBy: identity.Subject,
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO knowledge_bindings (app_id, environment_id, dataset_id, purpose, priority, status, policy, created_by)
		VALUES ($1,$2,$3,$4,$5,'active',$6,$7)
		ON CONFLICT (app_id, environment_id, dataset_id) DO UPDATE SET
			purpose=EXCLUDED.purpose, priority=EXCLUDED.priority, status='active', policy=EXCLUDED.policy,
			created_by=EXCLUDED.created_by, updated_at=now()`, binding.ApplicationID, binding.EnvironmentID, binding.DatasetID,
		binding.Purpose, binding.Priority, policyJSON, binding.CreatedBy)
	if err != nil {
		return KnowledgeBinding{}, fmt.Errorf("create knowledge binding: %w", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT created_at FROM knowledge_bindings WHERE app_id=$1 AND environment_id=$2 AND dataset_id=$3`,
		binding.ApplicationID, binding.EnvironmentID, binding.DatasetID).Scan(&binding.CreatedAt); err != nil {
		return KnowledgeBinding{}, err
	}
	return binding, nil
}

func (store *PostgresStore) authorizeApplication(ctx context.Context, identity auth.Identity, applicationID string) (Application, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return Application{}, err
	}
	var application Application
	err := store.db.QueryRowContext(ctx, `
		SELECT app_id, tenant_id, name, slug, description, status, created_by, created_at
		FROM applications WHERE app_id = $1`, strings.TrimSpace(applicationID)).Scan(
		&application.ID, &application.TenantID, &application.Name, &application.Slug, &application.Description,
		&application.Status, &application.CreatedBy, &application.CreatedAt)
	if err == sql.ErrNoRows {
		return Application{}, ErrDatasetNotFound
	}
	if err != nil {
		return Application{}, err
	}
	if application.Status != "active" || (!identity.HasRole("platform_admin") && application.TenantID != identity.TenantID) {
		return Application{}, ErrDatasetDenied
	}
	return application, nil
}

func normalizeRetrievalPolicy(policy RetrievalPolicy) RetrievalPolicy {
	if policy.TopK <= 0 {
		policy.TopK = 5
	}
	if policy.CandidateK <= 0 {
		policy.CandidateK = 20
	}
	if policy.CandidateK < policy.TopK {
		policy.CandidateK = policy.TopK
	}
	if policy.TokenBudget <= 0 {
		policy.TokenBudget = 4_000
	}
	return policy
}

var _ ApplicationStore = (*PostgresStore)(nil)

var _ IndexStore = (*PostgresStore)(nil)

func (store *PostgresStore) VisibleIndexReleases(ctx context.Context, identity auth.Identity, applicationID, environmentID string) ([]IndexRelease, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT release_id, app_id, environment_id, version, collection, alias, state, published_by, published_at, created_at
		FROM index_releases WHERE app_id=$1 AND environment_id=$2 ORDER BY published_at DESC`, application.ID, strings.TrimSpace(environmentID))
	if err != nil {
		return nil, fmt.Errorf("list index releases: %w", err)
	}
	defer rows.Close()
	var releases []IndexRelease
	for rows.Next() {
		var release IndexRelease
		if err := rows.Scan(&release.ReleaseID, &release.ApplicationID, &release.EnvironmentID, &release.Version,
			&release.Collection, &release.Alias, &release.State, &release.PublishedBy, &release.PublishedAt, &release.CreatedAt); err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, rows.Err()
}

func (store *PostgresStore) PublishIndexRelease(ctx context.Context, identity auth.Identity, applicationID string, input PublishIndex) (IndexRelease, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return IndexRelease{}, err
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	version := strings.TrimSpace(input.Version)
	collection := strings.TrimSpace(input.Collection)
	if environmentID == "" || version == "" || collection == "" {
		return IndexRelease{}, fmt.Errorf("environment_id, version and collection are required")
	}
	if !datasetSlugPattern.MatchString(version) || len(version) > 64 || !indexCollectionPattern.MatchString(collection) {
		return IndexRelease{}, fmt.Errorf("version must contain lowercase letters, numbers and single hyphens; collection must contain lowercase letters, numbers, underscores or hyphens")
	}
	var environmentStatus string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM app_environments WHERE environment_id=$1 AND app_id=$2`, environmentID, application.ID).Scan(&environmentStatus); err != nil {
		if err == sql.ErrNoRows {
			return IndexRelease{}, ErrDatasetNotFound
		}
		return IndexRelease{}, err
	}
	if environmentStatus != "active" {
		return IndexRelease{}, fmt.Errorf("application environment is not active")
	}
	environmentName := strings.TrimPrefix(environmentID, application.ID+"-")
	if environmentName == "" {
		environmentName = environmentID
	}
	release := IndexRelease{
		ReleaseID:     application.ID + "-" + environmentName + "-" + version,
		ApplicationID: application.ID, EnvironmentID: environmentID, Version: version, Collection: collection,
		Alias: strings.TrimSpace(input.Alias), State: "published", PublishedBy: identity.Subject,
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return IndexRelease{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE index_releases SET state='superseded' WHERE app_id=$1 AND environment_id=$2 AND state='published'`, application.ID, environmentID); err != nil {
		return IndexRelease{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO index_releases (release_id, app_id, environment_id, version, collection, alias, state, published_by)
		VALUES ($1,$2,$3,$4,$5,$6,'published',$7)
		ON CONFLICT (app_id, environment_id, version) DO UPDATE SET collection=EXCLUDED.collection, alias=EXCLUDED.alias,
			state='published', published_by=EXCLUDED.published_by, published_at=now()`, release.ReleaseID, release.ApplicationID,
		release.EnvironmentID, release.Version, release.Collection, release.Alias, release.PublishedBy); err != nil {
		return IndexRelease{}, fmt.Errorf("persist index release: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return IndexRelease{}, err
	}
	err = store.db.QueryRowContext(ctx, `SELECT published_at, created_at FROM index_releases WHERE release_id=$1`, release.ReleaseID).Scan(&release.PublishedAt, &release.CreatedAt)
	return release, err
}

func (store *PostgresStore) RollbackIndexRelease(ctx context.Context, identity auth.Identity, applicationID, environmentID, releaseID string) (IndexRelease, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return IndexRelease{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return IndexRelease{}, err
	}
	defer tx.Rollback()
	var release IndexRelease
	if err := tx.QueryRowContext(ctx, `SELECT release_id, app_id, environment_id, version, collection, alias, state, published_by, published_at, created_at FROM index_releases WHERE release_id=$1 AND app_id=$2 AND environment_id=$3`, releaseID, application.ID, strings.TrimSpace(environmentID)).Scan(
		&release.ReleaseID, &release.ApplicationID, &release.EnvironmentID, &release.Version, &release.Collection, &release.Alias, &release.State, &release.PublishedBy, &release.PublishedAt, &release.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return IndexRelease{}, ErrDatasetNotFound
		}
		return IndexRelease{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE index_releases SET state='superseded' WHERE app_id=$1 AND environment_id=$2 AND state='published'`, application.ID, environmentID); err != nil {
		return IndexRelease{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE index_releases SET state='published', published_by=$1, published_at=now() WHERE release_id=$2`, identity.Subject, releaseID); err != nil {
		return IndexRelease{}, err
	}
	if err := tx.Commit(); err != nil {
		return IndexRelease{}, err
	}
	if err := store.db.QueryRowContext(ctx, `SELECT state, published_by, published_at FROM index_releases WHERE release_id=$1`, releaseID).Scan(&release.State, &release.PublishedBy, &release.PublishedAt); err != nil {
		return IndexRelease{}, err
	}
	return release, nil
}

func (store *PostgresStore) ResolveIndexRelease(ctx context.Context, identity auth.Identity, applicationID, environmentID string) (IndexRelease, error) {
	application, err := store.authorizeApplication(ctx, identity, applicationID)
	if err != nil {
		return IndexRelease{}, err
	}
	var release IndexRelease
	err = store.db.QueryRowContext(ctx, `
		SELECT release_id, app_id, environment_id, version, collection, alias, state, published_by, published_at, created_at
		FROM index_releases WHERE app_id=$1 AND environment_id=$2 AND state='published' ORDER BY published_at DESC LIMIT 1`, application.ID, strings.TrimSpace(environmentID)).Scan(
		&release.ReleaseID, &release.ApplicationID, &release.EnvironmentID, &release.Version, &release.Collection, &release.Alias,
		&release.State, &release.PublishedBy, &release.PublishedAt, &release.CreatedAt)
	if err == sql.ErrNoRows {
		return IndexRelease{}, nil
	}
	return release, err
}

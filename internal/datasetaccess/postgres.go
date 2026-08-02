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
	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var datasetSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type PostgresStore struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("open control-plane database: %w", err)
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect control-plane database: %w", err)
	}
	store := &PostgresStore{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.seed(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *PostgresStore) Close() error { return store.db.Close() }

func (store *PostgresStore) UpsertQueryTrace(ctx context.Context, record querytrace.Record) error {
	metadata, err := json.Marshal(record.Metadata)
	if err != nil {
		return fmt.Errorf("encode query trace metadata: %w", err)
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO query_traces (
			trace_id, app_id, environment_id, tenant_id, subject, query, rewritten_query, status,
			index_version, index_collection, embedding_model, generator, model, prompt_version,
			top_k, candidate_count, hit_count, rerank_applied, rewrite_applied, answerable,
			refusal_reason, embedding_ms, retrieval_ms, generation_ms, total_ms, prompt_tokens,
			output_tokens, trace_parent, span_id, provider, input_cost_usd, output_cost_usd, total_cost_usd,
			error, metadata, started_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37)
		ON CONFLICT (trace_id) DO UPDATE SET
			status=EXCLUDED.status, rewritten_query=EXCLUDED.rewritten_query,
			index_version=EXCLUDED.index_version, index_collection=EXCLUDED.index_collection,
			embedding_model=EXCLUDED.embedding_model, generator=EXCLUDED.generator,
			model=EXCLUDED.model, prompt_version=EXCLUDED.prompt_version,
			top_k=EXCLUDED.top_k, candidate_count=EXCLUDED.candidate_count, hit_count=EXCLUDED.hit_count,
			rerank_applied=EXCLUDED.rerank_applied, rewrite_applied=EXCLUDED.rewrite_applied,
			answerable=EXCLUDED.answerable, refusal_reason=EXCLUDED.refusal_reason,
			embedding_ms=EXCLUDED.embedding_ms, retrieval_ms=EXCLUDED.retrieval_ms,
			generation_ms=EXCLUDED.generation_ms, total_ms=EXCLUDED.total_ms,
			prompt_tokens=EXCLUDED.prompt_tokens, output_tokens=EXCLUDED.output_tokens,
			trace_parent=EXCLUDED.trace_parent, span_id=EXCLUDED.span_id, provider=EXCLUDED.provider,
			input_cost_usd=EXCLUDED.input_cost_usd, output_cost_usd=EXCLUDED.output_cost_usd,
			total_cost_usd=EXCLUDED.total_cost_usd,
			error=EXCLUDED.error, metadata=EXCLUDED.metadata, completed_at=EXCLUDED.completed_at`,
		record.TraceID, record.AppID, record.EnvironmentID, record.TenantID, record.Subject,
		record.Query, record.RewrittenQuery, record.Status, record.IndexVersion, record.IndexCollection,
		record.EmbeddingModel, record.Generator, record.Model, record.PromptVersion, record.TopK,
		record.CandidateCount, record.HitCount, record.RerankApplied, record.RewriteApplied, record.Answerable,
		record.RefusalReason, record.EmbeddingMS, record.RetrievalMS, record.GenerationMS, record.TotalMS,
		record.PromptTokens, record.OutputTokens, record.TraceParent, record.SpanID, record.Provider,
		record.InputCostUSD, record.OutputCostUSD, record.TotalCostUSD, record.Error, metadata, record.StartedAt, record.CompletedAt)
	return err
}

func (store *PostgresStore) GetQueryTrace(ctx context.Context, identity auth.Identity, appID, traceID string) (querytrace.Record, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return querytrace.Record{}, err
	}
	var record querytrace.Record
	var answerable sql.NullBool
	var completedAt sql.NullTime
	var metadata []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT trace_id, app_id, environment_id, tenant_id, subject, query, rewritten_query, status,
		       index_version, index_collection, embedding_model, generator, model, prompt_version,
		       top_k, candidate_count, hit_count, rerank_applied, rewrite_applied, answerable,
		       refusal_reason, embedding_ms, retrieval_ms, generation_ms, total_ms, prompt_tokens,
		       output_tokens, trace_parent, span_id, provider, input_cost_usd, output_cost_usd, total_cost_usd,
		       error, metadata, started_at, completed_at
		FROM query_traces WHERE trace_id=$1 AND app_id=$2`, traceID, appID).Scan(
		&record.TraceID, &record.AppID, &record.EnvironmentID, &record.TenantID, &record.Subject,
		&record.Query, &record.RewrittenQuery, &record.Status, &record.IndexVersion, &record.IndexCollection,
		&record.EmbeddingModel, &record.Generator, &record.Model, &record.PromptVersion, &record.TopK,
		&record.CandidateCount, &record.HitCount, &record.RerankApplied, &record.RewriteApplied, &answerable,
		&record.RefusalReason, &record.EmbeddingMS, &record.RetrievalMS, &record.GenerationMS, &record.TotalMS,
		&record.PromptTokens, &record.OutputTokens, &record.TraceParent, &record.SpanID, &record.Provider,
		&record.InputCostUSD, &record.OutputCostUSD, &record.TotalCostUSD, &record.Error, &metadata, &record.StartedAt, &completedAt)
	if err == sql.ErrNoRows {
		return querytrace.Record{}, querytrace.ErrNotFound
	}
	if err != nil {
		return querytrace.Record{}, err
	}
	if record.TenantID != identity.TenantID && !identity.HasRole("platform_admin") {
		return querytrace.Record{}, querytrace.ErrDenied
	}
	if answerable.Valid {
		record.Answerable = &answerable.Bool
	}
	if completedAt.Valid {
		record.CompletedAt = &completedAt.Time
	}
	if len(metadata) > 0 && string(metadata) != "null" {
		if err := json.Unmarshal(metadata, &record.Metadata); err != nil {
			return querytrace.Record{}, err
		}
	}
	return record, nil
}

func (store *PostgresStore) EnsureIdentity(ctx context.Context, identity auth.Identity) error {
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.TenantID) == "" || identity.PrimaryRole() == "" {
		return fmt.Errorf("verified subject, tenant and role are required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tenants (id, name, status) VALUES ($1, $2, 'active')
		ON CONFLICT (id) DO NOTHING`, identity.TenantID, identity.TenantID); err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (subject, display_name, status) VALUES ($1, $2, 'active')
		ON CONFLICT (subject) DO NOTHING`, identity.Subject, identity.Subject); err != nil {
		return fmt.Errorf("ensure user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (tenant_id, subject, role, status)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT (tenant_id, subject) DO NOTHING`,
		identity.TenantID, identity.Subject, identity.PrimaryRole()); err != nil {
		return fmt.Errorf("ensure membership: %w", err)
	}
	return tx.Commit()
}

func (store *PostgresStore) Visible(ctx context.Context, identity auth.Identity) ([]Dataset, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.description, d.product, d.visibility, d.tenant_id,
		       d.status, d.created_by,
		       COALESCE(string_agg(dr.role, ',' ORDER BY dr.role), '')
		FROM datasets d
		LEFT JOIN dataset_roles dr ON dr.dataset_id = d.id
		WHERE d.status = 'active' AND (
			$3 = true OR d.visibility = 'public' OR (
				d.tenant_id = $1 AND EXISTS (
					SELECT 1 FROM memberships m
					WHERE m.tenant_id = d.tenant_id AND m.subject = $2
					  AND m.status = 'active'
					  AND EXISTS (
						SELECT 1 FROM dataset_roles permitted
						WHERE permitted.dataset_id = d.id AND permitted.role = m.role
					  )
				)
			)
		)
		GROUP BY d.id
		ORDER BY d.id`,
		identity.TenantID, identity.Subject, identity.HasRole("platform_admin"))
	if err != nil {
		return nil, fmt.Errorf("list visible datasets: %w", err)
	}
	defer rows.Close()
	var datasets []Dataset
	for rows.Next() {
		var dataset Dataset
		var roles string
		if err := rows.Scan(
			&dataset.ID, &dataset.Name, &dataset.Description, &dataset.Product,
			&dataset.Visibility, &dataset.OwnerTenant, &dataset.Status,
			&dataset.CreatedBy, &roles,
		); err != nil {
			return nil, fmt.Errorf("scan dataset: %w", err)
		}
		if roles != "" {
			dataset.AllowedRoles = strings.Split(roles, ",")
		}
		datasets = append(datasets, dataset)
	}
	return datasets, rows.Err()
}

func (store *PostgresStore) Authorize(ctx context.Context, id string, identity auth.Identity) (Dataset, error) {
	if identity.ApplicationID != "" {
		var dataset Dataset
		var roles string
		err := store.db.QueryRowContext(ctx, `
			SELECT d.id,d.name,d.description,d.product,d.visibility,d.tenant_id,d.status,d.created_by,
			COALESCE(string_agg(dr.role, ',' ORDER BY dr.role), '')
			FROM datasets d LEFT JOIN dataset_roles dr ON dr.dataset_id=d.id
			WHERE d.id=$1 AND d.status='active' AND d.tenant_id=$2 AND EXISTS (
				SELECT 1 FROM knowledge_bindings b WHERE b.app_id=$3 AND b.dataset_id=d.id AND b.status='active')
			GROUP BY d.id`, strings.TrimSpace(id), identity.TenantID, identity.ApplicationID).Scan(
			&dataset.ID, &dataset.Name, &dataset.Description, &dataset.Product, &dataset.Visibility, &dataset.OwnerTenant, &dataset.Status, &dataset.CreatedBy, &roles)
		if err == nil {
			if roles != "" {
				dataset.AllowedRoles = strings.Split(roles, ",")
			}
			return dataset, nil
		}
		if err != sql.ErrNoRows {
			return Dataset{}, err
		}
	}
	datasets, err := store.Visible(ctx, identity)
	if err != nil {
		return Dataset{}, err
	}
	for _, dataset := range datasets {
		if dataset.ID == strings.TrimSpace(id) {
			return dataset, nil
		}
	}
	var exists bool
	if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM datasets WHERE id = $1)`, strings.TrimSpace(id)).Scan(&exists); err != nil {
		return Dataset{}, err
	}
	if exists {
		return Dataset{}, ErrDatasetDenied
	}
	return Dataset{}, ErrDatasetNotFound
}

func (store *PostgresStore) Create(ctx context.Context, identity auth.Identity, input CreateDataset) (Dataset, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return Dataset{}, err
	}
	name, slug := strings.TrimSpace(input.Name), strings.TrimSpace(input.Slug)
	if name == "" || len(name) > 120 {
		return Dataset{}, fmt.Errorf("dataset name is required and must not exceed 120 characters")
	}
	if !datasetSlugPattern.MatchString(slug) || len(slug) > 48 {
		return Dataset{}, fmt.Errorf("slug must contain lowercase letters, numbers and single hyphens")
	}
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" {
		visibility = "tenant"
	}
	ownerTenant := identity.TenantID
	if identity.HasRole("platform_admin") && visibility == "public" {
		ownerTenant = "platform"
	} else if !identity.HasRole("admin") || visibility != "tenant" {
		return Dataset{}, ErrDatasetDenied
	}
	var membershipRole, membershipStatus string
	if err := store.db.QueryRowContext(ctx, `
		SELECT role, status FROM memberships WHERE tenant_id = $1 AND subject = $2`,
		identity.TenantID, identity.Subject).Scan(&membershipRole, &membershipStatus); err != nil {
		return Dataset{}, ErrDatasetDenied
	}
	if !identity.HasRole("platform_admin") && (membershipRole != "admin" || membershipStatus != "active") {
		return Dataset{}, ErrDatasetDenied
	}
	allowedRoles := normalizeAllowedRoles(input.AllowedRoles)
	if len(allowedRoles) == 0 {
		allowedRoles = []string{"admin"}
	}
	if !identity.HasRole("platform_admin") {
		for _, role := range allowedRoles {
			if role != "viewer" && role != "admin" {
				return Dataset{}, fmt.Errorf("tenant administrators may grant only viewer or admin access")
			}
		}
	}
	id := ownerTenant + "-" + slug
	product := "dataset-" + id
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Dataset{}, err
	}
	defer tx.Rollback()
	dataset := Dataset{
		ID: id, Name: name, Description: strings.TrimSpace(input.Description), Product: product,
		Visibility: visibility, OwnerTenant: ownerTenant, AllowedRoles: allowedRoles,
		Status: "active", CreatedBy: identity.Subject,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO datasets (id, tenant_id, name, description, product, visibility, status, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,'active',$7)`,
		dataset.ID, dataset.OwnerTenant, dataset.Name, dataset.Description,
		dataset.Product, dataset.Visibility, dataset.CreatedBy); err != nil {
		return Dataset{}, fmt.Errorf("create dataset: %w", err)
	}
	for _, role := range dataset.AllowedRoles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO dataset_roles (dataset_id, role) VALUES ($1, $2)`, dataset.ID, role); err != nil {
			return Dataset{}, fmt.Errorf("create dataset role: %w", err)
		}
	}
	after, _ := json.Marshal(dataset)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO control_plane_audit (actor_subject, tenant_id, action, resource_type, resource_id, after_state)
		VALUES ($1,$2,'dataset.create','dataset',$3,$4)`,
		identity.Subject, identity.TenantID, dataset.ID, after); err != nil {
		return Dataset{}, fmt.Errorf("audit dataset creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func normalizeAllowedRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(strings.ToLower(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	return result
}

func (store *PostgresStore) Members(ctx context.Context, identity auth.Identity) ([]Membership, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return nil, err
	}
	if !identity.HasRole("admin") && !identity.HasRole("platform_admin") {
		return nil, ErrDatasetDenied
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT tenant_id, subject, role, status FROM memberships
		WHERE tenant_id = $1 ORDER BY subject`, identity.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var memberships []Membership
	for rows.Next() {
		var membership Membership
		if err := rows.Scan(&membership.TenantID, &membership.Subject, &membership.Role, &membership.Status); err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}
	return memberships, rows.Err()
}

func (store *PostgresStore) Status(ctx context.Context, identity auth.Identity) (Status, error) {
	if err := store.EnsureIdentity(ctx, identity); err != nil {
		return Status{}, err
	}
	status := Status{Backend: "postgres", Connected: true}
	queries := []struct {
		query       string
		destination *int64
	}{
		{`SELECT count(*) FROM tenants WHERE $2 OR id=$1`, &status.Tenants},
		{`SELECT count(DISTINCT subject) FROM memberships WHERE $2 OR tenant_id=$1`, &status.Users},
		{`SELECT count(*) FROM memberships WHERE $2 OR tenant_id=$1`, &status.Memberships},
		{`SELECT count(*) FROM datasets WHERE status='active' AND ($2 OR visibility='public' OR tenant_id=$1)`, &status.Datasets},
	}
	for _, item := range queries {
		if err := store.db.QueryRowContext(ctx, item.query, identity.TenantID, identity.HasRole("platform_admin")).Scan(item.destination); err != nil {
			return Status{}, err
		}
	}
	return status, nil
}

func (store *PostgresStore) migrate(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71324119)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, controlPlaneSchema); err != nil {
		return fmt.Errorf("migrate control plane: %w", err)
	}
	return tx.Commit()
}

func (store *PostgresStore) seed(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, tenant := range []struct{ id, name string }{
		{"platform", "Platform"}, {"tenant_a", "Tenant A"}, {"tenant_b", "Tenant B"},
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tenants (id,name,status) VALUES ($1,$2,'active')
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`, tenant.id, tenant.name); err != nil {
			return err
		}
	}
	for _, dataset := range Defaults().byID {
		owner := dataset.OwnerTenant
		if owner == "" {
			owner = "platform"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO datasets (id,tenant_id,name,description,product,visibility,status,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,'active','system')
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description,
			  product=EXCLUDED.product, visibility=EXCLUDED.visibility`,
			dataset.ID, owner, dataset.Name, dataset.Description, dataset.Product, dataset.Visibility); err != nil {
			return err
		}
		for _, role := range dataset.AllowedRoles {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO dataset_roles (dataset_id,role) VALUES ($1,$2)
				ON CONFLICT DO NOTHING`, dataset.ID, role); err != nil {
				return err
			}
		}
	}
	// Seed one realistic application per demo tenant so a fresh deployment can
	// exercise the application-facing Gateway immediately. ON CONFLICT keeps
	// operator-created names, policies and bindings untouched on later boots.
	for _, application := range []struct {
		id, tenant, name, slug, dataset string
	}{
		{"tenant_a-support-agent", "tenant_a", "Tenant A Support Agent", "support-agent", "tenant-a-operations"},
		{"tenant_b-support-agent", "tenant_b", "Tenant B Support Agent", "support-agent", "tenant-b-operations"},
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO applications (app_id,tenant_id,name,slug,description,status,created_by)
			VALUES ($1,$2,$3,$4,$5,'active','system')
			ON CONFLICT (app_id) DO NOTHING`, application.id, application.tenant, application.name, application.slug,
			"Demo application for the application-level Knowledge Gateway"); err != nil {
			return err
		}
		environmentID := application.id + "-dev"
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_environments (environment_id,app_id,name,config_version,status)
			VALUES ($1,$2,'dev','v1','active') ON CONFLICT (environment_id) DO NOTHING`, environmentID, application.id); err != nil {
			return err
		}
		policy, _ := json.Marshal(RetrievalPolicy{TopK: 5, CandidateK: 20, Rerank: true, QueryRewrite: true, TokenBudget: 4000})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_bindings (app_id,environment_id,dataset_id,purpose,priority,status,policy,created_by)
			VALUES ($1,$2,$3,'tenant support knowledge',10,'active',$4,'system')
			ON CONFLICT (app_id,environment_id,dataset_id) DO NOTHING`, application.id, environmentID, application.dataset, policy); err != nil {
			return err
		}
		for _, publicDataset := range []string{
			"public-identity", "public-reports", "public-api-platform", "public-storage",
			"public-billing", "public-operations", "public-security", "public-integrations",
		} {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO knowledge_bindings (app_id,environment_id,dataset_id,purpose,priority,status,policy,created_by)
				VALUES ($1,$2,$3,'shared public product knowledge',20,'active',$4,'system')
				ON CONFLICT (app_id,environment_id,dataset_id) DO NOTHING`, application.id, environmentID, publicDataset, policy); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

const controlPlaneSchema = `
CREATE TABLE IF NOT EXISTS tenants (
	id text PRIMARY KEY,
	name text NOT NULL,
	status text NOT NULL CHECK (status IN ('active','suspended')),
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS users (
	subject text PRIMARY KEY,
	display_name text NOT NULL,
	status text NOT NULL CHECK (status IN ('active','disabled')),
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS memberships (
	tenant_id text NOT NULL REFERENCES tenants(id),
	subject text NOT NULL REFERENCES users(subject),
	role text NOT NULL CHECK (role IN ('viewer','admin','platform_admin')),
	status text NOT NULL CHECK (status IN ('active','revoked')),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (tenant_id, subject)
);
CREATE TABLE IF NOT EXISTS datasets (
	id text PRIMARY KEY,
	tenant_id text NOT NULL REFERENCES tenants(id),
	name text NOT NULL,
	description text NOT NULL DEFAULT '',
	product text NOT NULL,
	visibility text NOT NULL CHECK (visibility IN ('public','tenant')),
	status text NOT NULL CHECK (status IN ('active','archived')),
	created_by text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS dataset_roles (
	dataset_id text NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
	role text NOT NULL CHECK (role IN ('viewer','admin','platform_admin')),
	PRIMARY KEY (dataset_id, role)
);
CREATE TABLE IF NOT EXISTS control_plane_audit (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	actor_subject text NOT NULL,
	tenant_id text NOT NULL,
	action text NOT NULL,
	resource_type text NOT NULL,
	resource_id text NOT NULL,
	before_state jsonb,
	after_state jsonb,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS memberships_subject_idx ON memberships(subject, status);
CREATE INDEX IF NOT EXISTS datasets_tenant_status_idx ON datasets(tenant_id, status);
ALTER TABLE datasets DROP CONSTRAINT IF EXISTS datasets_product_key;
CREATE TABLE IF NOT EXISTS applications (
	app_id text PRIMARY KEY,
	tenant_id text NOT NULL REFERENCES tenants(id),
	name text NOT NULL,
	slug text NOT NULL,
	description text NOT NULL DEFAULT '',
	status text NOT NULL CHECK (status IN ('active','archived')),
	created_by text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, slug)
);
CREATE INDEX IF NOT EXISTS applications_tenant_status_idx ON applications(tenant_id, status);
CREATE TABLE IF NOT EXISTS app_environments (
	environment_id text PRIMARY KEY,
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	name text NOT NULL,
	config_version text NOT NULL DEFAULT 'v1',
	status text NOT NULL CHECK (status IN ('active','archived')),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (app_id, name)
);
CREATE INDEX IF NOT EXISTS app_environments_app_status_idx ON app_environments(app_id, status);
CREATE TABLE IF NOT EXISTS knowledge_bindings (
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	environment_id text NOT NULL REFERENCES app_environments(environment_id) ON DELETE CASCADE,
	dataset_id text NOT NULL REFERENCES datasets(id),
	purpose text NOT NULL DEFAULT '',
	priority integer NOT NULL DEFAULT 0,
	status text NOT NULL CHECK (status IN ('active','disabled')),
	policy jsonb NOT NULL DEFAULT '{}'::jsonb,
	created_by text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (app_id, environment_id, dataset_id)
);
CREATE TABLE IF NOT EXISTS index_releases (
	release_id text PRIMARY KEY,
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	environment_id text NOT NULL REFERENCES app_environments(environment_id) ON DELETE CASCADE,
	version text NOT NULL,
	collection text NOT NULL,
	alias text NOT NULL DEFAULT '',
	state text NOT NULL CHECK (state IN ('published','canary','superseded')),
	channel text NOT NULL DEFAULT 'stable',
	rollout_percent integer NOT NULL DEFAULT 100,
	published_by text NOT NULL,
	published_at timestamptz NOT NULL DEFAULT now(),
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (app_id, environment_id, version)
);
CREATE INDEX IF NOT EXISTS index_releases_environment_state_idx ON index_releases(app_id, environment_id, state, published_at DESC);
ALTER TABLE index_releases DROP CONSTRAINT IF EXISTS index_releases_state_check;
ALTER TABLE index_releases ADD CONSTRAINT index_releases_state_check CHECK (state IN ('published','canary','superseded'));
ALTER TABLE index_releases ADD COLUMN IF NOT EXISTS channel text NOT NULL DEFAULT 'stable';
ALTER TABLE index_releases ADD COLUMN IF NOT EXISTS rollout_percent integer NOT NULL DEFAULT 100;
CREATE TABLE IF NOT EXISTS query_traces (
	trace_id text PRIMARY KEY,
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	environment_id text NOT NULL REFERENCES app_environments(environment_id) ON DELETE CASCADE,
	tenant_id text NOT NULL REFERENCES tenants(id),
	subject text NOT NULL,
	query text NOT NULL,
	rewritten_query text NOT NULL DEFAULT '',
	status text NOT NULL CHECK (status IN ('retrieved','completed','failed')),
	index_version text NOT NULL DEFAULT '',
	index_collection text NOT NULL DEFAULT '',
	embedding_model text NOT NULL DEFAULT '',
	generator text NOT NULL DEFAULT '',
	model text NOT NULL DEFAULT '',
	prompt_version text NOT NULL DEFAULT '',
	top_k integer NOT NULL DEFAULT 0,
	candidate_count integer NOT NULL DEFAULT 0,
	hit_count integer NOT NULL DEFAULT 0,
	rerank_applied boolean NOT NULL DEFAULT false,
	rewrite_applied boolean NOT NULL DEFAULT false,
	answerable boolean,
	refusal_reason text NOT NULL DEFAULT '',
	embedding_ms double precision NOT NULL DEFAULT 0,
	retrieval_ms double precision NOT NULL DEFAULT 0,
	generation_ms double precision NOT NULL DEFAULT 0,
	total_ms double precision NOT NULL DEFAULT 0,
	prompt_tokens integer NOT NULL DEFAULT 0,
	output_tokens integer NOT NULL DEFAULT 0,
	trace_parent text NOT NULL DEFAULT '',
	span_id text NOT NULL DEFAULT '',
	provider text NOT NULL DEFAULT '',
	input_cost_usd double precision NOT NULL DEFAULT 0,
	output_cost_usd double precision NOT NULL DEFAULT 0,
	total_cost_usd double precision NOT NULL DEFAULT 0,
	error text NOT NULL DEFAULT '',
	metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
	started_at timestamptz NOT NULL DEFAULT now(),
	completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS query_traces_app_started_idx ON query_traces(app_id, environment_id, started_at DESC);
CREATE INDEX IF NOT EXISTS knowledge_bindings_dataset_idx ON knowledge_bindings(dataset_id, status);
CREATE TABLE IF NOT EXISTS ingestion_jobs (
	job_id text PRIMARY KEY,
	idempotency_key text NOT NULL UNIQUE,
	tenant_id text NOT NULL DEFAULT '',
	dataset_id text NOT NULL DEFAULT '',
	created_by text NOT NULL DEFAULT '',
	status text NOT NULL,
	stage text NOT NULL,
	attempts integer NOT NULL DEFAULT 0,
	max_attempts integer NOT NULL,
	cancel_requested boolean NOT NULL DEFAULT false,
	last_error text NOT NULL DEFAULT '',
	result jsonb,
	change jsonb NOT NULL,
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL,
	started_at timestamptz,
	completed_at timestamptz,
	worker_id text NOT NULL DEFAULT '',
	lease_expires_at timestamptz,
	last_heartbeat_at timestamptz,
	payload_hash text NOT NULL
);
CREATE INDEX IF NOT EXISTS ingestion_jobs_scope_idx ON ingestion_jobs(tenant_id, dataset_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ingestion_jobs_status_idx ON ingestion_jobs(status, lease_expires_at);
CREATE TABLE IF NOT EXISTS ingestion_job_events (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	job_id text NOT NULL REFERENCES ingestion_jobs(job_id) ON DELETE CASCADE,
	event_type text NOT NULL,
	status text NOT NULL,
	stage text NOT NULL,
	attempt integer NOT NULL,
	worker_id text NOT NULL DEFAULT '',
	error text,
	occurred_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS ingestion_job_events_job_idx ON ingestion_job_events(job_id, occurred_at);
CREATE TABLE IF NOT EXISTS application_credentials (
	credential_id text PRIMARY KEY,
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	tenant_id text NOT NULL REFERENCES tenants(id),
	name text NOT NULL,
	secret_hash text NOT NULL UNIQUE,
	scopes jsonb NOT NULL DEFAULT '[]'::jsonb,
	status text NOT NULL CHECK (status IN ('active','revoked')),
	created_by text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	expires_at timestamptz,
	last_used_at timestamptz
);
CREATE INDEX IF NOT EXISTS application_credentials_app_idx ON application_credentials(app_id, status);
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS trace_parent text NOT NULL DEFAULT '';
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS span_id text NOT NULL DEFAULT '';
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT '';
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS input_cost_usd double precision NOT NULL DEFAULT 0;
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS output_cost_usd double precision NOT NULL DEFAULT 0;
ALTER TABLE query_traces ADD COLUMN IF NOT EXISTS total_cost_usd double precision NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS index_builds (
	build_id text PRIMARY KEY,
	idempotency_key text NOT NULL UNIQUE,
	request_hash text NOT NULL,
	app_id text NOT NULL REFERENCES applications(app_id) ON DELETE CASCADE,
	environment_id text NOT NULL REFERENCES app_environments(environment_id) ON DELETE CASCADE,
	version text NOT NULL,
	collection text NOT NULL,
	alias text NOT NULL DEFAULT '',
	embedding_model text NOT NULL DEFAULT '',
	embedding_version text NOT NULL DEFAULT '',
	chunker_version text NOT NULL DEFAULT '',
	source_revision bigint NOT NULL DEFAULT 0,
	status text NOT NULL CHECK (status IN ('queued','running','completed','failed','cancelled')),
	stage text NOT NULL,
	attempts integer NOT NULL DEFAULT 0,
	last_error text NOT NULL DEFAULT '',
	manifest jsonb,
	created_by text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS index_builds_scope_idx ON index_builds(app_id, environment_id, created_at DESC);
CREATE INDEX IF NOT EXISTS index_builds_pending_idx ON index_builds(status, created_at);
`

var _ Store = (*PostgresStore)(nil)
var _ indexbuild.Store = (*PostgresStore)(nil)

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
`

var _ Store = (*PostgresStore)(nil)

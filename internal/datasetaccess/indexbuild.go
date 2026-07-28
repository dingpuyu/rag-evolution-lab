package datasetaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
)

func indexBuildRequestHash(input indexbuild.Request) string {
	data, _ := json.Marshal(input)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (store *PostgresStore) CreateIndexBuild(ctx context.Context, identity auth.Identity, input indexbuild.Request) (indexbuild.Build, bool, error) {
	application, err := store.authorizeApplication(ctx, identity, input.ApplicationID)
	if err != nil {
		return indexbuild.Build{}, false, err
	}
	input.ApplicationID = application.ID
	input.EnvironmentID = strings.TrimSpace(input.EnvironmentID)
	input.Version = strings.TrimSpace(input.Version)
	input.Collection = strings.TrimSpace(input.Collection)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.EnvironmentID == "" || input.Version == "" || input.Collection == "" || input.IdempotencyKey == "" {
		return indexbuild.Build{}, false, fmt.Errorf("environment_id, version, collection and idempotency_key are required")
	}
	var environmentStatus string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM app_environments WHERE environment_id=$1 AND app_id=$2`, input.EnvironmentID, application.ID).Scan(&environmentStatus); err != nil {
		if err == sql.ErrNoRows {
			return indexbuild.Build{}, false, ErrDatasetNotFound
		}
		return indexbuild.Build{}, false, err
	}
	if environmentStatus != "active" {
		return indexbuild.Build{}, false, fmt.Errorf("application environment is not active")
	}
	hash := indexBuildRequestHash(input)
	buildID := "ib_" + hash[:24]
	var existingHash string
	var existing indexbuild.Build
	var manifest []byte
	var completedAt sql.NullTime
	err = store.db.QueryRowContext(ctx, `
		SELECT build_id, idempotency_key, request_hash, app_id, environment_id, version, collection, alias,
		       embedding_model, embedding_version, chunker_version, source_revision, status, stage, attempts,
		       last_error, manifest, created_by, created_at, updated_at, completed_at
		FROM index_builds WHERE idempotency_key=$1`, input.IdempotencyKey).Scan(
		&existing.BuildID, &existing.IdempotencyKey, &existingHash, &existing.ApplicationID, &existing.EnvironmentID,
		&existing.Version, &existing.Collection, &existing.Alias, &existing.EmbeddingModel, &existing.EmbeddingVer,
		&existing.ChunkerVersion, &existing.SourceRevision, &existing.Status, &existing.Stage, &existing.Attempts,
		&existing.LastError, &manifest, &existing.CreatedBy, &existing.CreatedAt, &existing.UpdatedAt, &completedAt)
	if err == nil {
		if existingHash != hash {
			return indexbuild.Build{}, false, indexbuild.ErrBuildConflict
		}
		existing.Manifest = decodeBuildManifest(manifest)
		if completedAt.Valid {
			existing.CompletedAt = &completedAt.Time
		}
		return existing, true, nil
	}
	if err != sql.ErrNoRows {
		return indexbuild.Build{}, false, err
	}
	build := indexbuild.Build{
		BuildID: buildID, IdempotencyKey: input.IdempotencyKey, ApplicationID: application.ID,
		EnvironmentID: input.EnvironmentID, Version: input.Version, Collection: input.Collection,
		Alias: strings.TrimSpace(input.Alias), EmbeddingModel: input.EmbeddingModel, EmbeddingVer: input.EmbeddingVer,
		ChunkerVersion: input.ChunkerVersion, SourceRevision: input.SourceRevision,
		Status: indexbuild.StatusQueued, Stage: indexbuild.StageValidating,
		CreatedBy: identity.Subject, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO index_builds (build_id,idempotency_key,request_hash,app_id,environment_id,version,collection,alias,
		 embedding_model,embedding_version,chunker_version,source_revision,status,stage,attempts,last_error,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'queued','validating',0,'',$13)`,
		build.BuildID, build.IdempotencyKey, hash, build.ApplicationID, build.EnvironmentID, build.Version, build.Collection,
		build.Alias, input.EmbeddingModel, input.EmbeddingVer, input.ChunkerVersion, input.SourceRevision, build.CreatedBy)
	if err != nil {
		return indexbuild.Build{}, false, fmt.Errorf("create index build: %w", err)
	}
	return build, false, nil
}

func (store *PostgresStore) GetIndexBuild(ctx context.Context, identity auth.Identity, appID, buildID string) (indexbuild.Build, error) {
	if _, err := store.authorizeApplication(ctx, identity, appID); err != nil {
		return indexbuild.Build{}, err
	}
	build, err := store.scanIndexBuild(store.db.QueryRowContext(ctx, `
		SELECT build_id,idempotency_key,app_id,environment_id,version,collection,alias,embedding_model,embedding_version,
		       chunker_version,source_revision,status,stage,attempts,last_error,manifest,created_by,created_at,updated_at,completed_at
		FROM index_builds WHERE build_id=$1 AND app_id=$2`, strings.TrimSpace(buildID), strings.TrimSpace(appID)))
	if err == sql.ErrNoRows {
		return indexbuild.Build{}, indexbuild.ErrBuildNotFound
	}
	return build, err
}

func (store *PostgresStore) ListIndexBuilds(ctx context.Context, identity auth.Identity, appID, environmentID string) ([]indexbuild.Build, error) {
	if _, err := store.authorizeApplication(ctx, identity, appID); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT build_id,idempotency_key,app_id,environment_id,version,collection,alias,embedding_model,embedding_version,
		       chunker_version,source_revision,status,stage,attempts,last_error,manifest,created_by,created_at,updated_at,completed_at
		FROM index_builds WHERE app_id=$1 AND ($2='' OR environment_id=$2) ORDER BY created_at DESC`, strings.TrimSpace(appID), strings.TrimSpace(environmentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	builds := make([]indexbuild.Build, 0)
	for rows.Next() {
		build, scanErr := store.scanIndexBuild(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

func (store *PostgresStore) PendingIndexBuilds(ctx context.Context) ([]indexbuild.Build, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT build_id,idempotency_key,app_id,environment_id,version,collection,alias,embedding_model,embedding_version,
		       chunker_version,source_revision,status,stage,attempts,last_error,manifest,created_by,created_at,updated_at,completed_at
		FROM index_builds WHERE status IN ('queued','running') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	builds := make([]indexbuild.Build, 0)
	for rows.Next() {
		build, scanErr := store.scanIndexBuild(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

func (store *PostgresStore) ClaimIndexBuild(ctx context.Context, buildID string, _ int) (indexbuild.Build, bool, error) {
	row := store.db.QueryRowContext(ctx, `
		UPDATE index_builds SET status='running', stage='validating', attempts=attempts+1, last_error='', updated_at=now()
		WHERE build_id=$1 AND status IN ('queued','running')
		RETURNING build_id,idempotency_key,app_id,environment_id,version,collection,alias,embedding_model,embedding_version,
		          chunker_version,source_revision,status,stage,attempts,last_error,manifest,created_by,created_at,updated_at,completed_at`, buildID)
	build, err := store.scanIndexBuild(row)
	if err == sql.ErrNoRows {
		return indexbuild.Build{}, false, nil
	}
	if err != nil {
		return indexbuild.Build{}, false, err
	}
	return build, true, nil
}

type rowScanner interface{ Scan(...any) error }

func (store *PostgresStore) scanIndexBuild(row rowScanner) (indexbuild.Build, error) {
	var build indexbuild.Build
	var manifest []byte
	var completedAt sql.NullTime
	err := row.Scan(&build.BuildID, &build.IdempotencyKey, &build.ApplicationID, &build.EnvironmentID, &build.Version,
		&build.Collection, &build.Alias, &build.EmbeddingModel, &build.EmbeddingVer, &build.ChunkerVersion, &build.SourceRevision,
		&build.Status, &build.Stage, &build.Attempts, &build.LastError, &manifest, &build.CreatedBy, &build.CreatedAt,
		&build.UpdatedAt, &completedAt)
	if err != nil {
		return indexbuild.Build{}, err
	}
	build.Manifest = decodeBuildManifest(manifest)
	if completedAt.Valid {
		build.CompletedAt = &completedAt.Time
	}
	return build, nil
}

func decodeBuildManifest(data []byte) *indexbuild.Manifest {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var manifest indexbuild.Manifest
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	return &manifest
}

func (store *PostgresStore) UpdateIndexBuild(ctx context.Context, buildID, status, stage string, attempt int, lastError string, manifest *indexbuild.Manifest) error {
	var payload any
	if manifest != nil {
		data, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		payload = data
	}
	var completed any
	if status == indexbuild.StatusCompleted || status == indexbuild.StatusFailed || status == indexbuild.StatusCancelled {
		completed = time.Now().UTC()
	}
	_, err := store.db.ExecContext(ctx, `
		UPDATE index_builds SET status=$2,stage=$3,attempts=$4,last_error=$5,manifest=COALESCE($6,manifest),
		 updated_at=now(),completed_at=CASE WHEN $7::timestamptz IS NULL THEN completed_at ELSE $7 END
		WHERE build_id=$1`, buildID, status, stage, attempt, strings.TrimSpace(lastError), payload, completed)
	return err
}

package datasetaccess

import (
	"context"
	"encoding/json"
	"time"
)

// KnowledgeDocumentRevision is control-plane metadata. The raw content and
// Document IR stay in object storage; vectors stay in Milvus.
type KnowledgeDocumentRevision struct {
	DatasetID       string         `json:"dataset_id"`
	DocumentID      string         `json:"document_id"`
	Title           string         `json:"title"`
	SourceRevision  int64          `json:"source_revision"`
	DocumentVersion string         `json:"document_version,omitempty"`
	FileName        string         `json:"file_name"`
	ContentType     string         `json:"content_type"`
	SourceURI       string         `json:"source_uri"`
	IRURI           string         `json:"ir_uri,omitempty"`
	SourceHash      string         `json:"source_hash"`
	ParserStatus    string         `json:"parser_status"`
	IndexStatus     string         `json:"index_status"`
	JobID           string         `json:"job_id,omitempty"`
	BlockCount      int            `json:"block_count"`
	ChunkCount      int            `json:"chunk_count"`
	IndexVersion    string         `json:"index_version,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type DocumentRegistry interface {
	UpsertKnowledgeDocument(context.Context, KnowledgeDocumentRevision) error
	ListKnowledgeDocuments(context.Context, string) ([]KnowledgeDocumentRevision, error)
}

func (store *PostgresStore) UpsertKnowledgeDocument(ctx context.Context, revision KnowledgeDocumentRevision) error {
	metadata, err := json.Marshal(revision.Metadata)
	if err != nil {
		return err
	}
	warnings, err := json.Marshal(revision.Warnings)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO knowledge_document_revisions (
			dataset_id,document_id,title,source_revision,document_version,file_name,content_type,
			source_uri,ir_uri,source_hash,parser_status,index_status,job_id,block_count,chunk_count,
			index_version,metadata,warnings,last_error,created_by,created_at,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,now(),now())
		ON CONFLICT (dataset_id,document_id,source_revision) DO UPDATE SET
			title=EXCLUDED.title,document_version=EXCLUDED.document_version,file_name=EXCLUDED.file_name,
			content_type=EXCLUDED.content_type,source_uri=EXCLUDED.source_uri,ir_uri=EXCLUDED.ir_uri,
			source_hash=EXCLUDED.source_hash,parser_status=EXCLUDED.parser_status,index_status=EXCLUDED.index_status,
			job_id=EXCLUDED.job_id,block_count=EXCLUDED.block_count,chunk_count=EXCLUDED.chunk_count,
			index_version=EXCLUDED.index_version,metadata=EXCLUDED.metadata,warnings=EXCLUDED.warnings,
			last_error=EXCLUDED.last_error,updated_at=now()`,
		revision.DatasetID, revision.DocumentID, revision.Title, revision.SourceRevision, revision.DocumentVersion,
		revision.FileName, revision.ContentType, revision.SourceURI, revision.IRURI, revision.SourceHash,
		revision.ParserStatus, revision.IndexStatus, revision.JobID, revision.BlockCount, revision.ChunkCount,
		revision.IndexVersion, metadata, warnings, revision.LastError, revision.CreatedBy)
	return err
}

func (store *PostgresStore) ListKnowledgeDocuments(ctx context.Context, datasetID string) ([]KnowledgeDocumentRevision, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT dataset_id,document_id,title,source_revision,document_version,file_name,content_type,
		       source_uri,ir_uri,source_hash,parser_status,index_status,job_id,block_count,chunk_count,
		       index_version,metadata,warnings,last_error,created_by,created_at,updated_at
		FROM knowledge_document_revisions
		WHERE dataset_id=$1
		ORDER BY updated_at DESC, document_id, source_revision DESC`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]KnowledgeDocumentRevision, 0)
	for rows.Next() {
		var item KnowledgeDocumentRevision
		var metadata, warnings []byte
		if err := rows.Scan(
			&item.DatasetID, &item.DocumentID, &item.Title, &item.SourceRevision, &item.DocumentVersion,
			&item.FileName, &item.ContentType, &item.SourceURI, &item.IRURI, &item.SourceHash,
			&item.ParserStatus, &item.IndexStatus, &item.JobID, &item.BlockCount, &item.ChunkCount,
			&item.IndexVersion, &metadata, &warnings, &item.LastError, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &item.Metadata)
		}
		if len(warnings) > 0 {
			_ = json.Unmarshal(warnings, &item.Warnings)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

var _ DocumentRegistry = (*PostgresStore)(nil)

package httpapi

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

func TestCanManageDatasetDocuments(t *testing.T) {
	public := datasetaccess.Dataset{ID: "public", Visibility: "public"}
	tenantA := datasetaccess.Dataset{ID: "tenant-a", Visibility: "tenant", OwnerTenant: "tenant_a"}
	tests := []struct {
		name     string
		dataset  datasetaccess.Dataset
		identity auth.Identity
		want     bool
	}{
		{name: "platform administrator can manage public", dataset: public, identity: auth.Identity{TenantID: "platform", Roles: []string{"platform_admin"}}, want: true},
		{name: "tenant administrator can manage own private dataset", dataset: tenantA, identity: auth.Identity{TenantID: "tenant_a", Roles: []string{"admin"}}, want: true},
		{name: "tenant administrator cannot manage public", dataset: public, identity: auth.Identity{TenantID: "tenant_a", Roles: []string{"admin"}}, want: false},
		{name: "other tenant administrator cannot manage private dataset", dataset: tenantA, identity: auth.Identity{TenantID: "tenant_b", Roles: []string{"admin"}}, want: false},
		{name: "viewer cannot inspect source pipeline", dataset: tenantA, identity: auth.Identity{TenantID: "tenant_a", Roles: []string{"viewer"}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canManageDatasetDocuments(test.dataset, test.identity); got != test.want {
				t.Fatalf("canManageDatasetDocuments() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBuildDocumentPipelineCompleted(t *testing.T) {
	record := datasetaccess.KnowledgeDocumentRevision{
		FileName: "manual.docx", SourceURI: "s3://documents/manual.docx", ParserStatus: "ready",
		BlockCount: 8, ChunkCount: 5, IndexVersion: "text-embedding-v4-1024",
	}
	job := &ingestionjob.Job{Status: ingestionjob.StatusCompleted, Stage: ingestionjob.StageCompleted, Result: &milvus.LifecycleResult{Verified: true}}
	pipeline := buildDocumentPipeline(record, job, true)
	if len(pipeline) != 7 {
		t.Fatalf("expected seven stages, got %d", len(pipeline))
	}
	for _, stage := range pipeline {
		if stage.Status != "completed" {
			t.Fatalf("stage %s should be completed: %#v", stage.Key, stage)
		}
	}
}

func TestBuildDocumentPipelineShowsOCRBlock(t *testing.T) {
	record := datasetaccess.KnowledgeDocumentRevision{FileName: "scan.pdf", SourceURI: "s3://documents/scan.pdf", ParserStatus: "ocr_required", IndexStatus: "blocked"}
	pipeline := buildDocumentPipeline(record, nil, false)
	for index, stage := range pipeline {
		if index == 0 {
			continue
		}
		if stage.Status != "blocked" {
			t.Fatalf("stage %s should be blocked: %#v", stage.Key, pipeline)
		}
	}
	if pipeline[0].Status != "completed" {
		t.Fatalf("unexpected OCR pipeline: %#v", pipeline)
	}
}

func TestBuildDocumentPipelineSurfacesEarlyWorkerFailure(t *testing.T) {
	record := datasetaccess.KnowledgeDocumentRevision{
		FileName: "manual.docx", SourceURI: "s3://documents/manual.docx", ParserStatus: "ready",
		BlockCount: 4, LastError: "metadata validation failed",
	}
	job := &ingestionjob.Job{
		Status: ingestionjob.StatusFailed, Stage: ingestionjob.StageFailed,
		FailureStage: "validating", LastError: "metadata validation failed",
	}
	pipeline := buildDocumentPipeline(record, job, false)
	if pipeline[3].Status != "failed" || pipeline[3].Detail != "metadata validation failed" {
		t.Fatalf("expected early failure on first visible worker stage: %#v", pipeline)
	}
	for _, stage := range pipeline[4:] {
		if stage.Status != "blocked" {
			t.Fatalf("downstream stage should be blocked: %#v", pipeline)
		}
	}
}

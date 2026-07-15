package dataset

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestValidateRejectsGoldenReferenceToMissingDocument(t *testing.T) {
	documents := []domain.Document{{ID: "present", Title: "Present", Content: "content", Visibility: "public"}}
	cases := []domain.GoldenCase{{
		ID: "case", Query: "query", Split: "development", Category: "exact_match",
		Expected: domain.GoldenExpected{Answerable: true, RelevantDocumentIDs: []string{"missing"}},
	}}
	if err := Validate(documents, cases); err == nil {
		t.Fatal("expected missing document reference to fail validation")
	}
}

func TestValidateRejectsPrivateDocumentWithoutACL(t *testing.T) {
	documents := []domain.Document{{ID: "private", Title: "Private", Content: "content", Visibility: "tenant"}}
	if err := Validate(documents, nil); err == nil {
		t.Fatal("expected private document without ACL to fail validation")
	}
}

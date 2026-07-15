package dataset

import (
	"fmt"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func Validate(documents []domain.Document, cases []domain.GoldenCase) error {
	documentIDs := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		if err := validateDocument(document); err != nil {
			return fmt.Errorf("document %q: %w", document.ID, err)
		}
		if _, exists := documentIDs[document.ID]; exists {
			return fmt.Errorf("duplicate document id %q", document.ID)
		}
		documentIDs[document.ID] = struct{}{}
	}
	caseIDs := make(map[string]struct{}, len(cases))
	for _, golden := range cases {
		if err := validateGolden(golden, documentIDs); err != nil {
			return fmt.Errorf("golden case %q: %w", golden.ID, err)
		}
		if _, exists := caseIDs[golden.ID]; exists {
			return fmt.Errorf("duplicate golden case id %q", golden.ID)
		}
		caseIDs[golden.ID] = struct{}{}
	}
	return nil
}

func validateDocument(document domain.Document) error {
	if strings.TrimSpace(document.ID) == "" || strings.TrimSpace(document.Title) == "" {
		return fmt.Errorf("id and title are required")
	}
	if strings.TrimSpace(document.Content) == "" {
		return fmt.Errorf("content is empty")
	}
	if document.Visibility != "public" && (len(document.AllowedTenants) == 0 || len(document.AllowedRoles) == 0) {
		return fmt.Errorf("non-public document requires tenants and roles")
	}
	return nil
}

func validateGolden(golden domain.GoldenCase, documents map[string]struct{}) error {
	if strings.TrimSpace(golden.ID) == "" || strings.TrimSpace(golden.Query) == "" {
		return fmt.Errorf("id and query are required")
	}
	if golden.Split == "" || golden.Category == "" {
		return fmt.Errorf("split and category are required")
	}
	if golden.Expected.Answerable && len(golden.Expected.RelevantDocumentIDs) == 0 {
		return fmt.Errorf("answerable case requires relevant documents")
	}
	for _, documentID := range golden.Expected.RelevantDocumentIDs {
		if _, exists := documents[documentID]; !exists {
			return fmt.Errorf("references missing document %q", documentID)
		}
	}
	return nil
}

package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func LoadCorpus(root string) ([]domain.Document, error) {
	manifestPath := filepath.Join(root, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var documents []domain.Document
	if err := json.Unmarshal(data, &documents); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	seen := make(map[string]struct{}, len(documents))
	for index := range documents {
		document := &documents[index]
		if _, exists := seen[document.ID]; exists {
			return nil, fmt.Errorf("duplicate document id %q", document.ID)
		}
		seen[document.ID] = struct{}{}
		content, err := os.ReadFile(filepath.Join(root, document.Path))
		if err != nil {
			return nil, fmt.Errorf("read document %q: %w", document.ID, err)
		}
		document.Content = string(content)
	}
	return documents, nil
}

func LoadGolden(root, split string) ([]domain.GoldenCase, error) {
	directory := filepath.Join(root, split)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read golden directory: %w", err)
	}
	var cases []domain.GoldenCase
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		var golden domain.GoldenCase
		if err := json.Unmarshal(data, &golden); err != nil {
			return nil, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		if golden.Split != split {
			return nil, fmt.Errorf("case %q belongs to split %q, loaded from %q", golden.ID, golden.Split, split)
		}
		cases = append(cases, golden)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

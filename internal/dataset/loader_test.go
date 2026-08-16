package dataset

import (
	"path/filepath"
	"testing"
)

func TestLoadCorpusAndGoldenSplits(t *testing.T) {
	root := filepath.Join("..", "..", "datasets")
	documents, err := LoadCorpus(filepath.Join(root, "corpus", "acmecloud"))
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 24 {
		t.Fatalf("expected 24 documents, got %d", len(documents))
	}
	cases, err := LoadGolden(filepath.Join(root, "golden"), "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 20 {
		t.Fatalf("expected 20 development cases, got %d", len(cases))
	}
	challenge, err := LoadGolden(filepath.Join(root, "golden"), "v4-challenge")
	if err != nil {
		t.Fatal(err)
	}
	if len(challenge) != 8 {
		t.Fatalf("expected 8 V4 challenge cases, got %d", len(challenge))
	}
	if err := Validate(documents, challenge); err != nil {
		t.Fatalf("validate V4 challenge: %v", err)
	}
}

func TestLoadMedicalDeviceDomain(t *testing.T) {
	root := filepath.Join("..", "..", "datasets", "domains", "medical-device")
	documents, err := LoadCorpus(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 17 {
		t.Fatalf("expected 17 medical-device documents, got %d", len(documents))
	}
	cases, err := LoadGolden(filepath.Join(root, "golden"), "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 21 {
		t.Fatalf("expected 21 medical-device development cases, got %d", len(cases))
	}
	if err := Validate(documents, cases); err != nil {
		t.Fatalf("validate medical-device domain: %v", err)
	}
}

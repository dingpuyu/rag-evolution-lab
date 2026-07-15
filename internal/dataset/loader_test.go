package dataset

import (
	"path/filepath"
	"testing"
)

func TestLoadCorpusAndGoldenDevelopmentSet(t *testing.T) {
	root := filepath.Join("..", "..", "datasets")
	documents, err := LoadCorpus(filepath.Join(root, "corpus", "acmecloud"))
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 13 {
		t.Fatalf("expected 13 documents, got %d", len(documents))
	}
	cases, err := LoadGolden(filepath.Join(root, "golden"), "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 20 {
		t.Fatalf("expected 20 development cases, got %d", len(cases))
	}
}

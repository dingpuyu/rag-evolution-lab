package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDatasetRootsUsesMedicalDomain(t *testing.T) {
	projectRoot := filepath.Join("..", "..")
	t.Setenv("RAGLAB_DATASET_DOMAIN", "medical-device")
	corpusRoot, goldenRoot, err := resolveDatasetRoots(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(corpusRoot, filepath.Join("datasets", "domains", "medical-device", "corpus")) {
		t.Fatalf("unexpected corpus root %q", corpusRoot)
	}
	if !strings.HasSuffix(goldenRoot, filepath.Join("datasets", "domains", "medical-device", "golden")) {
		t.Fatalf("unexpected golden root %q", goldenRoot)
	}
}

func TestResolveDatasetRootsRejectsTraversal(t *testing.T) {
	t.Setenv("RAGLAB_DATASET_DOMAIN", "../medical-device")
	_, _, err := resolveDatasetRoots(filepath.Join("..", ".."))
	if err == nil {
		t.Fatal("expected traversal-like domain to be rejected")
	}
}

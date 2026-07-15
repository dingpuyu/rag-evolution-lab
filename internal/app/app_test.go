package app

import (
	"path/filepath"
	"testing"
)

func TestBuildRegistersHybridExperimentPipelines(t *testing.T) {
	runtime, err := Build(filepath.Join("..", "..", "datasets", "corpus", "acmecloud"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"v3-hybrid",
		"v3-hybrid-metadata",
		"v3-hybrid-metadata-consensus",
		"v4-router",
	} {
		if _, err := runtime.Pipeline(name); err != nil {
			t.Errorf("expected pipeline %q to be registered: %v", name, err)
		}
	}
}

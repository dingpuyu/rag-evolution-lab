package scalebench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type seedCheckpoint struct {
	Version     int           `json:"version"`
	Dataset     DatasetConfig `json:"dataset"`
	Collections Collections   `json:"collections"`
	NextOffset  int           `json:"next_offset"`
	Completed   bool          `json:"completed"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

func readCheckpoint(path string) (seedCheckpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return seedCheckpoint{}, fmt.Errorf("read checkpoint %s: %w", path, err)
	}
	var checkpoint seedCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return seedCheckpoint{}, fmt.Errorf("decode checkpoint %s: %w", path, err)
	}
	if checkpoint.Version != 1 {
		return seedCheckpoint{}, fmt.Errorf("unsupported checkpoint version %d", checkpoint.Version)
	}
	return checkpoint, nil
}

func writeCheckpoint(path string, checkpoint seedCheckpoint) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	checkpoint.Version = 1
	checkpoint.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".scale-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create checkpoint temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish checkpoint: %w", err)
	}
	return nil
}

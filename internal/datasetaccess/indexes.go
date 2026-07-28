package datasetaccess

import (
	"context"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

type IndexRelease struct {
	ReleaseID     string    `json:"release_id"`
	ApplicationID string    `json:"app_id"`
	EnvironmentID string    `json:"environment_id"`
	Version       string    `json:"version"`
	Collection    string    `json:"collection"`
	Alias         string    `json:"alias"`
	State         string    `json:"state"`
	PublishedBy   string    `json:"published_by"`
	PublishedAt   time.Time `json:"published_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type PublishIndex struct {
	EnvironmentID string `json:"environment_id"`
	Version       string `json:"version"`
	Collection    string `json:"collection"`
	Alias         string `json:"alias,omitempty"`
}

type IndexStore interface {
	VisibleIndexReleases(context.Context, auth.Identity, string, string) ([]IndexRelease, error)
	PublishIndexRelease(context.Context, auth.Identity, string, PublishIndex) (IndexRelease, error)
	RollbackIndexRelease(context.Context, auth.Identity, string, string, string) (IndexRelease, error)
	ResolveIndexRelease(context.Context, auth.Identity, string, string) (IndexRelease, error)
}

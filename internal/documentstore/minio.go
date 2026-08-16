package documentstore

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store interface {
	Put(context.Context, string, string, []byte) (string, error)
}

type MinIO struct {
	client *minio.Client
	bucket string
}

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseTLS    bool
}

func NewMinIO(config Config) (*MinIO, error) {
	client, err := minio.New(strings.TrimSpace(config.Endpoint), &minio.Options{
		Creds: credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""), Secure: config.UseTLS,
	})
	if err != nil {
		return nil, err
	}
	bucket := strings.TrimSpace(config.Bucket)
	if bucket == "" {
		bucket = "raglab-documents"
	}
	return &MinIO{client: client, bucket: bucket}, nil
}

func (store *MinIO) Put(ctx context.Context, key, contentType string, data []byte) (string, error) {
	exists, err := store.client.BucketExists(ctx, store.bucket)
	if err != nil {
		return "", fmt.Errorf("check document bucket: %w", err)
	}
	if !exists {
		if err := store.client.MakeBucket(ctx, store.bucket, minio.MakeBucketOptions{}); err != nil {
			return "", fmt.Errorf("create document bucket: %w", err)
		}
	}
	_, err = store.client.PutObject(ctx, store.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("store source document: %w", err)
	}
	return "s3://" + store.bucket + "/" + key, nil
}

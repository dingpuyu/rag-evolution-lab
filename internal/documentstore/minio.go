package documentstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store interface {
	Put(context.Context, string, string, []byte) (string, error)
	Get(context.Context, string, int64) (Object, error)
}

// Object is an authorized object-store read. Callers pass the URI previously
// returned by Put; arbitrary buckets and non-S3 schemes are rejected.
type Object struct {
	URI         string
	ContentType string
	Data        []byte
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

func (store *MinIO) Get(ctx context.Context, uri string, maxBytes int64) (Object, error) {
	bucket, key, err := parseObjectURI(uri)
	if err != nil {
		return Object{}, err
	}
	if bucket != store.bucket {
		return Object{}, fmt.Errorf("object bucket %q is outside the configured document store", bucket)
	}
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	object, err := store.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return Object{}, fmt.Errorf("open document object: %w", err)
	}
	defer object.Close()
	stat, err := object.Stat()
	if err != nil {
		return Object{}, fmt.Errorf("stat document object: %w", err)
	}
	if stat.Size > maxBytes {
		return Object{}, fmt.Errorf("document object exceeds preview limit of %d bytes", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	if err != nil {
		return Object{}, fmt.Errorf("read document object: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return Object{}, fmt.Errorf("document object exceeds preview limit of %d bytes", maxBytes)
	}
	return Object{URI: uri, ContentType: stat.ContentType, Data: data}, nil
}

func parseObjectURI(raw string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "s3" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("invalid document object URI")
	}
	key := strings.TrimPrefix(parsed.EscapedPath(), "/")
	decoded, err := url.PathUnescape(key)
	if err != nil || decoded == "" || strings.ContainsRune(decoded, '\x00') {
		return "", "", fmt.Errorf("invalid document object key")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", "", fmt.Errorf("invalid document object key")
		}
	}
	cleaned := path.Clean("/" + decoded)
	if cleaned == "/" || strings.HasPrefix(cleaned, "/../") || cleaned == "/.." {
		return "", "", fmt.Errorf("invalid document object key")
	}
	return parsed.Host, strings.TrimPrefix(cleaned, "/"), nil
}

package storage

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/WindAdherent/llm-platform/internal/config"
)

type ObjectStorage struct {
	Client *minio.Client
	Bucket string
	UseSSL bool
}

type UploadedObject struct {
	Bucket      string `json:"bucket"`
	ObjectKey   string `json:"object_key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag"`
}

type ListedObject struct {
	ObjectKey    string    `json:"object_key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
	ContentType  string    `json:"content_type"`
	ETag         string    `json:"etag"`
}

func ConnectMinIO(ctx context.Context, cfg config.Config) (*ObjectStorage, error) {
	useSSL, err := strconv.ParseBool(cfg.MinIOUseSSL)
	if err != nil {
		return nil, fmt.Errorf("invalid MINIO_USE_SSL: %w", err)
	}

	client, err := minio.New(cfg.MinIOEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinIOAccessKey, cfg.MinIOSecretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}

	store := &ObjectStorage{
		Client: client,
		Bucket: cfg.MinIOBucket,
		UseSSL: useSSL,
	}

	if err := store.Health(ctx); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *ObjectStorage) Health(ctx context.Context) error {
	exists, err := s.Client.BucketExists(ctx, s.Bucket)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	return nil
}

func (s *ObjectStorage) EnsureBucket(ctx context.Context) error {
	exists, err := s.Client.BucketExists(ctx, s.Bucket)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	return s.Client.MakeBucket(ctx, s.Bucket, minio.MakeBucketOptions{})
}

func (s *ObjectStorage) Upload(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	contentType string,
) (*UploadedObject, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	info, err := s.Client.PutObject(ctx, s.Bucket, objectKey, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}

	return &UploadedObject{
		Bucket:      s.Bucket,
		ObjectKey:   objectKey,
		Size:        info.Size,
		ContentType: contentType,
		ETag:        info.ETag,
	}, nil
}

func (s *ObjectStorage) PresignedGetURL(ctx context.Context, objectKey string, expires time.Duration) (string, error) {
	url, err := s.Client.PresignedGetObject(ctx, s.Bucket, objectKey, expires, nil)
	if err != nil {
		return "", err
	}

	return url.String(), nil
}

func (s *ObjectStorage) List(ctx context.Context, prefix string, recursive bool) ([]ListedObject, error) {
	objectCh := s.Client.ListObjects(ctx, s.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: recursive,
	})

	items := make([]ListedObject, 0)

	for object := range objectCh {
		if object.Err != nil {
			return nil, object.Err
		}

		items = append(items, ListedObject{
			ObjectKey:    object.Key,
			Size:         object.Size,
			LastModified: object.LastModified,
			ContentType:  object.ContentType,
			ETag:         object.ETag,
		})
	}

	return items, nil
}

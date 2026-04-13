package db

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func NewMinIOClient(ctx context.Context, cfg config.MinIOConfig) (*minio.Client, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	logger.Log.Info().Str("endpoint", cfg.Endpoint).Str("bucket", cfg.Bucket).Msg("connected to minio")
	return client, nil
}

// NewMinIOPublicSigner returns a minio client configured with the public-facing
// endpoint. It is used only to generate presigned URLs that clients outside
// the Docker network can reach. It does NOT verify bucket existence.
func NewMinIOPublicSigner(cfg config.MinIOConfig) (*minio.Client, error) {
	endpoint := cfg.PublicEndpoint
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		// Setting Region avoids the initial GetBucketLocation HTTP call;
		// this signer only generates URLs and must never touch the network.
		Region: "us-east-1",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio public signer: %w", err)
	}
	return client, nil
}

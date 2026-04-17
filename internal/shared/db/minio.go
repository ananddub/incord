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

	// Anonymous read on the whole bucket so download URLs don't need
	// signing. Presigned PUT is still required for uploads (the policy only
	// grants GetObject), and the only way to obtain the stable long-lived
	// URL for a file is to go through the media service — so this is still
	// effectively "URL-as-capability" access control.
	publicReadPolicy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Sid": "PublicRead",
			"Effect": "Allow",
			"Principal": {"AWS": ["*"]},
			"Action": ["s3:GetObject"],
			"Resource": ["arn:aws:s3:::%s/*"]
		}]
	}`, cfg.Bucket)
	if err := client.SetBucketPolicy(ctx, cfg.Bucket, publicReadPolicy); err != nil {
		// Non-fatal: if the backend doesn't support bucket policies
		// (minor rustfs variants), fall back to the presigned GET path —
		// media.Service will still generate 7-day URLs.
		logger.Log.Warn().Err(err).Str("bucket", cfg.Bucket).Msg("failed to set public read policy; falling back to signed GET URLs")
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

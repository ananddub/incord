package media

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
)

const (
	presignedURLExpiry = 15 * time.Minute
	downloadURLExpiry  = 1 * time.Hour
)

// Service contains the business logic for the media feature.
type Service struct {
	repo   *Repository
	minio  *minio.Client
	bucket string
}

// NewService creates a new media Service.
func NewService(repo *Repository, minioClient *minio.Client, minioCfg config.MinIOConfig) *Service {
	return &Service{
		repo:   repo,
		minio:  minioClient,
		bucket: minioCfg.Bucket,
	}
}

// RequestUploadResult holds the result of a RequestUpload operation.
type RequestUploadResult struct {
	UploadID  string
	UploadURL string
}

// RequestUpload creates a DB record for the pending upload and generates a presigned PUT URL.
func (s *Service) RequestUpload(ctx context.Context, userID, filename, contentType string, size int64) (*RequestUploadResult, error) {
	// Generate a unique object key
	objectKey := fmt.Sprintf("uploads/%s/%s_%s", userID, uuid.New().String(), filename)

	// Create the DB record (unconfirmed)
	mediaFile, err := s.repo.CreateMediaFile(ctx, userID, filename, contentType, size, objectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create media file record: %w", err)
	}

	// Generate presigned PUT URL
	presignedURL, err := s.minio.PresignedPutObject(ctx, s.bucket, objectKey, presignedURLExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to generate presigned PUT URL: %w", err)
	}

	// Extract the file ID as string
	fileID := uuidToString(mediaFile.ID)

	return &RequestUploadResult{
		UploadID:  fileID,
		UploadURL: presignedURL.String(),
	}, nil
}

// ConfirmUploadResult holds the result of a ConfirmUpload operation.
type ConfirmUploadResult struct {
	FileID string
	URL    string
}

// ConfirmUpload marks a media file as confirmed and returns a download URL.
func (s *Service) ConfirmUpload(ctx context.Context, uploadID string) (*ConfirmUploadResult, error) {
	mediaFile, err := s.repo.ConfirmMediaFile(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm media file: %w", err)
	}

	// Generate a download URL for the confirmed file
	dlURL, err := s.minio.PresignedGetObject(ctx, s.bucket, mediaFile.BucketKey, downloadURLExpiry, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("failed to generate download URL: %w", err)
	}

	fileID := uuidToString(mediaFile.ID)

	return &ConfirmUploadResult{
		FileID: fileID,
		URL:    dlURL.String(),
	}, nil
}

// GetDownloadURLResult holds the result of a GetDownloadURL operation.
type GetDownloadURLResult struct {
	URL       string
	ExpiresAt time.Time
}

// GetDownloadURL generates a presigned GET URL for a file.
func (s *Service) GetDownloadURL(ctx context.Context, fileID string) (*GetDownloadURLResult, error) {
	mediaFile, err := s.repo.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to get media file: %w", err)
	}

	if !mediaFile.Confirmed {
		return nil, fmt.Errorf("file upload not yet confirmed")
	}

	expiresAt := time.Now().Add(downloadURLExpiry)
	dlURL, err := s.minio.PresignedGetObject(ctx, s.bucket, mediaFile.BucketKey, downloadURLExpiry, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("failed to generate download URL: %w", err)
	}

	return &GetDownloadURLResult{
		URL:       dlURL.String(),
		ExpiresAt: expiresAt,
	}, nil
}

// DeleteFile removes the file from MinIO and deletes the DB record.
func (s *Service) DeleteFile(ctx context.Context, fileID string) error {
	mediaFile, err := s.repo.GetMediaFile(ctx, fileID)
	if err != nil {
		return fmt.Errorf("failed to get media file: %w", err)
	}

	// Remove from MinIO
	if err := s.minio.RemoveObject(ctx, s.bucket, mediaFile.BucketKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("failed to remove object from MinIO: %w", err)
	}

	// Delete from DB
	if err := s.repo.DeleteMediaFile(ctx, fileID); err != nil {
		return fmt.Errorf("failed to delete media file record: %w", err)
	}

	return nil
}

// uuidToString converts a pgtype.UUID to its standard string representation.
func uuidToString(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

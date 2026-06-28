package media

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
)

const (
	presignedURLExpiry = 15 * time.Minute
	// 7 days is the hard cap the minio-go client enforces (matches the AWS
	// SigV4 spec). For practically "forever" download URLs the bucket needs
	// anonymous-read policy — see MakeBucketPublic below — and we stitch
	// together a plain URL instead of a presigned one.
	downloadURLExpiry = 7 * 24 * time.Hour
)

// Service contains the business logic for the media feature.
type Service struct {
	repo          *Repository
	minio         *minio.Client // internal endpoint — used for direct object ops
	signer        *minio.Client // public endpoint — used for signing client-facing URLs
	bucket        string
	publicBaseURL string // pre-built "http(s)://<public-endpoint>/<bucket>/" prefix
}

// NewService creates a new media Service. `signer` is a MinIO client
// configured with the publicly-reachable endpoint used for presigned URL
// generation; if nil, `minioClient` is used for both.
func NewService(repo *Repository, minioClient, signer *minio.Client, minioCfg config.MinIOConfig) *Service {
	if signer == nil {
		signer = minioClient
	}
	// Build a plain anonymous-access URL prefix. Because we set a public-read
	// bucket policy at startup, clients can fetch these without any
	// signature, and the URL stays valid indefinitely.
	scheme := "http://"
	if minioCfg.UseSSL {
		scheme = "https://"
	}
	endpoint := minioCfg.PublicEndpoint
	if endpoint == "" {
		endpoint = minioCfg.Endpoint
	}
	return &Service{
		repo:          repo,
		minio:         minioClient,
		signer:        signer,
		bucket:        minioCfg.Bucket,
		publicBaseURL: scheme + endpoint + "/" + minioCfg.Bucket + "/",
	}
}

// publicURL returns the anonymous-access URL for an object in the bucket.
// Relies on the bucket having a public-read policy set at startup time.
func (s *Service) publicURL(key string) string {
	return s.publicBaseURL + key
}

// RequestUploadResult holds the result of a RequestUpload operation.
type RequestUploadResult struct {
	UploadID  string
	UploadURL string
}

const maxFileSize = 25 * 1024 * 1024 // 25MB

var allowedContentTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"video/mp4":       true,
	"audio/ogg":       true,
	"audio/mpeg":      true,
	"application/pdf": true,
}

// RequestUpload creates a DB record for the pending upload and generates a presigned PUT URL.
func (s *Service) RequestUpload(ctx context.Context, userID, filename, contentType string, size int64) (*RequestUploadResult, error) {
	if size > maxFileSize {
		return nil, ErrFileTooLarge
	}
	if !allowedContentTypes[contentType] {
		return nil, ErrInvalidContentType
	}

	// Generate a unique object key
	objectKey := fmt.Sprintf("uploads/%s/%s_%s", userID, uuid.New().String(), filename)

	// Create the DB record (unconfirmed)
	mediaFile, err := s.repo.CreateMediaFile(ctx, userID, filename, contentType, size, objectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create media file record: %w", err)
	}

	// Generate presigned PUT URL
	presignedURL, err := s.signer.PresignedPutObject(ctx, s.bucket, objectKey, presignedURLExpiry)
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

	fileID := uuidToString(mediaFile.ID)

	return &ConfirmUploadResult{
		FileID: fileID,
		URL:    s.publicURL(mediaFile.BucketKey),
	}, nil
}

// GetDownloadURLResult holds the result of a GetDownloadURL operation.
type GetDownloadURLResult struct {
	URL       string
	ExpiresAt time.Time
}

// GetDownloadURL returns the anonymous-access download URL for a file.
// With the public-read bucket policy set at startup the URL has no
// built-in expiry, so we report a far-future ExpiresAt (~1 year) purely
// so legacy clients can still render a "link expires on" hint.
func (s *Service) GetDownloadURL(ctx context.Context, fileID string) (*GetDownloadURLResult, error) {
	mediaFile, err := s.repo.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to get media file: %w", err)
	}

	if !mediaFile.Confirmed {
		return nil, fmt.Errorf("file upload not yet confirmed")
	}

	return &GetDownloadURLResult{
		URL:       s.publicURL(mediaFile.BucketKey),
		ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
	}, nil
}

// ResolveAttachment returns the metadata + presigned download URL for a
// confirmed media file owned by uploaderID. Used by the message service
// when persisting attachments alongside a message.
func (s *Service) ResolveAttachment(ctx context.Context, fileID, uploaderID string) (string, string, string, int64, error) {
	mediaFile, err := s.repo.GetMediaFile(ctx, fileID)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("failed to get media file: %w", err)
	}
	if !mediaFile.Confirmed {
		return "", "", "", 0, fmt.Errorf("file upload not yet confirmed")
	}
	// Ownership check — a client can only attach files they themselves uploaded.
	if uploaderID != "" && mediaFile.UploaderID.String() != uploaderID {
		var uploaderUUID pgtype.UUID
		if err := uploaderUUID.Scan(uploaderID); err != nil || mediaFile.UploaderID != uploaderUUID {
			return "", "", "", 0, fmt.Errorf("attachment owned by a different user")
		}
	}

	return mediaFile.Filename, s.publicURL(mediaFile.BucketKey), mediaFile.ContentType, mediaFile.Size, nil
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

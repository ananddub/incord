package media

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

// Repository wraps the sqlc-generated Queries for media file operations.
type Repository struct {
	q *db.Queries
}

// NewRepository creates a new media Repository.
func NewRepository(q *db.Queries) *Repository {
	return &Repository{q: q}
}

// CreateMediaFile inserts a new media file record into the database.
func (r *Repository) CreateMediaFile(ctx context.Context, uploaderID string, filename, contentType string, size int64, bucketKey string) (db.MediaFile, error) {
	uid, err := parseUUID(uploaderID)
	if err != nil {
		return db.MediaFile{}, fmt.Errorf("invalid uploader_id: %w", err)
	}

	return r.q.CreateMediaFile(ctx, db.CreateMediaFileParams{
		UploaderID:  uid,
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		BucketKey:   bucketKey,
	})
}

// GetMediaFile retrieves a media file record by its ID.
func (r *Repository) GetMediaFile(ctx context.Context, fileID string) (db.MediaFile, error) {
	uid, err := parseUUID(fileID)
	if err != nil {
		return db.MediaFile{}, fmt.Errorf("invalid file_id: %w", err)
	}

	return r.q.GetMediaFile(ctx, uid)
}

// ConfirmMediaFile marks a media file as confirmed (upload complete).
func (r *Repository) ConfirmMediaFile(ctx context.Context, fileID string) (db.MediaFile, error) {
	uid, err := parseUUID(fileID)
	if err != nil {
		return db.MediaFile{}, fmt.Errorf("invalid file_id: %w", err)
	}

	return r.q.ConfirmMediaFile(ctx, uid)
}

// DeleteMediaFile removes a media file record from the database.
func (r *Repository) DeleteMediaFile(ctx context.Context, fileID string) error {
	uid, err := parseUUID(fileID)
	if err != nil {
		return fmt.Errorf("invalid file_id: %w", err)
	}

	return r.q.DeleteMediaFile(ctx, uid)
}

// parseUUID converts a string UUID to pgtype.UUID.
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, err
	}
	return u, nil
}

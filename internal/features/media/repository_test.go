package media

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gendb "github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupRepo(t *testing.T) (*Repository, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)
	q := gendb.New(infra.Pool)
	repo := NewRepository(q)
	return repo, infra
}

// createTestUser inserts a user row and returns its UUID string.
func createTestUser(t *testing.T, infra *testutil.TestInfra) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := infra.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id`,
		"mediauser", "media@example.com", "hash",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestCreateAndGetMediaFile(t *testing.T) {
	repo, infra := setupRepo(t)
	ctx := context.Background()
	userID := createTestUser(t, infra)

	created, err := repo.CreateMediaFile(ctx, userID, "photo.png", "image/png", 1024, "uploads/photo.png")
	require.NoError(t, err)

	assert.Equal(t, "photo.png", created.Filename)
	assert.Equal(t, "image/png", created.ContentType)
	assert.Equal(t, int64(1024), created.Size)
	assert.Equal(t, "uploads/photo.png", created.BucketKey)
	assert.False(t, created.Confirmed)

	// Read it back by ID.
	fileID := uuidToString(created.ID)
	got, err := repo.GetMediaFile(ctx, fileID)
	require.NoError(t, err)
	assert.Equal(t, created.Filename, got.Filename)
	assert.Equal(t, created.BucketKey, got.BucketKey)
}

func TestConfirmMediaFile(t *testing.T) {
	repo, infra := setupRepo(t)
	ctx := context.Background()
	userID := createTestUser(t, infra)

	created, err := repo.CreateMediaFile(ctx, userID, "doc.pdf", "application/pdf", 2048, "uploads/doc.pdf")
	require.NoError(t, err)
	assert.False(t, created.Confirmed)

	fileID := uuidToString(created.ID)
	confirmed, err := repo.ConfirmMediaFile(ctx, fileID)
	require.NoError(t, err)
	assert.True(t, confirmed.Confirmed)

	// Verify persistence.
	got, err := repo.GetMediaFile(ctx, fileID)
	require.NoError(t, err)
	assert.True(t, got.Confirmed)
}

func TestDeleteMediaFile(t *testing.T) {
	repo, infra := setupRepo(t)
	ctx := context.Background()
	userID := createTestUser(t, infra)

	created, err := repo.CreateMediaFile(ctx, userID, "tmp.txt", "text/plain", 100, "uploads/tmp.txt")
	require.NoError(t, err)

	fileID := uuidToString(created.ID)
	err = repo.DeleteMediaFile(ctx, fileID)
	require.NoError(t, err)

	_, err = repo.GetMediaFile(ctx, fileID)
	assert.Error(t, err) // row no longer exists
}

func TestGetMediaFile_NotFound(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	_, err := repo.GetMediaFile(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
}

func TestCreateMediaFile_InvalidUploaderID(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	_, err := repo.CreateMediaFile(ctx, "not-a-uuid", "f.txt", "text/plain", 1, "k")
	assert.Error(t, err)
}

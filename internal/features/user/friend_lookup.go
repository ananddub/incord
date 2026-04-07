package user

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// FriendLookup implements gateway.FriendLookup using the user repository.
type FriendLookup struct {
	repo *Repository
}

func NewFriendLookup(repo *Repository) *FriendLookup {
	return &FriendLookup{repo: repo}
}

func (f *FriendLookup) GetFriendIDs(ctx context.Context, userID string) ([]string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}

	pgID := pgtype.UUID{Bytes: uid, Valid: true}
	friends, err := f.repo.ListFriends(ctx, pgID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(friends))
	for _, f := range friends {
		ids = append(ids, uuid.UUID(f.ID.Bytes).String())
	}
	return ids, nil
}

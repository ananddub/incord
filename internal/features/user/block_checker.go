package user

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

// BlockChecker implements message.BlockChecker using the user repository.
type BlockChecker struct {
	repo *Repository
}

func NewBlockChecker(repo *Repository) *BlockChecker {
	return &BlockChecker{repo: repo}
}

// IsBlocked returns true if userID has blocked targetID.
func (b *BlockChecker) IsBlocked(ctx context.Context, userID, targetID string) bool {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	tid, err := uuid.Parse(targetID)
	if err != nil {
		return false
	}

	pgUser := pgtype.UUID{Bytes: uid, Valid: true}
	pgTarget := pgtype.UUID{Bytes: tid, Valid: true}

	friendship, err := b.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   pgUser,
		FriendID: pgTarget,
	})
	if err != nil {
		return false
	}

	return friendship.Status == "blocked"
}

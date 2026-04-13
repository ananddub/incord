package stream

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

// Resolver implements UserDataResolver using sqlc-generated queries.
type Resolver struct {
	q *db.Queries
}

// NewResolver creates a Resolver backed by the given sqlc Queries.
func NewResolver(q *db.Queries) *Resolver {
	return &Resolver{q: q}
}

func (r *Resolver) GetUserGuildIDs(ctx context.Context, userID string) ([]string, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	guilds, err := r.q.ListUserGuilds(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list user guilds: %w", err)
	}
	ids := make([]string, 0, len(guilds))
	for _, g := range guilds {
		ids = append(ids, uuidToString(g.ID))
	}
	return ids, nil
}

// parseUUID converts a string to pgtype.UUID.
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}

// uuidToString converts a pgtype.UUID to string.
func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

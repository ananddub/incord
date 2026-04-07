package permissions

import "context"

// Checker resolves permissions for a user in a guild.
type Checker interface {
	HasPermission(ctx context.Context, userID, guildID string, perm int64) bool
}

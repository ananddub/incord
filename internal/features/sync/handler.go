package sync

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ananddub/ndiscord_backend/gen/db"
	syncv1 "github.com/ananddub/ndiscord_backend/gen/sync/v1"
	"github.com/ananddub/ndiscord_backend/internal/features/message"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

// PresenceReader returns the user's *live* presence (Redis-stored broadcast
// state — offline when their StreamFriendActivity session is closed, else
// the last status they chose). Used so SyncUsers responses reflect actual
// connectivity instead of the DB-persisted intent, which would say "online"
// for a friend who hasn't actually opened their app.
type PresenceReader interface {
	GetLiveStatus(ctx context.Context, userID string) (status, customStatus string)
}

type Handler struct {
	syncv1.UnimplementedSyncServiceServer
	q        *db.Queries
	msgRepo  *message.Repository
	presence PresenceReader
}

func NewHandler(q *db.Queries, msgRepo *message.Repository) *Handler {
	return &Handler{q: q, msgRepo: msgRepo}
}

// SetPresenceReader wires a live-presence reader so SyncUsers stamps
// Redis-derived status (actual online/offline) instead of the DB intent.
func (h *Handler) SetPresenceReader(p PresenceReader) { h.presence = p }

func (h *Handler) Sync(ctx context.Context, req *syncv1.SyncRequest) (*syncv1.SyncResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid user id")
	}
	pgUID := pgtype.UUID{Bytes: uid, Valid: true}

	lastSync := time.Unix(0, 0) // epoch = get everything
	if req.LastSyncTime != nil {
		lastSync = req.LastSyncTime.AsTime()
	}
	pgLastSync := pgtype.Timestamptz{Time: lastSync, Valid: true}

	resp := &syncv1.SyncResponse{
		ServerTime: timestamppb.Now(),
	}

	// Users. Status comes from Redis (live presence) when a PresenceReader
	// is wired — this way an offline friend shows up as "offline" in the
	// sync response even though their DB intent column still says "online".
	// The DB intent is the user's *preference* and is restored to Redis
	// only when they open StreamFriendActivity.
	users, err := h.q.SyncUsers(ctx, db.SyncUsersParams{UpdatedAt: pgLastSync, ID: pgUID})
	if err == nil {
		for _, u := range users {
			statusStr := u.Status
			if h.presence != nil {
				if live, _ := h.presence.GetLiveStatus(ctx, ustr(u.ID)); live != "" {
					statusStr = live
				}
			}
			resp.Users = append(resp.Users, &syncv1.SyncUser{
				Id: ustr(u.ID), IsDeleted: u.Deleted, UpdatedAt: ts(u.UpdatedAt),
				Username: u.Username, Email: u.Email, AvatarUrl: u.AvatarUrl,
				Bio: u.Bio, Status: statusStr, Verified: u.Verified,
				BackgroundColor: u.BackgroundColor,
			})
		}
	}

	// Guilds
	guilds, err := h.q.SyncGuilds(ctx, db.SyncGuildsParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, g := range guilds {
			resp.Guilds = append(resp.Guilds, &syncv1.SyncGuild{
				Id: ustr(g.ID), IsDeleted: g.Deleted, UpdatedAt: ts(g.UpdatedAt),
				Name: g.Name, Description: g.Description, IconUrl: g.IconUrl, OwnerId: ustr(g.OwnerID),
			})
		}
	}

	// Channels
	channels, err := h.q.SyncChannels(ctx, db.SyncChannelsParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, c := range channels {
			resp.Channels = append(resp.Channels, &syncv1.SyncChannel{
				Id: ustr(c.ID), IsDeleted: c.Deleted, UpdatedAt: ts(c.UpdatedAt),
				GuildId: ustrNullable(c.GuildID), Name: c.Name, Type: c.Type,
				Topic: c.Topic, Position: c.Position, ParentId: ustrNullable(c.ParentID),
			})
		}
	}

	// Roles
	roles, err := h.q.SyncRoles(ctx, db.SyncRolesParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, r := range roles {
			resp.Roles = append(resp.Roles, &syncv1.SyncRole{
				Id: ustr(r.ID), IsDeleted: r.Deleted, UpdatedAt: ts(r.UpdatedAt),
				GuildId: ustr(r.GuildID), Name: r.Name, Color: r.Color, Position: r.Position,
			})
		}
	}

	// Guild members
	members, err := h.q.SyncGuildMembers(ctx, db.SyncGuildMembersParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, m := range members {
			resp.GuildMembers = append(resp.GuildMembers, &syncv1.SyncGuildMember{
				GuildId: ustr(m.GuildID), UserId: ustr(m.UserID), IsDeleted: m.Deleted,
				UpdatedAt: ts(m.UpdatedAt), Nickname: m.Nickname,
			})
		}
	}

	// Friendships
	friendships, err := h.q.SyncFriendships(ctx, db.SyncFriendshipsParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, f := range friendships {
			resp.Friendships = append(resp.Friendships, &syncv1.SyncFriendship{
				UserId: ustr(f.UserID), FriendId: ustr(f.FriendID), IsDeleted: f.Deleted,
				UpdatedAt: ts(f.UpdatedAt), Status: f.Status,
			})
		}
	}

	// DM members
	dmMembers, err := h.q.SyncDmMembers(ctx, db.SyncDmMembersParams{UserID: pgUID, UpdatedAt: pgLastSync})
	if err == nil {
		for _, d := range dmMembers {
			resp.DmMembers = append(resp.DmMembers, &syncv1.SyncDmMember{
				ChannelId: ustr(d.ChannelID), UserId: ustr(d.UserID), IsDeleted: d.Deleted,
				UpdatedAt: ts(d.UpdatedAt),
			})
		}
	}

	// Messages (ScyllaDB) - fetch from all user's channels
	if h.msgRepo != nil {
		// Get user's guild channels + DM channels
		var channelIDs []gocql.UUID

		// Guild channels
		guildsForMsg, _ := h.q.SyncGuilds(ctx, db.SyncGuildsParams{UserID: pgUID, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true}})
		for _, g := range guildsForMsg {
			chs, _ := h.q.ListGuildChannels(ctx, g.ID)
			for _, c := range chs {
				if cid, err := gocql.ParseUUID(ustr(c.ID)); err == nil {
					channelIDs = append(channelIDs, cid)
				}
			}
		}

		// DM channels
		dms, _ := h.q.ListDMChannels(ctx, pgUID)
		for _, d := range dms {
			if cid, err := gocql.ParseUUID(ustr(d.ID)); err == nil {
				channelIDs = append(channelIDs, cid)
			}
		}

		// Fetch messages since last sync from each channel
		for _, chID := range channelIDs {
			msgs, err := h.msgRepo.GetMessagesSince(ctx, chID, lastSync)
			if err != nil {
				continue
			}
			for _, m := range msgs {
				sm := &syncv1.SyncMessage{
					ChannelId: m.ChannelID.String(),
					Id:        m.ID.String(),
					IsDeleted: m.Deleted,
					AuthorId:  m.AuthorID.String(),
					Content:   m.Content,
					Type:      int32(m.Type),
					ReplyToId: m.ReplyToID.String(),
					Pinned:    m.Pinned,
				}
				if m.UpdatedAt != nil {
					sm.UpdatedAt = timestamppb.New(*m.UpdatedAt)
				}
				resp.Messages = append(resp.Messages, sm)
			}
		}
	}

	return resp, nil
}

func ustr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func ustrNullable(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func ts(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

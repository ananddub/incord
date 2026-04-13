package guild

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ananddub/ndiscord_backend/gen/db"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

type Handler struct {
	guildv1.UnimplementedGuildServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateGuild(ctx context.Context, req *guildv1.CreateGuildRequest) (*guildv1.CreateGuildResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	ownerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	guild, err := h.svc.CreateGuild(ctx, ownerPgID, req.GetName(), req.GetDescription(), req.GetIconUrl())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create guild")
	}

	return &guildv1.CreateGuildResponse{
		Guild: h.toProto(ctx, guild, 1),
	}, nil
}

func (h *Handler) GetGuild(ctx context.Context, req *guildv1.GetGuildRequest) (*guildv1.GetGuildResponse, error) {
	pgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	guild, memberCount, err := h.svc.GetGuild(ctx, pgID)
	if err != nil {
		if errors.Is(err, ErrGuildNotFound) {
			return nil, status.Error(codes.NotFound, "guild not found")
		}
		return nil, status.Error(codes.Internal, "failed to get guild")
	}

	return &guildv1.GetGuildResponse{
		Guild: h.toProto(ctx, guild, int32(memberCount)),
	}, nil
}

func (h *Handler) UploadGuildIcon(ctx context.Context, req *guildv1.UploadGuildIconRequest) (*guildv1.UploadGuildIconResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	guild, iconURL, err := h.svc.UploadGuildIcon(ctx, callerPgID, guildPgID, req.GetFilename(), req.GetContentType(), req.GetData())
	if err != nil {
		if errors.Is(err, ErrInsufficientPermissions) {
			return nil, status.Error(codes.PermissionDenied, "cannot manage this guild")
		}
		if errors.Is(err, ErrGuildNotFound) {
			return nil, status.Error(codes.NotFound, "guild not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &guildv1.UploadGuildIconResponse{
		IconUrl: iconURL,
		Guild:   h.toProto(ctx, guild, 0),
	}, nil
}

func (h *Handler) UpdateGuild(ctx context.Context, req *guildv1.UpdateGuildRequest) (*guildv1.UpdateGuildResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	params := db.UpdateGuildParams{
		ID:          guildPgID,
		Name:        req.Name,
		Description: req.Description,
		IconUrl:     req.IconUrl,
	}

	guild, err := h.svc.UpdateGuild(ctx, callerPgID, guildPgID, params)
	if err != nil {
		if errors.Is(err, ErrGuildNotFound) {
			return nil, status.Error(codes.NotFound, "guild not found")
		}
		if errors.Is(err, ErrNotGuildOwner) {
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can update the guild")
		}
		return nil, status.Error(codes.Internal, "failed to update guild")
	}

	count, _ := h.svc.repo.CountGuildMembers(ctx, guildPgID)
	return &guildv1.UpdateGuildResponse{
		Guild: h.toProto(ctx, guild, int32(count)),
	}, nil
}

func (h *Handler) DeleteGuild(ctx context.Context, req *guildv1.DeleteGuildRequest) (*guildv1.DeleteGuildResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	if err := h.svc.DeleteGuild(ctx, callerPgID, guildPgID); err != nil {
		if errors.Is(err, ErrGuildNotFound) {
			return nil, status.Error(codes.NotFound, "guild not found")
		}
		if errors.Is(err, ErrNotGuildOwner) {
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can delete the guild")
		}
		return nil, status.Error(codes.Internal, "failed to delete guild")
	}

	return &guildv1.DeleteGuildResponse{}, nil
}

func (h *Handler) ListUserGuilds(ctx context.Context, req *guildv1.ListUserGuildsRequest) (*guildv1.ListUserGuildsResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	guilds, err := h.svc.ListUserGuilds(ctx, pgID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list guilds")
	}

	protoGuilds := make([]*guildv1.Guild, len(guilds))
	for i, g := range guilds {
		protoGuilds[i] = h.toProto(ctx, g, 0)
	}

	return &guildv1.ListUserGuildsResponse{
		Guilds: protoGuilds,
	}, nil
}

func (h *Handler) JoinGuild(ctx context.Context, req *guildv1.JoinGuildRequest) (*guildv1.JoinGuildResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	guild, err := h.svc.JoinGuild(ctx, pgID, req.GetInviteCode())
	if err != nil {
		switch {
		case errors.Is(err, ErrInviteNotFound):
			return nil, status.Error(codes.NotFound, "invite not found")
		case errors.Is(err, ErrInviteExpired):
			return nil, status.Error(codes.FailedPrecondition, "invite has expired")
		case errors.Is(err, ErrInviteMaxUses):
			return nil, status.Error(codes.FailedPrecondition, "invite has reached maximum uses")
		case errors.Is(err, ErrUserBanned):
			return nil, status.Error(codes.PermissionDenied, "you are banned from this guild")
		case errors.Is(err, ErrAlreadyMember):
			return nil, status.Error(codes.AlreadyExists, "already a member of this guild")
		default:
			return nil, status.Error(codes.Internal, "failed to join guild")
		}
	}

	count, _ := h.svc.repo.CountGuildMembers(ctx, guild.ID)
	return &guildv1.JoinGuildResponse{
		Guild: h.toProto(ctx, guild, int32(count)),
	}, nil
}

func (h *Handler) LeaveGuild(ctx context.Context, req *guildv1.LeaveGuildRequest) (*guildv1.LeaveGuildResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	if err := h.svc.LeaveGuild(ctx, callerPgID, guildPgID); err != nil {
		switch {
		case errors.Is(err, ErrGuildNotFound):
			return nil, status.Error(codes.NotFound, "guild not found")
		case errors.Is(err, ErrOwnerCannotLeave):
			return nil, status.Error(codes.FailedPrecondition, "guild owner cannot leave the guild")
		case errors.Is(err, ErrNotGuildMember):
			return nil, status.Error(codes.NotFound, "not a member of this guild")
		default:
			return nil, status.Error(codes.Internal, "failed to leave guild")
		}
	}

	return &guildv1.LeaveGuildResponse{}, nil
}

func (h *Handler) ListMembers(ctx context.Context, req *guildv1.ListMembersRequest) (*guildv1.ListMembersResponse, error) {
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = 50
	}

	// Parse "after" cursor as offset (0-based)
	var offset int32
	if req.GetAfter() != "" {
		// If after is provided, treat as a numeric offset for simplicity
		// In a real implementation, this would be a cursor-based approach
	}

	members, err := h.svc.ListMembers(ctx, guildPgID, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list members")
	}

	protoMembers := make([]*guildv1.GuildMember, len(members))
	for i, m := range members {
		protoMembers[i] = dbGuildMemberToProto(m)
	}

	return &guildv1.ListMembersResponse{
		Members: protoMembers,
	}, nil
}

func (h *Handler) KickMember(ctx context.Context, req *guildv1.KickMemberRequest) (*guildv1.KickMemberResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	targetPgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	if err := h.svc.KickMember(ctx, callerPgID, guildPgID, targetPgID); err != nil {
		switch {
		case errors.Is(err, ErrGuildNotFound):
			return nil, status.Error(codes.NotFound, "guild not found")
		case errors.Is(err, ErrNotGuildOwner), errors.Is(err, ErrInsufficientPermissions):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can kick members")
		case errors.Is(err, ErrCannotKickOwner):
			return nil, status.Error(codes.InvalidArgument, "cannot kick the guild owner")
		case errors.Is(err, ErrMemberNotFound):
			return nil, status.Error(codes.NotFound, "member not found")
		default:
			return nil, status.Error(codes.Internal, "failed to kick member")
		}
	}

	return &guildv1.KickMemberResponse{}, nil
}

func (h *Handler) BanMember(ctx context.Context, req *guildv1.BanMemberRequest) (*guildv1.BanMemberResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	targetPgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	if err := h.svc.BanMember(ctx, callerPgID, guildPgID, targetPgID, req.GetReason()); err != nil {
		switch {
		case errors.Is(err, ErrGuildNotFound):
			return nil, status.Error(codes.NotFound, "guild not found")
		case errors.Is(err, ErrNotGuildOwner), errors.Is(err, ErrInsufficientPermissions):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can ban members")
		case errors.Is(err, ErrCannotBanOwner):
			return nil, status.Error(codes.InvalidArgument, "cannot ban the guild owner")
		default:
			return nil, status.Error(codes.Internal, "failed to ban member")
		}
	}

	return &guildv1.BanMemberResponse{}, nil
}

func (h *Handler) UnbanMember(ctx context.Context, req *guildv1.UnbanMemberRequest) (*guildv1.UnbanMemberResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	targetPgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	if err := h.svc.UnbanMember(ctx, callerPgID, guildPgID, targetPgID); err != nil {
		switch {
		case errors.Is(err, ErrGuildNotFound):
			return nil, status.Error(codes.NotFound, "guild not found")
		case errors.Is(err, ErrNotGuildOwner), errors.Is(err, ErrInsufficientPermissions):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can unban members")
		case errors.Is(err, ErrUserNotBanned):
			return nil, status.Error(codes.NotFound, "user is not banned")
		default:
			return nil, status.Error(codes.Internal, "failed to unban member")
		}
	}

	return &guildv1.UnbanMemberResponse{}, nil
}

func (h *Handler) CreateInvite(ctx context.Context, req *guildv1.CreateInviteRequest) (*guildv1.CreateInviteResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	var channelPgID pgtype.UUID
	if req.GetChannelId() != "" {
		channelPgID, err = parseUUID(req.GetChannelId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid channel_id")
		}
	}

	invite, err := h.svc.CreateInvite(ctx, callerPgID, guildPgID, channelPgID, req.GetMaxUses(), req.GetMaxAgeSeconds())
	if err != nil {
		if errors.Is(err, ErrNotGuildMember) {
			return nil, status.Error(codes.PermissionDenied, "must be a guild member to create invites")
		}
		return nil, status.Error(codes.Internal, "failed to create invite")
	}

	return &guildv1.CreateInviteResponse{
		Invite: dbInviteToProto(invite),
	}, nil
}

func (h *Handler) CreateRole(ctx context.Context, req *guildv1.CreateRoleRequest) (*guildv1.CreateRoleResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}

	role, err := h.svc.CreateRole(ctx, callerPgID, guildPgID, req.GetName(), req.GetColor())
	if err != nil {
		if errors.Is(err, ErrNotGuildOwner) {
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can create roles")
		}
		return nil, status.Error(codes.Internal, "failed to create role")
	}

	return &guildv1.CreateRoleResponse{
		Role: dbRoleToProto(role),
	}, nil
}

func (h *Handler) UpdateRole(ctx context.Context, req *guildv1.UpdateRoleRequest) (*guildv1.UpdateRoleResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	rolePgID, err := parseUUID(req.GetRoleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid role_id")
	}

	params := db.UpdateRoleParams{
		ID:       rolePgID,
		Name:     req.Name,
		Color:    req.Color,
		Position: req.Position,
	}
	// Permissions are managed via OpenFGA, not stored in DB

	role, err := h.svc.UpdateRole(ctx, callerPgID, guildPgID, params)
	if err != nil {
		if errors.Is(err, ErrNotGuildOwner) {
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can update roles")
		}
		return nil, status.Error(codes.Internal, "failed to update role")
	}

	return &guildv1.UpdateRoleResponse{
		Role: dbRoleToProto(role),
	}, nil
}

func (h *Handler) DeleteRole(ctx context.Context, req *guildv1.DeleteRoleRequest) (*guildv1.DeleteRoleResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	rolePgID, err := parseUUID(req.GetRoleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid role_id")
	}

	if err := h.svc.DeleteRole(ctx, callerPgID, guildPgID, rolePgID); err != nil {
		switch {
		case errors.Is(err, ErrNotGuildOwner), errors.Is(err, ErrInsufficientPermissions):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can delete roles")
		case errors.Is(err, ErrRoleNotFound):
			return nil, status.Error(codes.NotFound, "role not found")
		default:
			return nil, status.Error(codes.Internal, "failed to delete role")
		}
	}

	return &guildv1.DeleteRoleResponse{}, nil
}

func (h *Handler) AssignRole(ctx context.Context, req *guildv1.AssignRoleRequest) (*guildv1.AssignRoleResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	targetPgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}
	rolePgID, err := parseUUID(req.GetRoleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid role_id")
	}

	if err := h.svc.AssignRole(ctx, callerPgID, guildPgID, targetPgID, rolePgID); err != nil {
		switch {
		case errors.Is(err, ErrNotGuildOwner), errors.Is(err, ErrInsufficientPermissions):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can assign roles")
		case errors.Is(err, ErrMemberNotFound):
			return nil, status.Error(codes.NotFound, "member not found")
		case errors.Is(err, ErrRoleNotFound):
			return nil, status.Error(codes.NotFound, "role not found")
		default:
			return nil, status.Error(codes.Internal, "failed to assign role")
		}
	}

	return &guildv1.AssignRoleResponse{}, nil
}

func (h *Handler) RemoveRole(ctx context.Context, req *guildv1.RemoveRoleRequest) (*guildv1.RemoveRoleResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	targetPgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}
	rolePgID, err := parseUUID(req.GetRoleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid role_id")
	}

	if err := h.svc.RemoveRole(ctx, callerPgID, guildPgID, targetPgID, rolePgID); err != nil {
		if errors.Is(err, ErrNotGuildOwner) {
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can remove roles")
		}
		return nil, status.Error(codes.Internal, "failed to remove role")
	}

	return &guildv1.RemoveRoleResponse{}, nil
}

func (h *Handler) TransferOwnership(ctx context.Context, req *guildv1.TransferOwnershipRequest) (*guildv1.TransferOwnershipResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	callerPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	guildPgID, err := parseUUID(req.GetGuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid guild_id")
	}
	newOwnerPgID, err := parseUUID(req.GetNewOwnerId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid new_owner_id")
	}

	guild, err := h.svc.TransferOwnership(ctx, callerPgID, guildPgID, newOwnerPgID)
	if err != nil {
		switch {
		case errors.Is(err, ErrGuildNotFound):
			return nil, status.Error(codes.NotFound, "guild not found")
		case errors.Is(err, ErrNotGuildOwner):
			return nil, status.Error(codes.PermissionDenied, "only the guild owner can transfer ownership")
		case errors.Is(err, ErrMemberNotFound):
			return nil, status.Error(codes.NotFound, "new owner must be a member of the guild")
		default:
			return nil, status.Error(codes.Internal, "failed to transfer ownership")
		}
	}

	count, _ := h.svc.repo.CountGuildMembers(ctx, guildPgID)
	return &guildv1.TransferOwnershipResponse{
		Guild: h.toProto(ctx, guild, int32(count)),
	}, nil
}

// helpers

func parseUUID(s string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func dbGuildToProto(g db.Guild, memberCount int32) *guildv1.Guild {
	pg := &guildv1.Guild{
		Id:          g.ID.String(),
		Name:        g.Name,
		Description: g.Description,
		IconUrl:     g.IconUrl,
		OwnerId:     g.OwnerID.String(),
		MemberCount: memberCount,
	}
	if g.CreatedAt.Valid {
		pg.CreatedAt = timestamppb.New(g.CreatedAt.Time)
	}
	return pg
}

// toProto wraps dbGuildToProto and resolves the stored object key into a fresh
// presigned URL that external clients can actually reach.
func (h *Handler) toProto(ctx context.Context, g db.Guild, memberCount int32) *guildv1.Guild {
	pg := dbGuildToProto(g, memberCount)
	pg.IconUrl = h.svc.ResolveIconURL(ctx, g.IconUrl)
	return pg
}

func dbGuildMemberToProto(m db.GuildMember) *guildv1.GuildMember {
	pm := &guildv1.GuildMember{
		UserId:   m.UserID.String(),
		GuildId:  m.GuildID.String(),
		Nickname: m.Nickname,
	}
	if m.JoinedAt.Valid {
		pm.JoinedAt = timestamppb.New(m.JoinedAt.Time)
	}
	return pm
}

func dbRoleToProto(r db.Role) *guildv1.Role {
	pr := &guildv1.Role{
		Id:          r.ID.String(),
		GuildId:     r.GuildID.String(),
		Name:        r.Name,
		Color:       r.Color,
		Position:    r.Position,
		// Permissions managed via OpenFGA, not in DB
	}
	if r.CreatedAt.Valid {
		pr.CreatedAt = timestamppb.New(r.CreatedAt.Time)
	}
	return pr
}

func dbInviteToProto(inv db.Invite) *guildv1.Invite {
	pi := &guildv1.Invite{
		Code:      inv.Code,
		GuildId:   inv.GuildID.String(),
		ChannelId: inv.ChannelID.String(),
		CreatorId: inv.CreatorID.String(),
		MaxUses:   inv.MaxUses,
		Uses:      inv.Uses,
	}
	if inv.ExpiresAt.Valid {
		pi.ExpiresAt = timestamppb.New(inv.ExpiresAt.Time)
	}
	if inv.CreatedAt.Valid {
		pi.CreatedAt = timestamppb.New(inv.CreatedAt.Time)
	}
	return pi
}

// Permission enum → OpenFGA relation mapping
var permToRelation = map[guildv1.Permission]string{
	guildv1.Permission_PERMISSION_MANAGE_GUILD:    "can_manage_guild",
	guildv1.Permission_PERMISSION_KICK_MEMBERS:    "can_kick",
	guildv1.Permission_PERMISSION_BAN_MEMBERS:     "can_ban",
	guildv1.Permission_PERMISSION_MANAGE_ROLES:    "can_manage_roles",
	guildv1.Permission_PERMISSION_MANAGE_CHANNELS: "can_manage_channels",
	guildv1.Permission_PERMISSION_ADMINISTRATOR:   "admin",
}

func (h *Handler) GrantPermission(ctx context.Context, req *guildv1.GrantPermissionRequest) (*guildv1.GrantPermissionResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	if !h.svc.authz.CanManageRoles(ctx, callerID, req.GuildId) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
	}

	relation, ok := permToRelation[req.Permission]
	if !ok {
		// For non-guild-level permissions (view, send, connect etc), grant as admin
		relation = "admin"
	}

	if err := h.svc.authz.WriteTuple(ctx, "user:"+req.UserId, relation, "guild:"+req.GuildId); err != nil {
		return nil, status.Error(codes.Internal, "failed to grant permission")
	}
	return &guildv1.GrantPermissionResponse{}, nil
}

func (h *Handler) RevokePermission(ctx context.Context, req *guildv1.RevokePermissionRequest) (*guildv1.RevokePermissionResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	if !h.svc.authz.CanManageRoles(ctx, callerID, req.GuildId) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
	}

	relation, ok := permToRelation[req.Permission]
	if !ok {
		relation = "admin"
	}

	if err := h.svc.authz.DeleteTuple(ctx, "user:"+req.UserId, relation, "guild:"+req.GuildId); err != nil {
		return nil, status.Error(codes.Internal, "failed to revoke permission")
	}
	return &guildv1.RevokePermissionResponse{}, nil
}

func (h *Handler) GetUserPermissions(ctx context.Context, req *guildv1.GetUserPermissionsRequest) (*guildv1.GetUserPermissionsResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	var perms []guildv1.Permission
	for perm, relation := range permToRelation {
		if h.svc.authz.Check(ctx, "user:"+req.UserId, relation, "guild:"+req.GuildId) {
			perms = append(perms, perm)
		}
	}

	return &guildv1.GetUserPermissionsResponse{Permissions: perms}, nil
}

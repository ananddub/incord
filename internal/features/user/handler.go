package user

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ananddub/ndiscord_backend/gen/db"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

type Handler struct {
	userv1.UnimplementedUserServiceServer
	svc          *Service
	blockChecker *BlockChecker
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetBlockChecker sets the block checker used to hide profiles from blocked callers.
func (h *Handler) SetBlockChecker(b *BlockChecker) { h.blockChecker = b }

func (h *Handler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	pgID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	// If the target user has blocked the caller, pretend the user doesn't exist.
	if callerID := middleware.UserIDFromContext(ctx); callerID != "" && callerID != req.GetUserId() {
		if h.blockChecker != nil && h.blockChecker.IsBlocked(ctx, req.GetUserId(), callerID) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
	}

	user, err := h.svc.GetUser(ctx, pgID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to get user")
	}

	return &userv1.GetUserResponse{
		User: h.toProto(ctx, user),
	}, nil
}

func (h *Handler) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	params := db.UpdateUserParams{
		ID:              pgID,
		Username:        req.Username,
		AvatarUrl:       req.AvatarUrl,
		Bio:             req.Bio,
		Status:          req.Status,
		BackgroundColor: req.BackgroundColor,
	}

	user, err := h.svc.UpdateUser(ctx, params)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to update user")
	}

	return &userv1.UpdateUserResponse{
		User: h.toProto(ctx, user),
	}, nil
}

func (h *Handler) UpdateStatus(ctx context.Context, req *userv1.UpdateStatusRequest) (*userv1.UpdateStatusResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	s := req.GetStatus()
	user, err := h.svc.UpdateUser(ctx, db.UpdateUserParams{
		ID:     pgID,
		Status: &s,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to update status")
	}

	return &userv1.UpdateStatusResponse{
		User: h.toProto(ctx, user),
	}, nil
}

func (h *Handler) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	if err := h.svc.DeleteUser(ctx, pgID); err != nil {
		return nil, status.Error(codes.Internal, "failed to delete user")
	}

	return &userv1.DeleteUserResponse{}, nil
}

func (h *Handler) GetUserByUsername(ctx context.Context, req *userv1.GetUserByUsernameRequest) (*userv1.GetUserByUsernameResponse, error) {
	user, err := h.svc.GetUserByUsername(ctx, req.GetUsername())
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to get user")
	}

	// If the target user has blocked the caller, pretend the user doesn't exist.
	targetID := user.ID.String()
	if callerID := middleware.UserIDFromContext(ctx); callerID != "" && callerID != targetID {
		if h.blockChecker != nil && h.blockChecker.IsBlocked(ctx, targetID, callerID) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
	}

	return &userv1.GetUserByUsernameResponse{
		User: h.toProto(ctx, user),
	}, nil
}

func (h *Handler) SearchUsers(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	limit := req.GetLimit()
	if limit == 0 {
		limit = 20
	}
	offset := req.GetOffset()

	users, total, err := h.svc.SearchUsers(ctx, req.GetQuery(), limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to search users")
	}

	protoUsers := make([]*userv1.User, len(users))
	for i, u := range users {
		protoUsers[i] = h.toProto(ctx, u)
	}

	return &userv1.SearchUsersResponse{
		Users: protoUsers,
		Total: int32(total),
	}, nil
}

func (h *Handler) SendFriendRequest(ctx context.Context, req *userv1.SendFriendRequestRequest) (*userv1.SendFriendRequestResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	targetPgID, err := parseUUID(req.GetTargetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid target_user_id")
	}

	friendship, err := h.svc.SendFriendRequest(ctx, userPgID, targetPgID)
	if err != nil {
		switch {
		case errors.Is(err, ErrCannotFriendSelf):
			return nil, status.Error(codes.InvalidArgument, "cannot send friend request to yourself")
		case errors.Is(err, ErrAlreadyFriends):
			return nil, status.Error(codes.AlreadyExists, "already friends")
		case errors.Is(err, ErrFriendRequestExists):
			return nil, status.Error(codes.AlreadyExists, "friend request already exists")
		case errors.Is(err, ErrUserBlocked):
			return nil, status.Error(codes.FailedPrecondition, "user is blocked")
		default:
			return nil, status.Error(codes.Internal, "failed to send friend request")
		}
	}

	return &userv1.SendFriendRequestResponse{
		Friendship: dbFriendshipToProto(friendship),
	}, nil
}

func (h *Handler) AcceptFriendRequest(ctx context.Context, req *userv1.AcceptFriendRequestRequest) (*userv1.AcceptFriendRequestResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	requesterPgID, err := parseUUID(req.GetRequesterUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid requester_user_id")
	}

	friendship, err := h.svc.AcceptFriendRequest(ctx, userPgID, requesterPgID)
	if err != nil {
		if errors.Is(err, ErrNoFriendRequest) {
			return nil, status.Error(codes.NotFound, "no pending friend request found")
		}
		return nil, status.Error(codes.Internal, "failed to accept friend request")
	}

	return &userv1.AcceptFriendRequestResponse{
		Friendship: dbFriendshipToProto(friendship),
	}, nil
}

func (h *Handler) DeclineFriendRequest(ctx context.Context, req *userv1.DeclineFriendRequestRequest) (*userv1.DeclineFriendRequestResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	requesterPgID, err := parseUUID(req.GetRequesterUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid requester_user_id")
	}

	if err := h.svc.DeclineFriendRequest(ctx, userPgID, requesterPgID); err != nil {
		if errors.Is(err, ErrNoFriendRequest) {
			return nil, status.Error(codes.NotFound, "no pending friend request found")
		}
		return nil, status.Error(codes.Internal, "failed to decline friend request")
	}

	return &userv1.DeclineFriendRequestResponse{}, nil
}

func (h *Handler) CancelFriendRequest(ctx context.Context, req *userv1.CancelFriendRequestRequest) (*userv1.CancelFriendRequestResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	targetPgID, err := parseUUID(req.GetTargetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid target_user_id")
	}

	if err := h.svc.CancelFriendRequest(ctx, userPgID, targetPgID); err != nil {
		if errors.Is(err, ErrNoFriendRequest) {
			return nil, status.Error(codes.NotFound, "no pending friend request found")
		}
		return nil, status.Error(codes.Internal, "failed to cancel friend request")
	}

	return &userv1.CancelFriendRequestResponse{}, nil
}

func (h *Handler) RemoveFriend(ctx context.Context, req *userv1.RemoveFriendRequest) (*userv1.RemoveFriendResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	friendPgID, err := parseUUID(req.GetFriendId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid friend_id")
	}

	if err := h.svc.RemoveFriend(ctx, userPgID, friendPgID); err != nil {
		if errors.Is(err, ErrNotFriends) {
			return nil, status.Error(codes.NotFound, "not friends")
		}
		return nil, status.Error(codes.Internal, "failed to remove friend")
	}

	return &userv1.RemoveFriendResponse{}, nil
}

func (h *Handler) BlockUser(ctx context.Context, req *userv1.BlockUserRequest) (*userv1.BlockUserResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	targetPgID, err := parseUUID(req.GetTargetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid target_user_id")
	}

	if err := h.svc.BlockUser(ctx, userPgID, targetPgID); err != nil {
		if errors.Is(err, ErrCannotFriendSelf) {
			return nil, status.Error(codes.InvalidArgument, "cannot block yourself")
		}
		return nil, status.Error(codes.Internal, "failed to block user")
	}

	return &userv1.BlockUserResponse{}, nil
}

func (h *Handler) UnblockUser(ctx context.Context, req *userv1.UnblockUserRequest) (*userv1.UnblockUserResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	userPgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}
	targetPgID, err := parseUUID(req.GetTargetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid target_user_id")
	}

	if err := h.svc.UnblockUser(ctx, userPgID, targetPgID); err != nil {
		if errors.Is(err, ErrNotBlocked) {
			return nil, status.Error(codes.NotFound, "user is not blocked")
		}
		return nil, status.Error(codes.Internal, "failed to unblock user")
	}

	return &userv1.UnblockUserResponse{}, nil
}

func (h *Handler) ListFriends(ctx context.Context, req *userv1.ListFriendsRequest) (*userv1.ListFriendsResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	friends, err := h.svc.ListFriends(ctx, pgID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list friends")
	}

	protoUsers := make([]*userv1.User, len(friends))
	for i, u := range friends {
		protoUsers[i] = h.toProto(ctx, u)
	}

	return &userv1.ListFriendsResponse{
		Friends: protoUsers,
	}, nil
}

func (h *Handler) ListPendingRequests(ctx context.Context, req *userv1.ListPendingRequestsRequest) (*userv1.ListPendingRequestsResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	incoming, outgoing, err := h.svc.ListPendingRequests(ctx, pgID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list pending requests")
	}

	protoIncoming := make([]*userv1.Friendship, len(incoming))
	for i, f := range incoming {
		protoIncoming[i] = dbFriendshipToProto(f)
	}

	protoOutgoing := make([]*userv1.Friendship, len(outgoing))
	for i, f := range outgoing {
		protoOutgoing[i] = dbFriendshipToProto(f)
	}

	return &userv1.ListPendingRequestsResponse{
		Incoming: protoIncoming,
		Outgoing: protoOutgoing,
	}, nil
}

func (h *Handler) ListBlocked(ctx context.Context, req *userv1.ListBlockedRequest) (*userv1.ListBlockedResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	blocked, err := h.svc.ListBlocked(ctx, pgID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list blocked users")
	}

	protoUsers := make([]*userv1.User, len(blocked))
	for i, u := range blocked {
		protoUsers[i] = h.toProto(ctx, u)
	}

	return &userv1.ListBlockedResponse{
		Blocked: protoUsers,
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

func dbUserToProto(u db.User) *userv1.User {
	pu := &userv1.User{
		Id:              u.ID.String(),
		Username:        u.Username,
		Email:           u.Email,
		AvatarUrl:       u.AvatarUrl,
		Bio:             u.Bio,
		Status:          u.Status,
		BackgroundColor: u.BackgroundColor,
	}
	if u.CreatedAt.Valid {
		pu.CreatedAt = timestamppb.New(u.CreatedAt.Time)
	}
	if u.UpdatedAt.Valid {
		pu.UpdatedAt = timestamppb.New(u.UpdatedAt.Time)
	}
	return pu
}

// toProto resolves the stored avatar object key into a fresh presigned URL
// before returning the user to the client.
func (h *Handler) toProto(ctx context.Context, u db.User) *userv1.User {
	pu := dbUserToProto(u)
	pu.AvatarUrl = h.svc.ResolveAvatarURL(ctx, u.AvatarUrl)
	return pu
}

func (h *Handler) UploadUserAvatar(ctx context.Context, req *userv1.UploadUserAvatarRequest) (*userv1.UploadUserAvatarResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	pgID, err := parseUUID(callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid caller id")
	}

	user, avatarURL, err := h.svc.UploadAvatar(ctx, pgID, req.GetFilename(), req.GetContentType(), req.GetData())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &userv1.UploadUserAvatarResponse{
		AvatarUrl: avatarURL,
		User:      h.toProto(ctx, user),
	}, nil
}

func dbFriendshipToProto(f db.Friendship) *userv1.Friendship {
	pf := &userv1.Friendship{
		UserId:   f.UserID.String(),
		FriendId: f.FriendID.String(),
	}
	switch f.Status {
	case "pending":
		pf.Status = userv1.FriendshipStatus_FRIENDSHIP_STATUS_PENDING
	case "accepted":
		pf.Status = userv1.FriendshipStatus_FRIENDSHIP_STATUS_ACCEPTED
	case "blocked":
		pf.Status = userv1.FriendshipStatus_FRIENDSHIP_STATUS_BLOCKED
	default:
		pf.Status = userv1.FriendshipStatus_FRIENDSHIP_STATUS_UNSPECIFIED
	}
	if f.CreatedAt.Valid {
		pf.CreatedAt = timestamppb.New(f.CreatedAt.Time)
	}
	return pf
}

package user

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ananddub/ndiscord_backend/gen/db"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"github.com/ananddub/ndiscord_backend/internal/shared/util"
)

// DMOpener creates (or returns the existing) DM channel between two users.
// Implemented by channel.DMResolver; used here to auto-open a DM on friend
// accept without a hard dependency on the channel package.
type DMOpener interface {
	GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (string, error)
}

// PresenceReader returns the user's *live* presence (the Redis-stored
// broadcast state — offline when their StreamFriendActivity session is
// closed, else the last status they chose). Used to stamp enrichment
// payloads (friend_request, friend_accepted, ...) with the actual
// current state rather than the DB-persisted intent, which would say
// "online" even while the user is disconnected.
type PresenceReader interface {
	GetLiveStatus(ctx context.Context, userID string) (status, customStatus string)
}

type Service struct {
	repo     *Repository
	nats     *realtime.LPubSub
	dm       DMOpener
	presence PresenceReader
	minio    *minio.Client
	signer   *minio.Client
	bucket   string
}

func NewService(repo *Repository, nats *realtime.LPubSub) *Service {
	return &Service{repo: repo, nats: nats}
}

// SetDMOpener wires the DM channel opener used on friend accept.
func (s *Service) SetDMOpener(d DMOpener) {
	s.dm = d
}

// SetPresenceReader wires a live-presence reader so friend/profile
// enrichment events carry the recipient's actual online/offline state
// instead of the stale DB intent.
func (s *Service) SetPresenceReader(p PresenceReader) {
	s.presence = p
}

// pgIDToStr converts a pgtype.UUID to its canonical hex-dashed string form.
func pgIDToStr(id pgtype.UUID) string {
	return uuid.UUID(id.Bytes).String()
}

// ConvertUUIDToString converts a pgtype.UUID to its string representation.
func (s *Service) ConvertUUIDToString(id pgtype.UUID) string {
	return pgIDToStr(id)
}

// presenceStatusFromString maps the persisted status column into the
// PresenceStatus enum. Unknown values fall back to UNSPECIFIED.
func presenceStatusFromString(s string) streamv1.PresenceStatus {
	switch s {
	case "online":
		return streamv1.PresenceStatus_PRESENCE_STATUS_ONLINE
	case "idle":
		return streamv1.PresenceStatus_PRESENCE_STATUS_IDLE
	case "dnd":
		return streamv1.PresenceStatus_PRESENCE_STATUS_DND
	case "offline":
		return streamv1.PresenceStatus_PRESENCE_STATUS_OFFLINE
	}
	return streamv1.PresenceStatus_PRESENCE_STATUS_UNSPECIFIED
}

func (s *Service) publishFriendActivity(toUserID string, event streamv1.FriendEventType, evt *streamv1.FriendActivityEvent) {
	if s.nats == nil {
		return
	}
	if evt == nil {
		evt = &streamv1.FriendActivityEvent{}
	}
	evt.Event = event
	if evt.Timestamp == nil {
		evt.Timestamp = timestamppb.Now()
	}
	_ = realtime.Publish(s.nats, realtime.FriendActivity(toUserID), evt)
}

// LookupBasicProfile returns the username and resolved avatar URL for the
// given user ID string. Used by other features (e.g. presence) to enrich
// realtime events without taking a hard dependency on the user package.
// Returns empty strings if the lookup fails.
func (s *Service) LookupBasicProfile(ctx context.Context, userID string) (string, string) {
	pgID, err := parseUUID(userID)
	if err != nil {
		return "", ""
	}
	u, err := s.repo.GetUserByID(ctx, pgID)
	if err != nil {
		return "", ""
	}
	return u.Username, s.ResolveAvatarURL(ctx, u.AvatarUrl)
}

// GetStatusIntent returns the user's persisted status (Postgres
// users.status column) — their last-known *intent*, which should be
// re-broadcast whenever the user comes back online. Falls back to
// "online" if the column is blank so a fresh account still shows up
// to friends instead of silently staying offline.
func (s *Service) GetStatusIntent(ctx context.Context, userID string) (status, customStatus string) {
	pgID, err := parseUUID(userID)
	if err != nil {
		return "online", ""
	}
	u, err := s.repo.GetUserByID(ctx, pgID)
	if err != nil {
		return "online", ""
	}
	if u.Status == "" {
		return "online", ""
	}
	return u.Status, u.Status
}

func (s *Service) friendPayload(ctx context.Context, actorID pgtype.UUID) *streamv1.FriendActivityEvent {
	u, err := s.repo.GetUserByID(ctx, actorID)
	if err != nil {
		return &streamv1.FriendActivityEvent{UserId: pgIDToStr(actorID)}
	}
	statusStr := u.Status
	customStr := u.Status
	if s.presence != nil {
		if live, custom := s.presence.GetLiveStatus(ctx, pgIDToStr(u.ID)); live != "" {
			statusStr = live
			customStr = custom
		}
	}
	return &streamv1.FriendActivityEvent{
		UserId:       pgIDToStr(u.ID),
		Username:     u.Username,
		DisplayName:  u.DisplayName,
		Status:       presenceStatusFromString(statusStr),
		CustomStatus: customStr,
		AvatarUrl:    s.ResolveAvatarURL(ctx, u.AvatarUrl),
	}
}

func (s *Service) SetStorage(client, signer *minio.Client, bucket string) {
	s.minio = client
	s.signer = signer
	s.bucket = bucket
}

const (
	maxAvatarSize     = 5 * 1024 * 1024
	avatarDownloadTTL = 7 * 24 * time.Hour
)

var allowedAvatarContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func (s *Service) ResolveAvatarURL(ctx context.Context, key string) string {
	return util.GetBaseURl(key)
}

func (s *Service) UploadAvatar(ctx context.Context, userID pgtype.UUID, filename, contentType string, data []byte) (db.User, string, error) {
	if s.minio == nil {
		return db.User{}, "", fmt.Errorf("storage not configured")
	}
	if len(data) == 0 {
		return db.User{}, "", fmt.Errorf("empty file")
	}
	if len(data) > maxAvatarSize {
		return db.User{}, "", fmt.Errorf("avatar too large (max %d bytes)", maxAvatarSize)
	}
	if !allowedAvatarContentTypes[contentType] {
		return db.User{}, "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	uidStr := uuid.UUID(userID.Bytes).String()
	objectKey := fmt.Sprintf("users/%s/avatar_%s_%s", uidStr, uuid.New().String(), filename)

	_, err := s.minio.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return db.User{}, "", fmt.Errorf("failed to upload avatar: %w", err)
	}

	updated, err := s.repo.UpdateUser(ctx, db.UpdateUserParams{
		ID:        userID,
		AvatarUrl: &objectKey,
	})
	if err != nil {
		return db.User{}, "", fmt.Errorf("failed to update user avatar: %w", err)
	}

	avatarURL := s.ResolveAvatarURL(ctx, objectKey)
	s.publishFriendActivity(pgIDToStr(updated.ID), streamv1.FriendEventType_FRIEND_EVENT_PROFILE_UPDATE, &streamv1.FriendActivityEvent{
		UserId:      pgIDToStr(updated.ID),
		Username:    updated.Username,
		DisplayName: updated.DisplayName,
		AvatarUrl:   avatarURL,
	})

	return updated, avatarURL, nil
}

func (s *Service) GetUser(ctx context.Context, id pgtype.UUID) (*db.User, error) {
	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserNotFound, err)
	}
	user.AvatarUrl = s.ResolveAvatarURL(ctx, user.AvatarUrl)
	return util.SimilarValuesCopy(user, db.User{}), nil
}

func (s *Service) GetUserByUsername(ctx context.Context, username string) (*db.User, error) {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserNotFound, err)
	}
	user.AvatarUrl = s.ResolveAvatarURL(ctx, user.AvatarUrl)
	return util.SimilarValuesCopy(user, db.User{}), nil
}

func (s *Service) UpdateUsername(ctx context.Context, id pgtype.UUID, username string) (db.User, error) {
	user, err := s.repo.UpdateUsername(ctx, id, username)
	if err != nil {
		return db.User{}, err
	}

	s.publishFriendActivity(pgIDToStr(user.ID), streamv1.FriendEventType_FRIEND_EVENT_PROFILE_UPDATE, &streamv1.FriendActivityEvent{
		UserId:      pgIDToStr(user.ID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		AvatarUrl:   s.ResolveAvatarURL(ctx, user.AvatarUrl),
	})

	return user, nil
}

func (s *Service) UpdateUser(ctx context.Context, params db.UpdateUserParams) (db.User, error) {
	user, err := s.repo.UpdateUser(ctx, params)
	if err != nil {
		return db.User{}, fmt.Errorf("failed to update user: %w", err)
	}
	s.publishFriendActivity(pgIDToStr(user.ID), streamv1.FriendEventType_FRIEND_EVENT_PROFILE_UPDATE, &streamv1.FriendActivityEvent{
		UserId:       pgIDToStr(user.ID),
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Status:       presenceStatusFromString(user.Status),
		CustomStatus: user.Status,
		AvatarUrl:    s.ResolveAvatarURL(ctx, user.AvatarUrl),
	})

	return user, nil
}

func (s *Service) UpdateStatus(ctx context.Context, id pgtype.UUID, newStatus string) (db.User, error) {
	user, err := s.repo.UpdateUser(ctx, db.UpdateUserParams{
		ID:     id,
		Status: &newStatus,
	})
	if err != nil {
		return db.User{}, fmt.Errorf("failed to update status: %w", err)
	}

	s.publishFriendActivity(pgIDToStr(user.ID), streamv1.FriendEventType_FRIEND_EVENT_PRESENCE_UPDATE, &streamv1.FriendActivityEvent{
		UserId:       pgIDToStr(user.ID),
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Status:       presenceStatusFromString(user.Status),
		CustomStatus: user.Status,
		AvatarUrl:    s.ResolveAvatarURL(ctx, user.AvatarUrl),
	})

	return user, nil
}

func (s *Service) DeleteUser(ctx context.Context, id pgtype.UUID) error {
	if err := s.repo.DeleteUser(ctx, id); err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	return nil
}

func (s *Service) SearchUsers(ctx context.Context, query string, limit, offset int32) ([]db.User, int64, error) {
	count, err := s.repo.CountSearchUsers(ctx, &query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count search results: %w", err)
	}

	users, err := s.repo.SearchUsers(ctx, db.SearchUsersParams{
		Column1: &query,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search users: %w", err)
	}
	var dbUsers []db.User
	for _, u := range users {
		u.AvatarUrl = s.ResolveAvatarURL(ctx, u.AvatarUrl)
		dbUsers = append(dbUsers, *util.SimilarValuesCopy(u, db.User{}))
	}
	return dbUsers, count, nil
}

func (s *Service) SendFriendRequest(ctx context.Context, userID, targetID pgtype.UUID) (db.Friendship, error) {
	if userID == targetID {
		return db.Friendship{}, ErrCannotFriendSelf
	}

	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})
	if err == nil {
		switch existing.Status {
		case "accepted":
			return db.Friendship{}, ErrAlreadyFriends
		case "pending":
			return db.Friendship{}, ErrFriendRequestExists
		case "blocked":
			return db.Friendship{}, ErrUserBlocked
		}
	}

	friendship, err := s.repo.CreateFriendship(ctx, db.CreateFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
		Status:   "pending",
	})
	if err != nil {
		return db.Friendship{}, fmt.Errorf("failed to create friend request: %w", err)
	}

	s.publishFriendActivity(pgIDToStr(targetID), streamv1.FriendEventType_FRIEND_EVENT_REQUEST, s.friendPayload(ctx, userID))

	return friendship, nil
}

func (s *Service) AcceptFriendRequest(ctx context.Context, userID, requesterID pgtype.UUID) (db.Friendship, error) {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   requesterID,
		FriendID: userID,
	})
	if err != nil {
		return db.Friendship{}, ErrNoFriendRequest
	}
	if existing.Status != "pending" {
		return db.Friendship{}, ErrNoFriendRequest
	}
	if existing.UserID != requesterID || existing.FriendID != userID {
		return db.Friendship{}, ErrNoFriendRequest
	}

	friendship, err := s.repo.UpdateFriendshipStatus(ctx, db.UpdateFriendshipStatusParams{
		UserID:   requesterID,
		FriendID: userID,
		Status:   "accepted",
	})
	if err != nil {
		return db.Friendship{}, fmt.Errorf("failed to accept friend request: %w", err)
	}

	dmChannelID := ""
	if s.dm != nil {
		if id, err := s.dm.GetOrCreateDMChannel(ctx, pgIDToStr(userID), []string{pgIDToStr(requesterID)}); err == nil {
			dmChannelID = id
		}
	}

	payloadForRequester := s.friendPayload(ctx, userID)
	payloadForRequester.ChannelId = dmChannelID
	s.publishFriendActivity(pgIDToStr(requesterID), streamv1.FriendEventType_FRIEND_EVENT_ACCEPTED, payloadForRequester)

	payloadForAccepter := s.friendPayload(ctx, requesterID)
	payloadForAccepter.ChannelId = dmChannelID
	s.publishFriendActivity(pgIDToStr(userID), streamv1.FriendEventType_FRIEND_EVENT_ACCEPTED, payloadForAccepter)

	return friendship, nil
}

func (s *Service) DeclineFriendRequest(ctx context.Context, userID, requesterID pgtype.UUID) error {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   requesterID,
		FriendID: userID,
	})
	if err != nil {
		return ErrNoFriendRequest
	}
	if existing.Status != "pending" {
		return ErrNoFriendRequest
	}

	if err := s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   requesterID,
		FriendID: userID,
	}); err != nil {
		return err
	}

	s.publishFriendActivity(pgIDToStr(requesterID), streamv1.FriendEventType_FRIEND_EVENT_DECLINED, s.friendPayload(ctx, userID))

	return nil
}

func (s *Service) CancelFriendRequest(ctx context.Context, userID, targetID pgtype.UUID) error {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})
	if err != nil {
		return ErrNoFriendRequest
	}
	if existing.Status != "pending" || existing.UserID != userID {
		return ErrNoFriendRequest
	}

	if err := s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	}); err != nil {
		return err
	}

	s.publishFriendActivity(pgIDToStr(targetID), streamv1.FriendEventType_FRIEND_EVENT_REQUEST_CANCELLED, s.friendPayload(ctx, userID))

	return nil
}

func (s *Service) RemoveFriend(ctx context.Context, userID, friendID pgtype.UUID) error {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   userID,
		FriendID: friendID,
	})
	if err != nil {
		return ErrNotFriends
	}
	if existing.Status != "accepted" {
		return ErrNotFriends
	}

	if err := s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: friendID,
	}); err != nil {
		return err
	}

	s.publishFriendActivity(pgIDToStr(friendID), streamv1.FriendEventType_FRIEND_EVENT_REMOVED, s.friendPayload(ctx, userID))

	return nil
}

func (s *Service) BlockUser(ctx context.Context, userID, targetID pgtype.UUID) error {
	if userID == targetID {
		return ErrCannotFriendSelf
	}

	_ = s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})

	_, err := s.repo.CreateFriendship(ctx, db.CreateFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
		Status:   "blocked",
	})
	if err != nil {
		return fmt.Errorf("failed to block user: %w", err)
	}

	return nil
}

func (s *Service) UnblockUser(ctx context.Context, userID, targetID pgtype.UUID) error {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})
	if err != nil {
		return ErrNotBlocked
	}
	if existing.Status != "blocked" || existing.UserID != userID {
		return ErrNotBlocked
	}

	return s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})
}

func (s *Service) ListFriends(ctx context.Context, userID pgtype.UUID) (*[]db.User, error) {
	u, err := s.repo.ListFriends(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list friends: %w", err)
	}
	var friends []db.User
	for _, f := range u {
		friends = append(friends, db.User{
			ID:          f.ID,
			Username:    f.Username,
			DisplayName: f.DisplayName,
			Status:      f.Status,
			AvatarUrl:   s.ResolveAvatarURL(ctx, f.AvatarUrl),
		})
	}
	return &friends, nil
}

func (s *Service) ListPendingRequests(ctx context.Context, userID pgtype.UUID) ([]db.ListPendingIncomingRow, []db.ListPendingOutgoingRow, error) {
	incoming, err := s.repo.ListPendingIncoming(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list incoming requests: %w", err)
	}

	outgoing, err := s.repo.ListPendingOutgoing(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list outgoing requests: %w", err)
	}

	return incoming, outgoing, nil
}

func (s *Service) ListBlocked(ctx context.Context, userID pgtype.UUID) ([]db.User, error) {
	return s.repo.ListBlocked(ctx, userID)
}

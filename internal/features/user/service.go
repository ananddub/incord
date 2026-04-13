package user

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// DMOpener creates (or returns the existing) DM channel between two users.
// Implemented by channel.DMResolver; used here to auto-open a DM on friend
// accept without a hard dependency on the channel package.
type DMOpener interface {
	GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (string, error)
}

type Service struct {
	repo   *Repository
	nats   *realtime.Hub
	dm     DMOpener
	minio  *minio.Client
	signer *minio.Client
	bucket string
}

func NewService(repo *Repository, nats *realtime.Hub) *Service {
	return &Service{repo: repo, nats: nats}
}

// SetDMOpener wires the DM channel opener used on friend accept.
func (s *Service) SetDMOpener(d DMOpener) {
	s.dm = d
}

// pgIDToStr converts a pgtype.UUID to its canonical hex-dashed string form.
func pgIDToStr(id pgtype.UUID) string {
	return uuid.UUID(id.Bytes).String()
}

// publishFriendActivity publishes a FriendActivityEvent payload to the given
// user's realtime subject. Nil-safe: does nothing if NATS is not wired.
// Timestamp is intentionally omitted — the proto's google.protobuf.Timestamp
// can't be decoded by encoding/json from a plain RFC3339 string, and the
// client knows when they received the event anyway.
func (s *Service) publishFriendActivity(toUserID, event string, extra map[string]any) {
	if s.nats == nil {
		return
	}
	payload := map[string]any{"event": event}
	for k, v := range extra {
		payload[k] = v
	}
	_ = s.nats.Publish(realtime.FriendActivity(toUserID), payload)
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

// friendPayload loads the given user and returns a payload keyed on the
// FriendActivityEvent proto field names (user_id, username, status,
// custom_status, avatar_url). Used to enrich outgoing friend/presence events
// so the recipient doesn't just see a bare UUID.
func (s *Service) friendPayload(ctx context.Context, actorID pgtype.UUID) map[string]any {
	u, err := s.repo.GetUserByID(ctx, actorID)
	if err != nil {
		return map[string]any{"user_id": pgIDToStr(actorID)}
	}
	return map[string]any{
		"user_id":       pgIDToStr(u.ID),
		"username":      u.Username,
		"status":        u.Status,
		"custom_status": u.Status,
		"avatar_url":    s.ResolveAvatarURL(ctx, u.AvatarUrl),
	}
}

// SetStorage wires the MinIO clients for avatar uploads.
// `client` is used to upload bytes; `signer` signs presigned GET URLs
// with a public-reachable hostname.
func (s *Service) SetStorage(client, signer *minio.Client, bucket string) {
	s.minio = client
	s.signer = signer
	s.bucket = bucket
}

const (
	maxAvatarSize       = 5 * 1024 * 1024
	avatarDownloadTTL   = 7 * 24 * time.Hour
)

var allowedAvatarContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// ResolveAvatarURL turns a stored object key into a fresh presigned URL.
// Empty keys return empty string; legacy full URLs are returned as-is.
func (s *Service) ResolveAvatarURL(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	if len(key) >= 7 && (key[:7] == "http://" || (len(key) >= 8 && key[:8] == "https://")) {
		return key
	}
	if s.signer == nil {
		return ""
	}
	u, err := s.signer.PresignedGetObject(ctx, s.bucket, key, avatarDownloadTTL, url.Values{})
	if err != nil {
		return ""
	}
	return u.String()
}

// UploadAvatar stores raw bytes in object storage and updates users.avatar_url
// with the object key. The caller is expected to be authenticated.
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
	s.publishFriendActivity(pgIDToStr(updated.ID), "profile_update", map[string]any{
		"user_id":    pgIDToStr(updated.ID),
		"username":   updated.Username,
		"avatar_url": avatarURL,
	})

	return updated, avatarURL, nil
}

func (s *Service) GetUser(ctx context.Context, id pgtype.UUID) (db.User, error) {
	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return db.User{}, fmt.Errorf("%w: %v", ErrUserNotFound, err)
	}
	return user, nil
}

func (s *Service) GetUserByUsername(ctx context.Context, username string) (db.User, error) {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		return db.User{}, fmt.Errorf("%w: %v", ErrUserNotFound, err)
	}
	return user, nil
}

func (s *Service) UpdateUser(ctx context.Context, params db.UpdateUserParams) (db.User, error) {
	user, err := s.repo.UpdateUser(ctx, params)
	if err != nil {
		return db.User{}, fmt.Errorf("failed to update user: %w", err)
	}

	// Broadcast profile update to the user's own subject; friends subscribe
	// to this subject via StreamFriendActivity.
	s.publishFriendActivity(pgIDToStr(user.ID), "profile_update", map[string]any{
		"user_id":       pgIDToStr(user.ID),
		"username":      user.Username,
		"status":        user.Status,
		"custom_status": user.Status,
		"avatar_url":    s.ResolveAvatarURL(ctx, user.AvatarUrl),
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

	return users, count, nil
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

	// Notify the target: "X sent you a friend request".
	s.publishFriendActivity(pgIDToStr(targetID), "friend_request", s.friendPayload(ctx, userID))

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
	// Ensure the current user is the recipient (friend_id), not the sender
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

	// Auto-open a DM channel between the two users. If one already exists,
	// GetOrCreateDMChannel returns it — no duplicates.
	dmChannelID := ""
	if s.dm != nil {
		if id, err := s.dm.GetOrCreateDMChannel(ctx, pgIDToStr(userID), []string{pgIDToStr(requesterID)}); err == nil {
			dmChannelID = id
		}
	}

	// Notify the requester: "X accepted your friend request" with the DM
	// channel id so the client can jump straight into the conversation.
	payloadForRequester := s.friendPayload(ctx, userID)
	if dmChannelID != "" {
		payloadForRequester["channel_id"] = dmChannelID
	}
	s.publishFriendActivity(pgIDToStr(requesterID), "friend_accepted", payloadForRequester)

	// Also notify the accepter so both ends get the channel id for an
	// instant UI transition.
	payloadForAccepter := s.friendPayload(ctx, requesterID)
	if dmChannelID != "" {
		payloadForAccepter["channel_id"] = dmChannelID
	}
	s.publishFriendActivity(pgIDToStr(userID), "friend_accepted", payloadForAccepter)

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

	// Notify the requester: "X declined your friend request".
	s.publishFriendActivity(pgIDToStr(requesterID), "friend_declined", s.friendPayload(ctx, userID))

	return nil
}

// CancelFriendRequest is called by the sender (userID) to withdraw their
// own pending outgoing request to targetID. The row must still be pending
// and the caller must be the original sender.
func (s *Service) CancelFriendRequest(ctx context.Context, userID, targetID pgtype.UUID) error {
	existing, err := s.repo.GetFriendship(ctx, db.GetFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	})
	if err != nil {
		return ErrNoFriendRequest
	}
	// Must still be pending and owned by the caller (userID) as the sender.
	if existing.Status != "pending" || existing.UserID != userID {
		return ErrNoFriendRequest
	}

	if err := s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: targetID,
	}); err != nil {
		return err
	}

	// Notify the target so their incoming-requests list updates in realtime:
	// "X cancelled the request they sent you".
	s.publishFriendActivity(pgIDToStr(targetID), "friend_request_cancelled", s.friendPayload(ctx, userID))

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

	// Notify the removed friend: "X removed you".
	s.publishFriendActivity(pgIDToStr(friendID), "friend_removed", s.friendPayload(ctx, userID))

	return nil
}

func (s *Service) BlockUser(ctx context.Context, userID, targetID pgtype.UUID) error {
	if userID == targetID {
		return ErrCannotFriendSelf
	}

	// Delete any existing friendship first
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

func (s *Service) ListFriends(ctx context.Context, userID pgtype.UUID) ([]db.User, error) {
	return s.repo.ListFriends(ctx, userID)
}

func (s *Service) ListPendingRequests(ctx context.Context, userID pgtype.UUID) ([]db.Friendship, []db.Friendship, error) {
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

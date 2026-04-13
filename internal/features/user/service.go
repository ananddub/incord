package user

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
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

	return s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   requesterID,
		FriendID: userID,
	})
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

	return s.repo.DeleteFriendship(ctx, db.DeleteFriendshipParams{
		UserID:   userID,
		FriendID: friendID,
	})
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

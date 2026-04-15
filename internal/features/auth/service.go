package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/mail"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrUserExists         = errors.New("user already exists")
	ErrInvalidOTP         = errors.New("invalid or expired OTP")
	ErrNotVerified        = errors.New("email not verified")
	ErrAlreadyVerified    = errors.New("email already verified")
)

type Service struct {
	repo   *Repository
	cfg    config.JWTConfig
	mailer *mail.Sender
}

func NewService(repo *Repository, cfg config.JWTConfig, mailer ...*mail.Sender) *Service {
	s := &Service{repo: repo, cfg: cfg}
	if len(mailer) > 0 {
		s.mailer = mailer[0]
	}
	return s
}

// Register creates user (unverified) and sends OTP email. No tokens yet.
// The provided username is the base name; the repository assigns a random
// 4-digit discriminator so the stored handle becomes "name#1234" and sets
// display_name to the base name.
func (s *Service) Register(ctx context.Context, username, email, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	user, err := s.repo.CreateUserWithUsername(ctx, username, email, string(hash))
	if err != nil {
		return "", fmt.Errorf("failed to create user: %w", err)
	}

	// Generate and send OTP
	if err := s.sendOTP(ctx, email); err != nil {
		return user.ID.String(), fmt.Errorf("user created but failed to send OTP: %w", err)
	}

	return user.ID.String(), nil
}

// VerifyOTP checks OTP, marks user verified, returns tokens.
func (s *Service) VerifyOTP(ctx context.Context, email, otp string) (string, *TokenPair, error) {
	storedOTP, err := s.repo.GetOTP(ctx, email)
	if err != nil {
		return "", nil, ErrInvalidOTP
	}

	if storedOTP != otp {
		return "", nil, ErrInvalidOTP
	}

	// Get user
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return "", nil, ErrInvalidCredentials
	}

	// Mark verified
	uid, _ := uuid.Parse(user.ID.String())
	pgID := pgtype.UUID{Bytes: uid, Valid: true}
	if _, err := s.repo.VerifyUser(ctx, pgID); err != nil {
		return "", nil, fmt.Errorf("failed to verify user: %w", err)
	}

	// Clean up OTP
	_ = s.repo.DeleteOTP(ctx, email)

	// Generate tokens
	tokens, err := s.generateTokens(ctx, user.ID.String())
	if err != nil {
		return "", nil, err
	}

	return user.ID.String(), tokens, nil
}

// ResendOTP generates and sends a new OTP.
func (s *Service) ResendOTP(ctx context.Context, email string) error {
	// Check user exists
	_, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return ErrInvalidCredentials
	}

	return s.sendOTP(ctx, email)
}

// Login checks credentials + verified status, returns tokens.
func (s *Service) Login(ctx context.Context, email, password string) (string, *TokenPair, error) {
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return "", nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", nil, ErrInvalidCredentials
	}

	if !user.Verified {
		return "", nil, ErrNotVerified
	}

	tokens, err := s.generateTokens(ctx, user.ID.String())
	if err != nil {
		return "", nil, err
	}

	return user.ID.String(), tokens, nil
}

func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	userID, err := s.repo.GetRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if err := s.repo.DeleteRefreshToken(ctx, refreshToken); err != nil {
		return nil, fmt.Errorf("failed to delete old refresh token: %w", err)
	}

	return s.generateTokens(ctx, userID)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.repo.DeleteRefreshToken(ctx, refreshToken)
}

func (s *Service) ValidateToken(ctx context.Context, accessToken string) (string, bool, error) {
	token, err := jwt.Parse(accessToken, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.cfg.Secret), nil
	})
	if err != nil {
		return "", false, nil
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", false, nil
	}

	userID, ok := claims["sub"].(string)
	if !ok {
		return "", false, nil
	}

	return userID, true, nil
}

// sendOTP generates a 6-digit OTP, stores in Redis, sends via email.
func (s *Service) sendOTP(ctx context.Context, email string) error {
	otp := generateOTP()

	if err := s.repo.StoreOTP(ctx, email, otp); err != nil {
		return fmt.Errorf("failed to store OTP: %w", err)
	}

	if s.mailer != nil {
		if err := s.mailer.SendOTP(email, otp); err != nil {
			return fmt.Errorf("failed to send OTP email: %w", err)
		}
	}

	return nil
}

// generateOTP returns a random 6-digit string.
func generateOTP() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(999999))
	return fmt.Sprintf("%06d", n.Int64())
}

func (s *Service) generateTokens(ctx context.Context, userID string) (*TokenPair, error) {
	now := time.Now()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(s.cfg.AccessTokenTTL).Unix(),
	})

	accessStr, err := accessToken.SignedString([]byte(s.cfg.Secret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshBytes := make([]byte, 32)
	if _, err := rand.Read(refreshBytes); err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	refreshStr := hex.EncodeToString(refreshBytes)

	if err := s.repo.StoreRefreshToken(ctx, userID, refreshStr, s.cfg.RefreshTokenTTL); err != nil {
		return nil, fmt.Errorf("failed to store refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessStr,
		RefreshToken: refreshStr,
	}, nil
}

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const userIDContextKey contextKey = "auth_user_id"

type Service struct {
	secret []byte
	ttl    time.Duration
}

func NewService(secret string, ttl time.Duration) (*Service, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("auth secret is required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Service{
		secret: []byte(secret),
		ttl:    ttl,
	}, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(password, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (s *Service) IssueToken(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("userID is required")
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(s.ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *Service) ParseToken(token string) (string, error) {
	parsed, err := jwt.Parse(strings.TrimSpace(token), func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}
	if !parsed.Valid {
		return "", errors.New("invalid token")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid token claims")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return "", errors.New("invalid subject claim")
	}
	userID := strings.TrimSpace(sub)
	if userID == "" {
		return "", errors.New("empty subject claim")
	}
	return userID, nil
}

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(userIDContextKey)
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

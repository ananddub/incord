package auth

import "time"

type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

type Claims struct {
	UserID string
	Exp    time.Time
}

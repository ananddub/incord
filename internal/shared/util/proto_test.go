package util

import "testing"

type DBUser struct {
	ID          string
	Username    string
	Email       string
	AvatarURL   string
	Age         int
	PrivateData string
}

type ProtoUser struct {
	Username  string
	Email     string
	AvatarURL string
	Age       int
	ID        string
}

func TestSimilarValuesCopy(t *testing.T) {
	src := DBUser{
		ID:          "123",
		Username:    "anand",
		Email:       "anand@example.com",
		AvatarURL:   "avatar.png",
		Age:         21,
		PrivateData: "secret",
	}

	dst := SimilarValuesCopy(src, ProtoUser{})

	if dst.ID != src.ID {
		t.Fatalf("ID mismatch")
	}

	if dst.Username != src.Username {
		t.Fatalf("Username mismatch")
	}

	if dst.Email != src.Email {
		t.Fatalf("Email mismatch")
	}

	if dst.AvatarURL != src.AvatarURL {
		t.Fatalf("AvatarURL mismatch")
	}

	if dst.Age != src.Age {
		t.Fatalf("Age mismatch")
	}
}

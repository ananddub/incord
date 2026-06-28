package sqlutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitStatementsIgnoresSemicolonsInCommentsAndQuotes(t *testing.T) {
	sql := `-- comment with ; semicolon
CREATE TABLE example (
	name TEXT DEFAULT 'semi;colon',
	quoted TEXT DEFAULT $$body; text$$
);
/* block ; comment */
INSERT INTO example (name) VALUES ('it''s; ok');`

	got := SplitStatements(sql)
	want := []string{
		"-- comment with ; semicolon\nCREATE TABLE example (\n\tname TEXT DEFAULT 'semi;colon',\n\tquoted TEXT DEFAULT $$body; text$$\n)",
		"/* block ; comment */\nINSERT INTO example (name) VALUES ('it''s; ok')",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitStatements() = %#v, want %#v", got, want)
	}
}

func TestGooseUpSection(t *testing.T) {
	sql := `-- +goose Up
CREATE TABLE users (id uuid);
-- +goose Down
DROP TABLE users;`

	got := strings.TrimSpace(GooseUpSection(sql))
	want := "CREATE TABLE users (id uuid);"
	if got != want {
		t.Fatalf("GooseUpSection() = %q, want %q", got, want)
	}
}

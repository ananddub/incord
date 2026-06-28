package sqlutil

import "strings"

// GooseUpSection returns the SQL between "-- +goose Up" and "-- +goose Down".
// Files without Goose markers are returned unchanged for older plain SQL files.
func GooseUpSection(sql string) string {
	up := strings.Index(sql, "-- +goose Up")
	if up == -1 {
		return sql
	}
	body := sql[up+len("-- +goose Up"):]
	down := strings.Index(body, "-- +goose Down")
	if down == -1 {
		return body
	}
	return body[:down]
}

// SplitStatements splits SQL text on statement-ending semicolons while
// ignoring semicolons inside comments and quoted strings.
func SplitStatements(sql string) []string {
	var statements []string
	start := 0
	inLineComment := false
	inBlockComment := false
	inSingleQuote := false
	inDoubleQuote := false
	dollarQuote := ""

	for i := 0; i < len(sql); i++ {
		c := sql[i]
		next := byte(0)
		if i+1 < len(sql) {
			next = sql[i+1]
		}

		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
			}
			continue
		case inBlockComment:
			if c == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		case dollarQuote != "":
			if strings.HasPrefix(sql[i:], dollarQuote) {
				i += len(dollarQuote) - 1
				dollarQuote = ""
			}
			continue
		case inSingleQuote:
			if c == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		case inDoubleQuote:
			if c == '"' {
				if next == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}

		switch {
		case c == '-' && next == '-':
			inLineComment = true
			i++
		case c == '/' && next == '*':
			inBlockComment = true
			i++
		case c == '\'':
			inSingleQuote = true
		case c == '"':
			inDoubleQuote = true
		case c == '$':
			if tag, ok := readDollarQuoteTag(sql[i:]); ok {
				dollarQuote = tag
				i += len(tag) - 1
			}
		case c == ';':
			if stmt := strings.TrimSpace(sql[start:i]); stmt != "" {
				statements = append(statements, stmt)
			}
			start = i + 1
		}
	}

	if stmt := strings.TrimSpace(sql[start:]); stmt != "" {
		statements = append(statements, stmt)
	}
	return statements
}

func readDollarQuoteTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '$':
			return s[:i+1], true
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 1 && c >= '0' && c <= '9':
			continue
		default:
			return "", false
		}
	}
	return "", false
}

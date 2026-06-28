-- +goose Up
CREATE TABLE IF NOT EXISTS role_members (
    role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    deleted    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS role_members;


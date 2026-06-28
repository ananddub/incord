-- +goose Up
CREATE TABLE IF NOT EXISTS permissions (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS permissions;


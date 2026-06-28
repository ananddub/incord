-- +goose Up
CREATE TABLE IF NOT EXISTS media_files (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    uploader_id  UUID NOT NULL REFERENCES users(id),
    filename     TEXT NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    size         BIGINT NOT NULL,
    bucket_key   TEXT NOT NULL,
    confirmed    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted      BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_uploader ON media_files(uploader_id);

-- +goose Down
DROP TABLE IF EXISTS media_files;


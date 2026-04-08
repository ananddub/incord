-- Convert permissions from BIGINT bitfield to TEXT[] array of permission names
ALTER TABLE roles ADD COLUMN IF NOT EXISTS permission_list TEXT[] NOT NULL DEFAULT '{}';

-- Migrate existing bitfield data to array (best effort)
UPDATE roles SET permission_list = CASE
    WHEN permissions & (1 << 31) != 0 THEN ARRAY['ADMINISTRATOR']
    ELSE ARRAY[]::TEXT[]
END WHERE permission_list = '{}' AND permissions != 0;

-- Drop old column
ALTER TABLE roles DROP COLUMN IF EXISTS permissions;

-- Rename new column
ALTER TABLE roles RENAME COLUMN permission_list TO permissions;

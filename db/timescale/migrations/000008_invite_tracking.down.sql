DROP TABLE IF EXISTS invite_uses;
ALTER TABLE guild_members DROP COLUMN IF EXISTS invite_code;
ALTER TABLE guild_members DROP COLUMN IF EXISTS invited_by;

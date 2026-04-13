CREATE INDEX IF NOT EXISTS idx_channels_parent ON channels(parent_id);
CREATE INDEX IF NOT EXISTS idx_bans_user ON bans(user_id);
CREATE INDEX IF NOT EXISTS idx_invites_guild ON invites(guild_id);
CREATE INDEX IF NOT EXISTS idx_media_uploader ON media_files(uploader_id);
CREATE INDEX IF NOT EXISTS idx_guild_members_guild ON guild_members(guild_id);

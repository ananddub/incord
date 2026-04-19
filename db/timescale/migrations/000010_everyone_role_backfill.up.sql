-- Backfill @everyone role into every existing guild and assign every current
-- member to it, so permission grants on "@everyone" apply retroactively.
-- Idempotent: safe to re-run via ON CONFLICT and WHERE NOT EXISTS guards.

-- 1) Insert @everyone row for every guild that doesn't already have one.
INSERT INTO roles (guild_id, name, color, position)
SELECT g.id, '@everyone', '', 0
FROM guilds g
WHERE g.deleted = FALSE
  AND NOT EXISTS (
    SELECT 1 FROM roles r
    WHERE r.guild_id = g.id
      AND r.name = '@everyone'
      AND r.deleted = FALSE
  );

-- 2) Assign every active guild member to their guild's @everyone role.
-- ON CONFLICT re-uses the existing row (re-activates a soft-deleted one
-- as a side-effect, which matches what AssignRole does on the hot path).
INSERT INTO role_members (role_id, user_id)
SELECT r.id, gm.user_id
FROM guild_members gm
JOIN roles r
  ON r.guild_id = gm.guild_id
 AND r.name = '@everyone'
 AND r.deleted = FALSE
WHERE gm.deleted = FALSE
ON CONFLICT (role_id, user_id) DO UPDATE
  SET deleted = FALSE, updated_at = NOW();

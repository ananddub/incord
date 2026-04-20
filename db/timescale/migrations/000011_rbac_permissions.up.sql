-- Replace OpenFGA with a plain Postgres-backed RBAC.
--
-- Two new tables:
--   permissions      — canonical catalogue of Discord-style permission names
--   role_permissions — join table: which permissions a role grants
--
-- Guild owner stays authoritative via guilds.owner_id. Admin is just a
-- row in permissions named 'ADMINISTRATOR' that short-circuits every
-- Can* check when a user has it through any assigned role.

CREATE TABLE permissions (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT ''
);

-- Seed all 50 Discord permissions. Names match the guild.v1.Permission
-- proto enum so the Go client can map can_* relation strings → name
-- via authz.PermissionRelation without a second translation table.
INSERT INTO permissions (name, description) VALUES
  ('VIEW_CHANNELS',                        'See channels in the guild'),
  ('SEND_MESSAGES',                        'Post messages in text channels'),
  ('MANAGE_MESSAGES',                      'Delete/pin others'' messages'),
  ('MANAGE_CHANNELS',                      'Create, edit, delete channels'),
  ('MANAGE_GUILD',                         'Edit guild settings'),
  ('KICK_MEMBERS',                         'Remove members from the guild'),
  ('BAN_MEMBERS',                          'Ban members from re-joining'),
  ('MANAGE_ROLES',                         'Create, edit, delete, assign roles'),
  ('MANAGE_INVITES',                       'Create or delete invite codes'),
  ('ADD_REACTIONS',                        'Add new reactions to messages'),
  ('CONNECT',                              'Join voice / stage channels'),
  ('SPEAK',                                'Transmit audio in voice channels'),
  ('STREAM',                               'Publish camera / screen share'),
  ('MUTE_MEMBERS',                         'Server-mute other members'),
  ('DEAFEN_MEMBERS',                       'Server-deafen other members'),
  ('MENTION_EVERYONE',                     'Use @everyone or @here'),
  ('MANAGE_EMOJIS',                        'Edit or delete guild emojis'),
  ('MANAGE_WEBHOOKS',                      'Create, edit, delete webhooks'),
  ('ADMINISTRATOR',                        'Bypasses every permission check'),
  ('VIEW_AUDIT_LOG',                       'View the guild''s audit trail'),
  ('VIEW_GUILD_INSIGHTS',                  'View guild analytics'),
  ('MANAGE_NICKNAMES',                     'Change other members'' nicknames'),
  ('CHANGE_NICKNAME',                      'Change one''s own nickname'),
  ('CREATE_GUILD_EXPRESSIONS',             'Create emojis, stickers, sounds'),
  ('MODERATE_MEMBERS',                     'Timeout members'),
  ('VIEW_CREATOR_MONETIZATION_ANALYTICS',  'View monetization analytics'),
  ('SEND_TTS_MESSAGES',                    'Use /tts'),
  ('EMBED_LINKS',                          'Auto-expand shared links'),
  ('ATTACH_FILES',                         'Upload attachments'),
  ('READ_MESSAGE_HISTORY',                 'Read past messages'),
  ('USE_EXTERNAL_EMOJIS',                  'Use emojis from other guilds'),
  ('USE_EXTERNAL_STICKERS',                'Use stickers from other guilds'),
  ('USE_EXTERNAL_SOUNDS',                  'Use soundboard sounds from other guilds'),
  ('USE_EXTERNAL_APPS',                    'Deploy user-installed apps'),
  ('PRIORITY_SPEAKER',                     'Duck other voice on speak'),
  ('MOVE_MEMBERS',                         'Move members between voice channels'),
  ('USE_VAD',                              'Use voice activity detection'),
  ('REQUEST_TO_SPEAK',                     'Request to speak in stage channels'),
  ('USE_SOUNDBOARD',                       'Play soundboard sounds'),
  ('USE_EMBEDDED_ACTIVITIES',              'Launch embedded activities'),
  ('SEND_VOICE_MESSAGES',                  'Record and send voice messages'),
  ('SEND_POLLS',                           'Create polls'),
  ('PIN_MESSAGES',                         'Pin / unpin messages'),
  ('BYPASS_SLOWMODE',                      'Send regardless of rate-limit'),
  ('CREATE_PUBLIC_THREADS',                'Start public threads'),
  ('CREATE_PRIVATE_THREADS',               'Start private threads'),
  ('SEND_MESSAGES_IN_THREADS',             'Post in threads'),
  ('MANAGE_THREADS',                       'Delete / archive threads'),
  ('CREATE_EVENTS',                        'Create scheduled events'),
  ('MANAGE_EVENTS',                        'Edit / delete scheduled events');

-- Join table. CASCADE on role_id so deleting a role auto-cleans grants;
-- RESTRICT on permission_id so a catalogue row can never be dropped while
-- referenced (would silently strip permissions from every role that had it).
CREATE TABLE role_permissions (
    role_id       UUID   NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE RESTRICT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id)
);
CREATE INDEX idx_role_permissions_perm ON role_permissions(permission_id);

-- Seed @everyone baseline in every existing guild: the permissions
-- Discord enables by default for a fresh server so old users aren't
-- suddenly locked out of public channels after the cutover.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = '@everyone' AND r.deleted = FALSE
  AND p.name IN (
    'VIEW_CHANNELS','SEND_MESSAGES','READ_MESSAGE_HISTORY',
    'ADD_REACTIONS','CONNECT','SPEAK','USE_VAD',
    'CHANGE_NICKNAME','ATTACH_FILES','EMBED_LINKS',
    'USE_EXTERNAL_EMOJIS','USE_SOUNDBOARD','REQUEST_TO_SPEAK'
  )
ON CONFLICT DO NOTHING;

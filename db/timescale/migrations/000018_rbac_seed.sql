-- +goose Up
-- +goose StatementBegin
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
  ('MANAGE_EVENTS',                        'Edit / delete scheduled events')
ON CONFLICT (name) DO UPDATE
  SET description = EXCLUDED.description;

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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE permission_id IN (
  SELECT id
  FROM permissions
  WHERE name IN (
    'VIEW_CHANNELS','SEND_MESSAGES','MANAGE_MESSAGES','MANAGE_CHANNELS',
    'MANAGE_GUILD','KICK_MEMBERS','BAN_MEMBERS','MANAGE_ROLES',
    'MANAGE_INVITES','ADD_REACTIONS','CONNECT','SPEAK','STREAM',
    'MUTE_MEMBERS','DEAFEN_MEMBERS','MENTION_EVERYONE','MANAGE_EMOJIS',
    'MANAGE_WEBHOOKS','ADMINISTRATOR','VIEW_AUDIT_LOG','VIEW_GUILD_INSIGHTS',
    'MANAGE_NICKNAMES','CHANGE_NICKNAME','CREATE_GUILD_EXPRESSIONS',
    'MODERATE_MEMBERS','VIEW_CREATOR_MONETIZATION_ANALYTICS','SEND_TTS_MESSAGES',
    'EMBED_LINKS','ATTACH_FILES','READ_MESSAGE_HISTORY','USE_EXTERNAL_EMOJIS',
    'USE_EXTERNAL_STICKERS','USE_EXTERNAL_SOUNDS','USE_EXTERNAL_APPS',
    'PRIORITY_SPEAKER','MOVE_MEMBERS','USE_VAD','REQUEST_TO_SPEAK',
    'USE_SOUNDBOARD','USE_EMBEDDED_ACTIVITIES','SEND_VOICE_MESSAGES',
    'SEND_POLLS','PIN_MESSAGES','BYPASS_SLOWMODE','CREATE_PUBLIC_THREADS',
    'CREATE_PRIVATE_THREADS','SEND_MESSAGES_IN_THREADS','MANAGE_THREADS',
    'CREATE_EVENTS','MANAGE_EVENTS'
  )
);

DELETE FROM permissions
WHERE name IN (
  'VIEW_CHANNELS','SEND_MESSAGES','MANAGE_MESSAGES','MANAGE_CHANNELS',
  'MANAGE_GUILD','KICK_MEMBERS','BAN_MEMBERS','MANAGE_ROLES',
  'MANAGE_INVITES','ADD_REACTIONS','CONNECT','SPEAK','STREAM',
  'MUTE_MEMBERS','DEAFEN_MEMBERS','MENTION_EVERYONE','MANAGE_EMOJIS',
  'MANAGE_WEBHOOKS','ADMINISTRATOR','VIEW_AUDIT_LOG','VIEW_GUILD_INSIGHTS',
  'MANAGE_NICKNAMES','CHANGE_NICKNAME','CREATE_GUILD_EXPRESSIONS',
  'MODERATE_MEMBERS','VIEW_CREATOR_MONETIZATION_ANALYTICS','SEND_TTS_MESSAGES',
  'EMBED_LINKS','ATTACH_FILES','READ_MESSAGE_HISTORY','USE_EXTERNAL_EMOJIS',
  'USE_EXTERNAL_STICKERS','USE_EXTERNAL_SOUNDS','USE_EXTERNAL_APPS',
  'PRIORITY_SPEAKER','MOVE_MEMBERS','USE_VAD','REQUEST_TO_SPEAK',
  'USE_SOUNDBOARD','USE_EMBEDDED_ACTIVITIES','SEND_VOICE_MESSAGES',
  'SEND_POLLS','PIN_MESSAGES','BYPASS_SLOWMODE','CREATE_PUBLIC_THREADS',
  'CREATE_PRIVATE_THREADS','SEND_MESSAGES_IN_THREADS','MANAGE_THREADS',
  'CREATE_EVENTS','MANAGE_EVENTS'
);
-- +goose StatementEnd


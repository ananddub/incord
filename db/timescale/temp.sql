create table if not exists users
(
    id               uuid                     default gen_random_uuid()            not null primary key,
    username         varchar(64)                                                   not null unique,
    email            varchar(255)                                                  not null unique,
    password_hash    text                                                          not null,
    avatar_url       text                     default ''::text                     not null,
    bio              text                     default ''::text                     not null,
    status           varchar(20)              default 'offline'::character varying not null,
    created_at       timestamp with time zone default now()                        not null,
    updated_at       timestamp with time zone default now()                        not null,
    verified         boolean                  default false                        not null,
    deleted          boolean                  default false                        not null,
    background_color text                     default ''::text                     not null,
    display_name     varchar(32)              default ''::character varying        not null
);

create index if not exists idx_users_email
    on users (email);

create index if not exists idx_users_username
    on users (username);

create table if not exists friendships
(
    user_id    uuid                                                          not null references users on delete cascade,
    friend_id  uuid                                                          not null references users on delete cascade,
    status     varchar(20)              default 'pending'::character varying not null,
    created_at timestamp with time zone default now()                        not null,
    deleted    boolean                  default false                        not null,
    updated_at timestamp with time zone default now()                        not null,
    primary key (user_id, friend_id)
);

create index if not exists idx_friendships_friend
    on friendships (friend_id);

create table if not exists guilds
(
    id          uuid                     default gen_random_uuid() not null
        primary key,
    name        varchar(100)                                       not null,
    description text                     default ''::text          not null,
    icon_url    text                     default ''::text          not null,
    owner_id    uuid                                               not null
        references users,
    created_at  timestamp with time zone default now()             not null,
    deleted     boolean                  default false             not null,
    updated_at  timestamp with time zone default now()             not null,
    banner_url  text                     default ''::text          not null
);

create table if not exists guild_members
(
    guild_id    uuid                                                   not null
        references guilds
            on delete cascade,
    user_id     uuid                                                   not null
        references users
            on delete cascade,
    nickname    varchar(32)              default ''::character varying not null,
    joined_at   timestamp with time zone default now()                 not null,
    deleted     boolean                  default false                 not null,
    updated_at  timestamp with time zone default now()                 not null,
    invite_code varchar(10)              default NULL::character varying,
    invited_by  uuid
        references users,
    primary key (guild_id, user_id)
);

create index if not exists idx_guild_members_user
    on guild_members (user_id);

create index if not exists idx_guild_members_guild
    on guild_members (guild_id);

create table if not exists roles
(
    id         uuid                     default gen_random_uuid()            not null
        primary key,
    guild_id   uuid                                                          not null
        references guilds
            on delete cascade,
    name       varchar(100)                                                  not null,
    color      varchar(7)               default '#99AAB5'::character varying not null,
    position   integer                  default 0                            not null,
    created_at timestamp with time zone default now()                        not null,
    deleted    boolean                  default false                        not null,
    updated_at timestamp with time zone default now()                        not null
);

create index if not exists idx_roles_guild
    on roles (guild_id);

create table if not exists role_members
(
    role_id    uuid                                   not null
        references roles
            on delete cascade,
    user_id    uuid                                   not null
        references users
            on delete cascade,
    deleted    boolean                  default false not null,
    updated_at timestamp with time zone default now() not null,
    primary key (role_id, user_id)
);

create table if not exists channels
(
    id         uuid                     default gen_random_uuid() not null
        primary key,
    guild_id   uuid
        references guilds
            on delete cascade,
    name       varchar(100)                                       not null,
    type       integer                  default 1                 not null,
    topic      text                     default ''::text          not null,
    position   integer                  default 0                 not null,
    parent_id  uuid
                                                                  references channels
                                                                      on delete set null,
    created_at timestamp with time zone default now()             not null,
    deleted    boolean                  default false             not null,
    updated_at timestamp with time zone default now()             not null
);

create index if not exists idx_channels_guild
    on channels (guild_id);

create index if not exists idx_channels_parent
    on channels (parent_id);

create table if not exists dm_channel_members
(
    channel_id uuid                                   not null
        references channels
            on delete cascade,
    user_id    uuid                                   not null
        references users
            on delete cascade,
    deleted    boolean                  default false not null,
    updated_at timestamp with time zone default now() not null,
    primary key (channel_id, user_id)
);

create index if not exists idx_dm_members_user
    on dm_channel_members (user_id);

create table if not exists invites
(
    code       varchar(10)                            not null
        primary key,
    guild_id   uuid                                   not null
        references guilds
            on delete cascade,
    channel_id uuid                                   not null
        references channels
            on delete cascade,
    creator_id uuid                                   not null
        references users,
    max_uses   integer                  default 0     not null,
    uses       integer                  default 0     not null,
    expires_at timestamp with time zone,
    created_at timestamp with time zone default now() not null,
    deleted    boolean                  default false not null,
    updated_at timestamp with time zone default now() not null
);

create index if not exists idx_invites_guild
    on invites (guild_id);

create table if not exists bans
(
    guild_id   uuid                                      not null
        references guilds
            on delete cascade,
    user_id    uuid                                      not null
        references users
            on delete cascade,
    reason     text                     default ''::text not null,
    created_at timestamp with time zone default now()    not null,
    deleted    boolean                  default false    not null,
    updated_at timestamp with time zone default now()    not null,
    primary key (guild_id, user_id)
);

create index if not exists idx_bans_user
    on bans (user_id);

create table if not exists emojis
(
    id         uuid                     default gen_random_uuid() not null
        primary key,
    guild_id   uuid                                               not null
        references guilds
            on delete cascade,
    name       varchar(32)                                        not null,
    image_url  text                                               not null,
    creator_id uuid                                               not null
        references users,
    created_at timestamp with time zone default now()             not null
);

create table if not exists webhooks
(
    id         uuid                     default gen_random_uuid() not null
        primary key,
    channel_id uuid                                               not null
        references channels
            on delete cascade,
    guild_id   uuid                                               not null
        references guilds
            on delete cascade,
    name       varchar(80)                                        not null,
    avatar_url text                     default ''::text          not null,
    token      text                                               not null,
    created_at timestamp with time zone default now()             not null
);

create table if not exists media_files
(
    id           uuid                     default gen_random_uuid() not null
        primary key,
    uploader_id  uuid                                               not null
        references users,
    filename     text                                               not null,
    content_type varchar(255)                                       not null,
    size         bigint                                             not null,
    bucket_key   text                                               not null,
    confirmed    boolean                  default false             not null,
    created_at   timestamp with time zone default now()             not null,
    deleted      boolean                  default false             not null,
    updated_at   timestamp with time zone default now()             not null
);

create index if not exists idx_media_uploader
    on media_files (uploader_id);

create table if not exists invite_uses
(
    id          uuid                     default gen_random_uuid() not null
        primary key,
    invite_code varchar(10)                                        not null
        references invites
            on delete cascade,
    guild_id    uuid                                               not null
        references guilds
            on delete cascade,
    user_id     uuid                                               not null
        references users
            on delete cascade,
    inviter_id  uuid                                               not null
        references users,
    used_at     timestamp with time zone default now()             not null
);

create index if not exists idx_invite_uses_guild
    on invite_uses (guild_id);

create index if not exists idx_invite_uses_invite
    on invite_uses (invite_code);

create table if not exists permissions
(
    id          bigserial
        primary key,
    name        varchar(64)           not null
        unique,
    description text default ''::text not null
);

create table if not exists role_permissions
(
    role_id       uuid                                   not null
        references roles
            on delete cascade,
    permission_id bigint                                 not null
        references permissions
            on delete restrict,
    created_at    timestamp with time zone default now() not null,
    primary key (role_id, permission_id)
);

create index if not exists idx_role_permissions_perm
    on role_permissions (permission_id);

create table if not exists channel_permission_overwrites
(
    channel_id    uuid                                   not null
        references channels
            on delete cascade,
    target_type   varchar(10)                            not null
        constraint channel_permission_overwrites_target_type_check
            check ((target_type)::text = ANY ((ARRAY ['role'::character varying, 'user'::character varying])::text[])),
    target_id     uuid                                   not null,
    permission_id bigint                                 not null
        references permissions
            on delete restrict,
    effect        varchar(5)                             not null
        constraint channel_permission_overwrites_effect_check
            check ((effect)::text = ANY ((ARRAY ['allow'::character varying, 'deny'::character varying])::text[])),
    created_at    timestamp with time zone default now() not null,
    primary key (channel_id, target_type, target_id, permission_id)
);

create index if not exists idx_cpo_channel
    on channel_permission_overwrites (channel_id);

create index if not exists idx_cpo_target
    on channel_permission_overwrites (target_type, target_id);

-- === KEYSPACE ===
CREATE KEYSPACE myapp
WITH replication = {
    'class': 'SimpleStrategy',
    'replication_factor': 1
};

-- === TABLES ===

CREATE TABLE IF NOT EXISTS myapp.users (
    id          uuid PRIMARY KEY,
    username    text,
    email       text,
    full_name   text,
    is_active   boolean,
    tags        set<text>,
    metadata    map<text, text>,
    created_at  timestamp,
    updated_at  timestamp
);

CREATE INDEX IF NOT EXISTS myapp.users_email_idx ON myapp.users (email);

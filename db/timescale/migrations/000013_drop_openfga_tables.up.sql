-- Remove OpenFGA's own schema tables from the shared ndiscord database.
-- OpenFGA was installed against Postgres and wrote these; after the
-- swap to Postgres-backed RBAC (migration 000011) nothing reads or
-- writes them anymore. Kept in place for one release to allow rollback;
-- now safe to drop.

DROP TABLE IF EXISTS assertion;
DROP TABLE IF EXISTS changelog;
DROP TABLE IF EXISTS tuple;
DROP TABLE IF EXISTS authorization_model;
DROP TABLE IF EXISTS store;
-- Defensive: other OpenFGA schema variants name these differently.
DROP TABLE IF EXISTS continuation_token;
DROP TABLE IF EXISTS openfga_objects;
DROP TABLE IF EXISTS openfga_tuple;

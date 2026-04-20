-- No-op: OpenFGA's own schema migration would be complex to recreate
-- by hand. If a rollback is needed, run `openfga migrate` against this
-- database (restores all tables with proper constraints).
SELECT 1;

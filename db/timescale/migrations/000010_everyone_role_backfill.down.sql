-- No-op: we don't delete @everyone roles on rollback because removing them
-- would also drop any permission grants pinned to the role. If you need to
-- revert, delete the roles manually and clear OpenFGA tuples for role:<id>.
SELECT 1;

-- 0019_user_sessions.down.sql
-- Logs everyone out (the sessions are dropped). Users are not touched.

DROP INDEX uq_users_username_lower;

DROP TABLE user_sessions;

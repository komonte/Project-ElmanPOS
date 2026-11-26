-- 0017_pos_sessions.up.sql
-- F-11 (open the register) and F-07/F-10 (sell) before F-13 (login).
--
-- 1. There is no user yet to record who opened a session. Like 0013: the
--    column accepts NULL until there is a login, and the app does not invent
--    an author.
-- 2. BR-02: at most one OPEN session per register. The app checks it, and
--    this partial unique index makes a second OPEN row impossible even when
--    two screens open the same register at the same time.
-- 3. F-10: a weight above this ceiling is rejected as a typing mistake
--    (3000 instead of 3.000). Lives in app_settings so it can change without
--    a new build. Inserted here, not in the seed: a database without the
--    seed (the real catalog) needs it too.

ALTER TABLE cashier_sessions ALTER COLUMN opened_by DROP NOT NULL;

CREATE UNIQUE INDEX uq_cashier_sessions_one_open
    ON cashier_sessions (cash_register_id)
    WHERE status = 'OPEN';

INSERT INTO app_settings (key, value, description)
VALUES ('max_sale_weight_kg', '50'::jsonb, 'Largest weight (kg) accepted on one sale line (F-10)')
ON CONFLICT (key) DO NOTHING;

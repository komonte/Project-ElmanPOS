-- 0018_pos_close.down.sql
-- Drops the void date and reason of every voided sale (the audit_log rows
-- keep them). The status stays VOIDED.

DROP INDEX uq_cash_audits_session_method;
CREATE INDEX idx_cash_audits_session_method ON cash_audits (cashier_session_id, method);

ALTER TABLE sales
    DROP CONSTRAINT chk_sales_void,
    DROP COLUMN void_reason,
    DROP COLUMN voided_at;

-- 0018_pos_close.up.sql
-- F-08 (void a sale) and F-12 (close the register with a cash audit).
--
-- 1. A void keeps when and why on the sale itself, to show them in the
--    session's sale list and on the ticket (the audit_log row has them too).
--    Either the sale is VOIDED with both, or it has neither; a reason cannot
--    be blank.
-- 2. F-12: one declared amount per payment method and session. The plain
--    index from 0004 becomes unique (same columns, so it keeps serving the
--    lookups).

ALTER TABLE sales
    ADD COLUMN voided_at   TIMESTAMPTZ,
    ADD COLUMN void_reason VARCHAR(300),
    ADD CONSTRAINT chk_sales_void CHECK (
        (status = 'VOIDED') = (voided_at IS NOT NULL)
        AND (status = 'VOIDED') = (void_reason IS NOT NULL)
        AND (void_reason IS NULL OR btrim(void_reason) <> '')
    );

DROP INDEX idx_cash_audits_session_method;
CREATE UNIQUE INDEX uq_cash_audits_session_method ON cash_audits (cashier_session_id, method);

-- 0006_system.up.sql
-- System domain: audit log and global settings. 1:1 from docs/02-data-model.md (02 4.24 / 4.25).

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT REFERENCES users(id),
    table_name VARCHAR(80) NOT NULL,
    action     VARCHAR(40) NOT NULL,
    record_id  BIGINT,
    old_data   JSONB,
    new_data   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_log_table_name ON audit_log (table_name);
CREATE INDEX idx_audit_log_user_id ON audit_log (user_id);

CREATE TABLE app_settings (
    key         VARCHAR(80) PRIMARY KEY,
    value       JSONB NOT NULL,
    description VARCHAR(300),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Persist administrator-run model degradation checks so results survive
-- page reloads and can be compared over time.
CREATE TABLE IF NOT EXISTS account_degradation_check_results (
    id              BIGSERIAL PRIMARY KEY,
    account_id      BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    check_type      VARCHAR(32) NOT NULL,
    requested_model VARCHAR(256) NOT NULL,
    tested_model    VARCHAR(256) NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL,
    result          JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_text     TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_degradation_check_type_valid
        CHECK (check_type IN ('model_trace', 'svg_animation')),
    CONSTRAINT account_degradation_check_status_valid
        CHECK (status IN ('success', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_account_degradation_check_history
    ON account_degradation_check_results(account_id, check_type, created_at DESC, id DESC);

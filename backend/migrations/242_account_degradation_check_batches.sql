CREATE TABLE account_degradation_check_batches (
    id BIGSERIAL PRIMARY KEY,
    check_type VARCHAR(32) NOT NULL CHECK (check_type IN ('model_trace', 'svg_animation')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'running', 'canceling', 'completed', 'canceled')),
    created_by BIGINT NOT NULL,
    request_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    cancel_requested_at TIMESTAMPTZ,
    canceled_by BIGINT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (created_by, request_key)
);
CREATE INDEX idx_degradation_batches_created ON account_degradation_check_batches(created_at DESC, id DESC);

CREATE TABLE account_degradation_check_batch_items (
    id BIGSERIAL PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES account_degradation_check_batches(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    account_name TEXT NOT NULL,
    platform TEXT NOT NULL,
    account_type TEXT NOT NULL,
    requested_model VARCHAR(256) NOT NULL,
    tested_model VARCHAR(256) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped', 'canceled', 'interrupted')),
    reason_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    progress JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_text TEXT NOT NULL DEFAULT '',
    history_id BIGINT REFERENCES account_degradation_check_results(id) ON DELETE SET NULL,
    claim_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (batch_id, account_id),
    UNIQUE (batch_id, position)
);
CREATE INDEX idx_degradation_items_pending ON account_degradation_check_batch_items(batch_id, position) WHERE status = 'pending';
CREATE INDEX idx_degradation_items_lease ON account_degradation_check_batch_items(lease_expires_at, batch_id) WHERE status = 'running';
CREATE INDEX idx_degradation_items_order ON account_degradation_check_batch_items(batch_id, position);
CREATE INDEX idx_degradation_items_running_account ON account_degradation_check_batch_items(account_id, lease_expires_at) WHERE status = 'running';

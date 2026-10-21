-- 自有生图工具台迁移；发布后保持文件名与内容不变。
CREATE TABLE image_workbench_jobs (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id UUID NOT NULL,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    input JSONB NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('running','complete','partial','failed','cancelled','interrupted')),
    reserved_bytes BIGINT NOT NULL CHECK (reserved_bytes >= 0),
    deleting BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, request_id)
);
CREATE UNIQUE INDEX idx_image_workbench_one_running ON image_workbench_jobs(user_id) WHERE status='running';
CREATE INDEX idx_image_workbench_jobs_user ON image_workbench_jobs(user_id, created_at DESC, id);

CREATE TABLE image_workbench_assets (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    mime_type VARCHAR(32) NOT NULL CHECK (mime_type IN ('image/png','image/jpeg','image/webp')),
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 20971520),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    draft BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_image_workbench_assets_user ON image_workbench_assets(user_id);

CREATE TABLE image_workbench_items (
    job_id UUID NOT NULL REFERENCES image_workbench_jobs(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 3),
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending','complete','failed','cancelled','interrupted')),
    asset_id UUID REFERENCES image_workbench_assets(id),
    error TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY(job_id, position)
);

CREATE TABLE image_workbench_references (
    job_id UUID NOT NULL REFERENCES image_workbench_jobs(id) ON DELETE CASCADE,
    asset_id UUID NOT NULL REFERENCES image_workbench_assets(id),
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 4),
    PRIMARY KEY(job_id, asset_id),
    UNIQUE(job_id, position)
);

-- 清理历史时按图片查找跨任务引用，避免扫描其他用户的全部历史。
CREATE INDEX idx_image_workbench_items_asset ON image_workbench_items(asset_id) WHERE asset_id IS NOT NULL;
CREATE INDEX idx_image_workbench_references_asset ON image_workbench_references(asset_id);

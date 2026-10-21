-- 自有迁移使用 custom_eezd 前缀，避免占用上游数字编号；首次发布后保持文件名与内容不变。
CREATE TABLE chat_conversations (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(100) NOT NULL,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    model VARCHAR(200) NOT NULL DEFAULT '',
    deleting BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_chat_conversations_user ON chat_conversations(user_id, updated_at DESC, id);

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY,
    position BIGINT GENERATED ALWAYS AS IDENTITY,
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    turn_id UUID NOT NULL,
    role VARCHAR(16) NOT NULL CHECK(role IN ('user', 'assistant')),
    content TEXT NOT NULL DEFAULT '',
    reasoning TEXT NOT NULL DEFAULT '',
    model VARCHAR(200) NOT NULL,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    status VARCHAR(16) NOT NULL CHECK(status IN ('complete', 'generating', 'interrupted', 'failed')),
    revision BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(conversation_id, turn_id, role)
);
CREATE INDEX idx_chat_messages_conversation ON chat_messages(conversation_id, position);
CREATE UNIQUE INDEX idx_chat_one_generation ON chat_messages(conversation_id) WHERE status = 'generating';

CREATE TABLE chat_attachments (
    id UUID PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    message_id UUID REFERENCES chat_messages(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    size BIGINT NOT NULL CHECK(size > 0 AND size <= 20971520),
    extracted_text TEXT NOT NULL DEFAULT '' CHECK(octet_length(extracted_text) <= 2000000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_chat_attachments_user ON chat_attachments(user_id);
CREATE INDEX idx_chat_attachments_conversation ON chat_attachments(conversation_id, created_at);

BEGIN;

CREATE TABLE IF NOT EXISTS api_tokens (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE ON UPDATE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL ON UPDATE CASCADE,
    name VARCHAR(255) NOT NULL,
    token_hash CHAR(64) NOT NULL,
    token_prefix VARCHAR(8) NOT NULL,
    scopes JSONB NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE,
    last_used_at TIMESTAMP WITH TIME ZONE,
    last_used_ip VARCHAR(64) NOT NULL DEFAULT '',
    revoked_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_api_tokens_tenant_name UNIQUE (tenant_id, name),
    CONSTRAINT uq_api_tokens_token_hash UNIQUE (token_hash)
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_tenant_id ON api_tokens (tenant_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_id ON api_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_revoked_at ON api_tokens (revoked_at);
CREATE INDEX IF NOT EXISTS idx_api_tokens_created_at ON api_tokens (created_at);

COMMIT;

BEGIN;

CREATE TABLE IF NOT EXISTS node_enroll_tokens (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE ON UPDATE CASCADE,
    network_id UUID REFERENCES virtual_networks(id) ON DELETE SET NULL ON UPDATE CASCADE,
    name_hint VARCHAR(255) NOT NULL DEFAULT '',
    token_hash CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    max_uses INTEGER NOT NULL DEFAULT 1,
    used_count INTEGER NOT NULL DEFAULT 0,
    revoked_at TIMESTAMP WITH TIME ZONE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL ON UPDATE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_tenant_id ON node_enroll_tokens (tenant_id);
CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_network_id ON node_enroll_tokens (network_id);
CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_created_by ON node_enroll_tokens (created_by);
CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_expires_at ON node_enroll_tokens (expires_at);
CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_revoked_at ON node_enroll_tokens (revoked_at);
CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_created_at ON node_enroll_tokens (created_at);

CREATE TABLE IF NOT EXISTS node_enrollments (
    id UUID PRIMARY KEY,
    token_id UUID NOT NULL REFERENCES node_enroll_tokens(id) ON DELETE CASCADE ON UPDATE CASCADE,
    node_id UUID NOT NULL UNIQUE REFERENCES nodes(id) ON DELETE CASCADE ON UPDATE CASCADE,
    request_hash CHAR(64) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_node_enrollments_token_request UNIQUE (token_id, request_hash)
);

CREATE INDEX IF NOT EXISTS idx_node_enrollments_token_id ON node_enrollments (token_id);
CREATE INDEX IF NOT EXISTS idx_node_enrollments_node_id ON node_enrollments (node_id);
CREATE INDEX IF NOT EXISTS idx_node_enrollments_created_at ON node_enrollments (created_at);

COMMIT;

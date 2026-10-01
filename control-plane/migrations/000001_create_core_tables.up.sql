BEGIN;

CREATE TABLE tenants (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    plan VARCHAR(64) NOT NULL DEFAULT 'free',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_tenants_created_at ON tenants (created_at);

CREATE TABLE users (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_users_tenant_id ON users (tenant_id);
CREATE INDEX idx_users_created_at ON users (created_at);

CREATE TABLE certificates (
    id UUID PRIMARY KEY,
    domain VARCHAR(255) NOT NULL,
    issuer VARCHAR(255) NOT NULL,
    cert_pem TEXT NOT NULL,
    key_pem TEXT NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_certificates_domain ON certificates (domain);

CREATE TABLE domains (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    domain VARCHAR(255) NOT NULL,
    cert_id UUID REFERENCES certificates(id) ON DELETE SET NULL ON UPDATE CASCADE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_domains_tenant_id ON domains (tenant_id);
CREATE INDEX idx_domains_cert_id ON domains (cert_id);
CREATE INDEX idx_domains_created_at ON domains (created_at);

CREATE TABLE proxy_rules (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    domain_id UUID NOT NULL REFERENCES domains(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    path VARCHAR(255) NOT NULL DEFAULT '/',
    target_type VARCHAR(32) NOT NULL,
    target VARCHAR(255) NOT NULL,
    access_control JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_proxy_rules_tenant_id ON proxy_rules (tenant_id);
CREATE INDEX idx_proxy_rules_domain_id ON proxy_rules (domain_id);
CREATE INDEX idx_proxy_rules_created_at ON proxy_rules (created_at);

CREATE TABLE nodes (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    name VARCHAR(255) NOT NULL,
    public_key TEXT NOT NULL,
    virtual_ip INET,
    os VARCHAR(64) NOT NULL,
    arch VARCHAR(64) NOT NULL,
    version VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'offline',
    last_seen TIMESTAMP WITH TIME ZONE,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    agent_token_hash CHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_nodes_tenant_id ON nodes (tenant_id);
CREATE INDEX idx_nodes_status ON nodes (status);
CREATE INDEX idx_nodes_last_seen ON nodes (last_seen);
CREATE INDEX idx_nodes_created_at ON nodes (created_at);

CREATE TABLE virtual_networks (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    name VARCHAR(255) NOT NULL,
    cidr CIDR NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_virtual_networks_tenant_id ON virtual_networks (tenant_id);
CREATE INDEX idx_virtual_networks_created_at ON virtual_networks (created_at);

CREATE TABLE network_members (
    id UUID PRIMARY KEY,
    network_id UUID NOT NULL REFERENCES virtual_networks(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    virtual_ip INET NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'member',
    joined_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_network_members_network_id ON network_members (network_id);
CREATE INDEX idx_network_members_node_id ON network_members (node_id);
CREATE INDEX idx_network_members_joined_at ON network_members (joined_at);

CREATE TABLE acl_rules (
    id UUID PRIMARY KEY,
    network_id UUID NOT NULL REFERENCES virtual_networks(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    src VARCHAR(255) NOT NULL,
    dst VARCHAR(255) NOT NULL,
    action VARCHAR(16) NOT NULL,
    protocol VARCHAR(16) NOT NULL DEFAULT 'any',
    ports VARCHAR(255) NOT NULL DEFAULT 'any',
    priority INTEGER NOT NULL
);
CREATE INDEX idx_acl_rules_network_id ON acl_rules (network_id);
CREATE INDEX idx_acl_rules_priority ON acl_rules (priority);

CREATE TABLE subnet_routes (
    id UUID PRIMARY KEY,
    network_id UUID NOT NULL REFERENCES virtual_networks(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    cidr CIDR NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_subnet_routes_network_id ON subnet_routes (network_id);
CREATE INDEX idx_subnet_routes_node_id ON subnet_routes (node_id);

CREATE TABLE relay_servers (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    endpoint VARCHAR(255) NOT NULL,
    region VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'unknown',
    last_seen TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_relay_servers_last_seen ON relay_servers (last_seen);

CREATE TABLE config_versions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    target_type VARCHAR(32) NOT NULL,
    target_id UUID NOT NULL,
    version INTEGER NOT NULL,
    config JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_config_versions_tenant_id ON config_versions (tenant_id);
CREATE INDEX idx_config_versions_target_id ON config_versions (target_id);
CREATE INDEX idx_config_versions_created_at ON config_versions (created_at);

CREATE TABLE audit_logs (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id) ON DELETE SET NULL ON UPDATE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL ON UPDATE CASCADE,
    action VARCHAR(255) NOT NULL,
    resource VARCHAR(255) NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip INET NOT NULL DEFAULT '0.0.0.0',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_logs_tenant_id ON audit_logs (tenant_id);
CREATE INDEX idx_audit_logs_user_id ON audit_logs (user_id);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);

CREATE TABLE traffic_logs (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT ON UPDATE CASCADE,
    direction VARCHAR(8) NOT NULL,
    bytes BIGINT NOT NULL,
    protocol VARCHAR(32) NOT NULL,
    peer VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_traffic_logs_tenant_id ON traffic_logs (tenant_id);
CREATE INDEX idx_traffic_logs_node_id ON traffic_logs (node_id);
CREATE INDEX idx_traffic_logs_created_at ON traffic_logs (created_at);

COMMIT;

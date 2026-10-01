BEGIN;

ALTER TABLE nodes ADD COLUMN private_key TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN public_endpoint VARCHAR(255);
ALTER TABLE virtual_networks ADD COLUMN secret TEXT NOT NULL DEFAULT '';
ALTER TABLE config_versions ADD COLUMN reason VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE config_versions ADD COLUMN summary JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE UNIQUE INDEX idx_virtual_networks_tenant_name
    ON virtual_networks (tenant_id, name);
CREATE UNIQUE INDEX idx_network_members_network_node
    ON network_members (network_id, node_id);
CREATE UNIQUE INDEX idx_network_members_network_ip
    ON network_members (network_id, virtual_ip);
CREATE UNIQUE INDEX idx_config_versions_target_version
    ON config_versions (target_type, target_id, version);

COMMIT;

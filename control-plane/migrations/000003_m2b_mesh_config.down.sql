BEGIN;

DROP INDEX IF EXISTS idx_config_versions_target_version;
DROP INDEX IF EXISTS idx_network_members_network_ip;
DROP INDEX IF EXISTS idx_network_members_network_node;
DROP INDEX IF EXISTS idx_virtual_networks_tenant_name;

ALTER TABLE config_versions DROP COLUMN IF EXISTS summary;
ALTER TABLE config_versions DROP COLUMN IF EXISTS reason;
ALTER TABLE virtual_networks DROP COLUMN IF EXISTS secret;
ALTER TABLE nodes DROP COLUMN IF EXISTS public_endpoint;
ALTER TABLE nodes DROP COLUMN IF EXISTS private_key;

COMMIT;

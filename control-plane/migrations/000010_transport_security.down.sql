BEGIN;
ALTER TABLE proxy_rules DROP COLUMN IF EXISTS upstream_ca_file;
ALTER TABLE proxy_rules DROP COLUMN IF EXISTS upstream_insecure_skip_verify;
ALTER TABLE proxy_rules DROP COLUMN IF EXISTS upstream_scheme;
ALTER TABLE virtual_networks DROP COLUMN IF EXISTS preshared_key;
COMMIT;

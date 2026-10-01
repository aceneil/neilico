BEGIN;

DROP INDEX IF EXISTS idx_traffic_logs_node_created_at;
DROP INDEX IF EXISTS idx_domains_domain_unique;
DROP INDEX IF EXISTS idx_certificates_tenant_id;
ALTER TABLE certificates DROP COLUMN tenant_id;

COMMIT;

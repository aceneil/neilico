BEGIN;

ALTER TABLE certificates ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE RESTRICT ON UPDATE CASCADE;
UPDATE certificates SET tenant_id = (
    SELECT d.tenant_id FROM domains d WHERE d.cert_id = certificates.id LIMIT 1
) WHERE tenant_id IS NULL;
UPDATE certificates SET tenant_id = (
    SELECT id FROM tenants ORDER BY created_at LIMIT 1
) WHERE tenant_id IS NULL;
ALTER TABLE certificates ALTER COLUMN tenant_id SET NOT NULL;
CREATE INDEX idx_certificates_tenant_id ON certificates (tenant_id);

CREATE UNIQUE INDEX idx_domains_domain_unique ON domains (domain);
CREATE INDEX idx_traffic_logs_node_created_at ON traffic_logs (node_id, created_at);

COMMIT;

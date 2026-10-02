BEGIN;

CREATE TABLE IF NOT EXISTS cas (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    cert_pem TEXT NOT NULL,
    encrypted_key_pem TEXT NOT NULL,
    not_before TIMESTAMP WITH TIME ZONE NOT NULL,
    not_after TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cas_name ON cas (name);
CREATE INDEX IF NOT EXISTS idx_cas_not_after ON cas (not_after);
CREATE INDEX IF NOT EXISTS idx_cas_created_at ON cas (created_at);

CREATE TABLE IF NOT EXISTS node_certificates (
    id UUID PRIMARY KEY,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE ON UPDATE CASCADE,
    ca_id UUID NOT NULL REFERENCES cas(id) ON DELETE CASCADE ON UPDATE CASCADE,
    serial_number VARCHAR(128) NOT NULL,
    fingerprint VARCHAR(128) NOT NULL,
    cert_pem TEXT NOT NULL,
    encrypted_key_pem TEXT NOT NULL,
    not_before TIMESTAMP WITH TIME ZONE NOT NULL,
    not_after TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_node_certificates_node_id ON node_certificates (node_id);
CREATE INDEX IF NOT EXISTS idx_node_certificates_ca_id ON node_certificates (ca_id);
CREATE INDEX IF NOT EXISTS idx_node_certificates_serial_number ON node_certificates (serial_number);
CREATE INDEX IF NOT EXISTS idx_node_certificates_not_after ON node_certificates (not_after);
CREATE INDEX IF NOT EXISTS idx_node_certificates_created_at ON node_certificates (created_at);

COMMIT;

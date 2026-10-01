BEGIN;

DROP INDEX IF EXISTS idx_network_members_network_ip;
CREATE UNIQUE INDEX idx_network_members_network_ip
    ON network_members (network_id, virtual_ip);

COMMIT;

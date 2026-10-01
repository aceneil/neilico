BEGIN;

DROP INDEX IF EXISTS idx_network_members_network_ip;
CREATE UNIQUE INDEX idx_network_members_network_ip
    ON network_members (virtual_ip);

COMMIT;

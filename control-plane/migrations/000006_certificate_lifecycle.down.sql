BEGIN;

DROP INDEX IF EXISTS idx_certificates_created_at;
DROP INDEX IF EXISTS idx_certificates_expires_at;
DROP INDEX IF EXISTS idx_certificates_status;

ALTER TABLE certificates DROP COLUMN IF EXISTS created_at;
ALTER TABLE certificates DROP COLUMN IF EXISTS renewal_failures;
ALTER TABLE certificates DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE certificates DROP COLUMN IF EXISTS auto_renew;
ALTER TABLE certificates DROP COLUMN IF EXISTS challenge_type;
ALTER TABLE certificates DROP COLUMN IF EXISTS renew_count;
ALTER TABLE certificates DROP COLUMN IF EXISTS renewed_at;
ALTER TABLE certificates DROP COLUMN IF EXISTS last_error;
ALTER TABLE certificates DROP COLUMN IF EXISTS status;

COMMIT;

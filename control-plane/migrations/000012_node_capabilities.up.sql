BEGIN;

ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL
    DEFAULT '{"mesh":"unavailable","subnet_routes":"unavailable","tunnel":"unavailable","reason":"尚未上报"}'::jsonb;

COMMIT;

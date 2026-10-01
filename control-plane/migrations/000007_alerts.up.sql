BEGIN;

CREATE TABLE IF NOT EXISTS config_dispatch_failures (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE ON UPDATE CASCADE,
    target_type VARCHAR(32) NOT NULL,
    target_id UUID NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    failures INTEGER NOT NULL DEFAULT 1,
    failed_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_config_dispatch_target
    ON config_dispatch_failures (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_config_dispatch_tenant_id
    ON config_dispatch_failures (tenant_id);
CREATE INDEX IF NOT EXISTS idx_config_dispatch_failed_at
    ON config_dispatch_failures (failed_at);

CREATE TABLE IF NOT EXISTS alerts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE ON UPDATE CASCADE,
    rule VARCHAR(64) NOT NULL,
    severity VARCHAR(16) NOT NULL,
    target_type VARCHAR(32) NOT NULL,
    target_id UUID NOT NULL,
    title VARCHAR(255) NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    since TIMESTAMP WITH TIME ZONE NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    state VARCHAR(16) NOT NULL,
    evaluation_count INTEGER NOT NULL DEFAULT 0,
    last_evaluated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    resolved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_tenant_rule_target
    ON alerts (tenant_id, rule, target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_alerts_rule_target ON alerts (rule, target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_alerts_state ON alerts (state);
CREATE INDEX IF NOT EXISTS idx_alerts_severity ON alerts (severity);
CREATE INDEX IF NOT EXISTS idx_alerts_since ON alerts (since);
CREATE INDEX IF NOT EXISTS idx_alerts_last_evaluated_at ON alerts (last_evaluated_at);
CREATE INDEX IF NOT EXISTS idx_alerts_resolved_at ON alerts (resolved_at);

CREATE TABLE IF NOT EXISTS alert_events (
    id UUID PRIMARY KEY,
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE ON UPDATE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE ON UPDATE CASCADE,
    rule VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL,
    target_id UUID NOT NULL,
    state VARCHAR(16) NOT NULL,
    severity VARCHAR(16) NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_alert_events_alert_id_created_at
    ON alert_events (alert_id, created_at);
CREATE INDEX IF NOT EXISTS idx_alert_events_tenant_id ON alert_events (tenant_id);
CREATE INDEX IF NOT EXISTS idx_alert_events_rule ON alert_events (rule);
CREATE INDEX IF NOT EXISTS idx_alert_events_target ON alert_events (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_alert_events_created_at ON alert_events (created_at);

COMMIT;

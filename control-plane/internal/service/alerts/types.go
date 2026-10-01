package alerts

import (
	"time"

	"github.com/google/uuid"
)

const (
	RuleNodeOffline          = "node_offline"
	RuleCertificateExpiring  = "certificate_expiring"
	RuleP2PSuccessRateLow    = "p2p_success_rate_low"
	RuleRelayTrafficSpike    = "relay_traffic_spike"
	RuleConfigDispatchFailed = "config_dispatch_failed"

	StateFiring   = "firing"
	StateResolved = "resolved"

	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	DataStatusAvailable        = "available"
	DataStatusInsufficientData = "insufficient_data"
)

type Options struct {
	EvaluationInterval    time.Duration
	NodeOfflineAfter      time.Duration
	CertificateExpiringIn time.Duration
	CertificateCriticalIn time.Duration
	P2PSuccessRateMinimum float64
	RelaySpikeMultiplier  float64
	RelayBaselineWindow   time.Duration
	ResolvedRetention     time.Duration
	WebhookURL            string
	WebhookTimeout        time.Duration
	WebhookRetries        int
}

func DefaultOptions() Options {
	return Options{
		EvaluationInterval:    time.Minute,
		NodeOfflineAfter:      5 * time.Minute,
		CertificateExpiringIn: 30 * 24 * time.Hour,
		CertificateCriticalIn: 7 * 24 * time.Hour,
		P2PSuccessRateMinimum: 0.60,
		RelaySpikeMultiplier:  3,
		RelayBaselineWindow:   24 * time.Hour,
		ResolvedRetention:     7 * 24 * time.Hour,
		WebhookTimeout:        5 * time.Second,
		WebhookRetries:        3,
	}
}

func (o Options) normalized() Options {
	result := DefaultOptions()
	if o.EvaluationInterval > 0 {
		result.EvaluationInterval = o.EvaluationInterval
	}
	if o.NodeOfflineAfter > 0 {
		result.NodeOfflineAfter = o.NodeOfflineAfter
	}
	if o.CertificateExpiringIn > 0 {
		result.CertificateExpiringIn = o.CertificateExpiringIn
	}
	if o.CertificateCriticalIn > 0 {
		result.CertificateCriticalIn = o.CertificateCriticalIn
	}
	// A zero-valued Options is the common constructor form; preserve the
	// documented 60% default instead of treating it as an explicit 0% override.
	if o.P2PSuccessRateMinimum > 0 && o.P2PSuccessRateMinimum <= 1 {
		result.P2PSuccessRateMinimum = o.P2PSuccessRateMinimum
	}
	if o.RelaySpikeMultiplier > 0 {
		result.RelaySpikeMultiplier = o.RelaySpikeMultiplier
	}
	if o.RelayBaselineWindow > 0 {
		result.RelayBaselineWindow = o.RelayBaselineWindow
	}
	if o.ResolvedRetention > 0 {
		result.ResolvedRetention = o.ResolvedRetention
	}
	result.WebhookURL = o.WebhookURL
	if o.WebhookTimeout > 0 {
		result.WebhookTimeout = o.WebhookTimeout
	}
	if o.WebhookRetries > 0 && o.WebhookRetries <= 3 {
		result.WebhookRetries = o.WebhookRetries
	}
	return result
}

type Rule struct {
	ID                   string  `json:"id"`
	Condition            string  `json:"condition"`
	Severity             string  `json:"severity"`
	TargetType           string  `json:"target_type"`
	Threshold            float64 `json:"threshold"`
	ThresholdUnit        string  `json:"threshold_unit"`
	ThresholdDescription string  `json:"threshold_description"`
	DataStatus           string  `json:"data_status"`
}

type Alert struct {
	ID          string     `json:"id"`
	Rule        string     `json:"rule"`
	Severity    string     `json:"severity"`
	TargetType  string     `json:"target_type"`
	TargetID    string     `json:"target_id"`
	Title       string     `json:"title"`
	Detail      string     `json:"detail"`
	Value       float64    `json:"value"`
	Threshold   float64    `json:"threshold"`
	Since       time.Time  `json:"since"`
	State       string     `json:"state"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	DataStatus  string     `json:"data_status,omitempty"`
	TenantID    string     `json:"tenant_id,omitempty"`
	EvaluatedAt *time.Time `json:"evaluated_at,omitempty"`
}

type AlertEvent struct {
	ID         string    `json:"id"`
	AlertID    string    `json:"alert_id"`
	Rule       string    `json:"rule"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	State      string    `json:"state"`
	Severity   string    `json:"severity"`
	Value      float64   `json:"value"`
	Threshold  float64   `json:"threshold"`
	Detail     string    `json:"detail"`
	CreatedAt  time.Time `json:"created_at"`
}

type Notification struct {
	Event     string    `json:"event"`
	Schema    string    `json:"schema_version"`
	Alert     Alert     `json:"alert"`
	Timestamp time.Time `json:"timestamp"`
}

type ListFilter struct {
	TenantID   *uuid.UUID
	State      string
	Severity   string
	Rule       string
	TargetType string
	TargetID   *uuid.UUID
	Page       int
	PageSize   int
}

type List struct {
	Items    []Alert `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

type SeverityCounts struct {
	Critical int64 `json:"critical"`
	Warning  int64 `json:"warning"`
	Info     int64 `json:"info"`
}

type Summary struct {
	Firing         SeverityCounts   `json:"firing"`
	ResolvedRecent int64            `json:"resolved_recent"`
	ByRule         map[string]int64 `json:"by_rule"`
}

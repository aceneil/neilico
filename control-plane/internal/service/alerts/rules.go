package alerts

import (
	"context"
	"strings"
	"time"
)

type nodeObservation struct {
	ID       string
	Name     string
	LastSeen *time.Time
}

type dispatchFailureObservation struct {
	TargetType string
	TargetID   string
	LastError  string
	Failures   int
}

type certificateObservation struct {
	ID              string
	Domain          string
	Issuer          string
	ExpiresAt       *time.Time
	Status          string
	LastError       string
	RenewalFailures int
}

func (e *Engine) Rules() []Rule {
	options := e.options
	return []Rule{
		{
			ID: RuleNodeOffline, Condition: "node.last_seen age > threshold",
			Severity: SeverityWarning, TargetType: "node",
			Threshold: options.NodeOfflineAfter.Seconds(), ThresholdUnit: "seconds",
			ThresholdDescription: "节点最后心跳距今超过 5 分钟", DataStatus: DataStatusAvailable,
		},
		{
			ID: RuleCertificateExpiring, Condition: "certificate.status=active AND (certificate.expires_at - now) < threshold",
			Severity: SeverityWarning, TargetType: "certificate",
			Threshold: options.CertificateExpiringIn.Hours() / 24, ThresholdUnit: "days",
			ThresholdDescription: "30 天内过期；剩余不超过 7 天升级为 critical", DataStatus: DataStatusAvailable,
		},
		{
			ID: RuleP2PSuccessRateLow, Condition: "p2p.success_rate < threshold",
			Severity: SeverityWarning, TargetType: "platform",
			Threshold: options.P2PSuccessRateMinimum, ThresholdUnit: "ratio",
			ThresholdDescription: "P2P 打洞成功率低于 60%", DataStatus: DataStatusInsufficientData,
		},
		{
			ID: RuleRelayTrafficSpike, Condition: "relay.current >= relay.24h_mean * threshold",
			Severity: SeverityWarning, TargetType: "platform",
			Threshold: options.RelaySpikeMultiplier, ThresholdUnit: "multiplier",
			ThresholdDescription: "中继流量达到 24 小时均值的 3 倍", DataStatus: DataStatusInsufficientData,
		},
		{
			ID: RuleConfigDispatchFailed, Condition: "certificate.status=failed OR certificate.last_error != ''",
			Severity: SeverityCritical, TargetType: "certificate|node",
			Threshold: 0, ThresholdUnit: "failures",
			ThresholdDescription: "配置下发/证书续期失败即时触发", DataStatus: DataStatusAvailable,
		},
	}
}

func (e *Engine) evaluateRules(ctx context.Context, tenantID string, now time.Time) ([]Alert, map[string]struct{}) {
	candidates := make([]Alert, 0)
	unavailable := make(map[string]struct{})

	var nodes []nodeObservation
	if err := e.db.WithContext(ctx).Raw(
		`SELECT id, name, last_seen FROM nodes WHERE tenant_id = ?`, tenantID,
	).Scan(&nodes).Error; err != nil {
		e.logger.Error("load nodes for alert evaluation", "error", err)
	}
	candidates = append(candidates, nodeRuleAlerts(nodes, now, e.options.NodeOfflineAfter)...)

	var certificates []certificateObservation
	if err := e.db.WithContext(ctx).Raw(
		`SELECT id, domain, issuer, expires_at, status, last_error, renewal_failures FROM certificates WHERE tenant_id = ?`, tenantID,
	).Scan(&certificates).Error; err != nil {
		e.logger.Error("load certificates for alert evaluation", "error", err)
	}
	candidates = append(candidates, certificateRuleAlerts(certificates, now, e.options.CertificateExpiringIn, e.options.CertificateCriticalIn)...)
	candidates = append(candidates, configFailureAlerts(certificates, now)...)

	var dispatchFailures []dispatchFailureObservation
	if err := e.db.WithContext(ctx).Raw(
		`SELECT target_type, target_id, last_error, failures FROM config_dispatch_failures WHERE tenant_id = ?`, tenantID,
	).Scan(&dispatchFailures).Error; err != nil {
		e.logger.Error("load config dispatch failures for alert evaluation", "error", err)
	}
	candidates = append(candidates, dispatchFailureAlerts(dispatchFailures, now)...)

	unavailable[RuleP2PSuccessRateLow] = struct{}{}
	candidates = append(candidates, Alert{
		ID: "insufficient-data:" + RuleP2PSuccessRateLow, Rule: RuleP2PSuccessRateLow,
		Severity: SeverityInfo, TargetType: "platform", TargetID: "p2p-collector",
		Title: "P2P 成功率数据不足", Detail: "insufficient_data: 当前没有 P2P 打洞成功率采集数据源",
		Value: 0, Threshold: e.options.P2PSuccessRateMinimum, Since: now,
		State: StateResolved, DataStatus: DataStatusInsufficientData, TenantID: tenantID,
	})
	unavailable[RuleRelayTrafficSpike] = struct{}{}
	candidates = append(candidates, Alert{
		ID: "insufficient-data:" + RuleRelayTrafficSpike, Rule: RuleRelayTrafficSpike,
		Severity: SeverityInfo, TargetType: "platform", TargetID: "relay-collector",
		Title: "中继流量数据不足", Detail: "insufficient_data: 当前没有中继吞吐/24h 基线采集数据源",
		Value: 0, Threshold: e.options.RelaySpikeMultiplier, Since: now,
		State: StateResolved, DataStatus: DataStatusInsufficientData, TenantID: tenantID,
	})
	return candidates, unavailable
}

func uuidString(rule, target string) string {
	return rule + ":" + target
}

func nodeRuleAlerts(nodes []nodeObservation, now time.Time, threshold time.Duration) []Alert {
	result := make([]Alert, 0)
	for _, node := range nodes {
		if node.LastSeen == nil {
			continue
		}
		age := now.Sub(*node.LastSeen)
		if age <= threshold {
			continue
		}
		result = append(result, Alert{
			ID: uuidString(RuleNodeOffline, node.ID), Rule: RuleNodeOffline,
			Severity: SeverityWarning, TargetType: "node", TargetID: node.ID,
			Title: "节点离线", Detail: "节点 " + node.Name + " 已超过心跳阈值未上报",
			Value: age.Seconds(), Threshold: threshold.Seconds(),
			Since: now, State: StateFiring, DataStatus: DataStatusAvailable,
		})
	}
	return result
}

func certificateRuleAlerts(certificates []certificateObservation, now time.Time, expiringIn, criticalIn time.Duration) []Alert {
	result := make([]Alert, 0)
	for _, certificate := range certificates {
		if certificate.Status != "active" || certificate.ExpiresAt == nil {
			continue
		}
		remaining := certificate.ExpiresAt.Sub(now)
		if remaining >= expiringIn {
			continue
		}
		severity := SeverityWarning
		if remaining <= criticalIn {
			severity = SeverityCritical
		}
		result = append(result, Alert{
			ID: uuidString(RuleCertificateExpiring, certificate.ID), Rule: RuleCertificateExpiring,
			Severity: severity, TargetType: "certificate", TargetID: certificate.ID,
			Title: "证书即将过期", Detail: "证书 " + certificate.Domain + "（" + certificate.Issuer + "）剩余有效期低于阈值",
			Value: remaining.Hours() / 24, Threshold: expiringIn.Hours() / 24,
			Since: now, State: StateFiring, DataStatus: DataStatusAvailable,
		})
	}
	return result
}

func configFailureAlerts(certificates []certificateObservation, now time.Time) []Alert {
	result := make([]Alert, 0)
	for _, certificate := range certificates {
		failed := strings.EqualFold(certificate.Status, "failed") || strings.TrimSpace(certificate.LastError) != ""
		if !failed {
			continue
		}
		value := float64(certificate.RenewalFailures)
		if value < 1 {
			value = 1
		}
		severity := SeverityWarning
		if strings.EqualFold(certificate.Status, "failed") {
			severity = SeverityCritical
		}
		result = append(result, Alert{
			ID: uuidString(RuleConfigDispatchFailed, certificate.ID), Rule: RuleConfigDispatchFailed,
			Severity: severity, TargetType: "certificate", TargetID: certificate.ID,
			Title: "配置下发或续期失败", Detail: "证书 " + certificate.Domain + " 记录了签发/续期失败",
			Value: value, Threshold: 0, Since: now, State: StateFiring,
			DataStatus: DataStatusAvailable,
		})
	}
	return result
}

func dispatchFailureAlerts(failures []dispatchFailureObservation, now time.Time) []Alert {
	result := make([]Alert, 0)
	for _, failure := range failures {
		if strings.TrimSpace(failure.LastError) == "" {
			continue
		}
		value := float64(failure.Failures)
		if value < 1 {
			value = 1
		}
		result = append(result, Alert{
			ID: uuidString(RuleConfigDispatchFailed, failure.TargetID), Rule: RuleConfigDispatchFailed,
			Severity: SeverityCritical, TargetType: failure.TargetType, TargetID: failure.TargetID,
			Title: "配置下发失败", Detail: "配置下发未成功，失败记录在成功交付后清除",
			Value: value, Threshold: 0, Since: now, State: StateFiring,
			DataStatus: DataStatusAvailable,
		})
	}
	return result
}

func p2pRuleAlert(rate *float64, now time.Time, threshold float64) (Alert, bool) {
	if rate == nil {
		return Alert{
			ID: "insufficient-data:" + RuleP2PSuccessRateLow, Rule: RuleP2PSuccessRateLow,
			Severity: SeverityInfo, TargetType: "platform", TargetID: "p2p-collector",
			Title: "P2P 成功率数据不足", Detail: "insufficient_data: 当前没有 P2P 打洞成功率采集数据源",
			Value: 0, Threshold: threshold, Since: now, State: StateResolved,
			DataStatus: DataStatusInsufficientData,
		}, false
	}
	if *rate >= threshold {
		return Alert{}, false
	}
	return Alert{
		ID: uuidString(RuleP2PSuccessRateLow, "platform"), Rule: RuleP2PSuccessRateLow,
		Severity: SeverityWarning, TargetType: "platform", TargetID: "p2p-collector",
		Title: "P2P 成功率过低", Detail: "P2P 打洞成功率低于阈值",
		Value: *rate, Threshold: threshold, Since: now, State: StateFiring,
		DataStatus: DataStatusAvailable,
	}, true
}

func relayTrafficSpikeAlert(current, baseline *float64, now time.Time, multiplier float64) (Alert, bool) {
	if current == nil || baseline == nil {
		return Alert{
			ID: "insufficient-data:" + RuleRelayTrafficSpike, Rule: RuleRelayTrafficSpike,
			Severity: SeverityInfo, TargetType: "platform", TargetID: "relay-collector",
			Title: "中继流量数据不足", Detail: "insufficient_data: 当前没有中继吞吐/24h 基线采集数据源",
			Value: 0, Threshold: multiplier, Since: now, State: StateResolved,
			DataStatus: DataStatusInsufficientData,
		}, false
	}
	threshold := *baseline * multiplier
	if *current < threshold {
		return Alert{}, false
	}
	return Alert{
		ID: uuidString(RuleRelayTrafficSpike, "platform"), Rule: RuleRelayTrafficSpike,
		Severity: SeverityWarning, TargetType: "platform", TargetID: "relay-collector",
		Title: "中继流量异常增长", Detail: "中继流量达到 24 小时基线的异常增长阈值",
		Value: *current, Threshold: threshold, Since: now, State: StateFiring,
		DataStatus: DataStatusAvailable,
	}, true
}

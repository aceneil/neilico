package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

type Metrics struct {
	db                   *gorm.DB
	registry             *prometheus.Registry
	httpRequests         *prometheus.CounterVec
	nodesOnline          prometheus.GaugeFunc
	proxyRequests        *prometheus.CounterVec
	proxyRequestsGauge   prometheus.Gauge
	p2pSuccessRate       prometheus.Gauge
	relayBytes           prometheus.Gauge
	heartbeatLatency     prometheus.Gauge
	alertsFiring         *prometheus.GaugeVec
	proxyProviderUp      *prometheus.GaugeVec
	tunnelUp             *prometheus.GaugeVec
	configVersion        *prometheus.GaugeVec
	aclDenied            prometheus.Counter
	acmeOrders           *prometheus.CounterVec
	acmeOrderDuration    prometheus.Histogram
	certificateExpiry    *prometheus.GaugeVec
	certificateRenewals  *prometheus.CounterVec
	tlsHandshakes        *prometheus.CounterVec
	pkiCAExpiry          prometheus.Gauge
	pkiIssued            *prometheus.CounterVec
	pkiCertificateExpiry *prometheus.GaugeVec
}

func New(db *gorm.DB) *Metrics {
	registry := prometheus.NewRegistry()
	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_http_requests_total",
		Help: "Total HTTP requests handled by the NEILICO control plane.",
	}, []string{"method", "path", "status"})
	nodesOnline := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "neilico_nodes_online",
		Help: "Current number of online NEILICO nodes.",
	}, func() float64 {
		var count int64
		if err := db.Model(&models.Node{}).Where("status = ?", "online").Count(&count).Error; err != nil {
			return 0
		}
		return float64(count)
	})
	proxyRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_proxy_requests_total",
		Help: "Total requests handled by the NEILICO proxy plane.",
	}, []string{"domain", "status"})
	proxyRequestsGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "neilico_proxy_requests",
		Help: "Requests handled by the NEILICO proxy plane (labelled breakdown is neilico_proxy_requests_total).",
	})
	p2pSuccessRate := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "neilico_p2p_success_rate",
		Help: "P2P hole-punch success rate from 0 to 1; no collector is connected in V1-R2.",
	})
	relayBytes := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "neilico_relay_bytes",
		Help: "Relay traffic bytes; no relay throughput collector is connected in V1-R2.",
	})
	heartbeatLatency := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "neilico_agent_heartbeat_latency",
		Help: "Agent heartbeat request latency; no heartbeat latency samples are collected in V1-R2.",
	})
	alertsFiring := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_alerts_firing",
		Help: "Current firing NEILICO alerts by severity and rule.",
	}, []string{"severity", "rule"})
	proxyProviderUp := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_proxy_provider_up",
		Help: "Whether a NEILICO proxy provider is ready (1) or unavailable (0).",
	}, []string{"kind"})
	tunnelUp := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_tunnel_up",
		Help: "Whether a NEILICO mesh tunnel is up (1) or down (0).",
	}, []string{"network_id", "node_id"})
	configVersion := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_config_version",
		Help: "Latest NEILICO configuration version for a target.",
	}, []string{"target_type", "target_id"})
	aclDenied := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "neilico_acl_denied_total",
		Help: "Total NEILICO traffic decisions denied by ACL policy.",
	})
	acmeOrders := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_acme_orders_total",
		Help: "Total ACME certificate orders by result.",
	}, []string{"result"})
	acmeOrderDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "neilico_acme_order_duration_seconds",
		Help:    "Duration of ACME certificate order attempts in seconds.",
		Buckets: prometheus.DefBuckets,
	})
	certificateExpiry := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_certificate_expiry_days",
		Help: "Days until each managed domain certificate expires.",
	}, []string{"domain"})
	certificateRenewals := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_certificate_renewals_total",
		Help: "Total automatic or manual certificate renewals by result.",
	}, []string{"result"})
	tlsHandshakes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_tls_handshakes_total",
		Help: "Total TLS handshakes by result and listener.",
	}, []string{"result", "listener"})
	pkiCAExpiry := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "neilico_pki_ca_not_after_timestamp",
		Help: "Unix timestamp when the active NEILICO PKI CA expires.",
	})
	pkiIssued := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "neilico_pki_certificates_issued_total",
		Help: "Total PKI certificates issued by kind.",
	}, []string{"kind"})
	pkiCertificateExpiry := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "neilico_pki_certificate_expiry_days",
		Help: "Days until the latest PKI certificate of each kind expires.",
	}, []string{"kind"})
	pkiIssued.WithLabelValues("ca").Add(0)
	pkiIssued.WithLabelValues("server").Add(0)
	pkiIssued.WithLabelValues("node").Add(0)
	for _, severity := range []string{"critical", "warning", "info"} {
		for _, rule := range []string{"node_offline", "certificate_expiring", "p2p_success_rate_low", "relay_traffic_spike", "config_dispatch_failed"} {
			alertsFiring.WithLabelValues(severity, rule).Set(0)
		}
	}
	proxyProviderUp.WithLabelValues("builtin").Set(0)
	proxyProviderUp.WithLabelValues("nps").Set(0)
	registry.MustRegister(
		httpRequests, nodesOnline, proxyRequests, proxyRequestsGauge,
		p2pSuccessRate, relayBytes, heartbeatLatency, alertsFiring,
		proxyProviderUp, tunnelUp, configVersion, aclDenied, acmeOrders,
		acmeOrderDuration, certificateExpiry, certificateRenewals, tlsHandshakes,
		pkiCAExpiry, pkiIssued, pkiCertificateExpiry,
	)
	return &Metrics{
		db:                   db,
		registry:             registry,
		httpRequests:         httpRequests,
		nodesOnline:          nodesOnline,
		proxyRequests:        proxyRequests,
		proxyRequestsGauge:   proxyRequestsGauge,
		p2pSuccessRate:       p2pSuccessRate,
		relayBytes:           relayBytes,
		heartbeatLatency:     heartbeatLatency,
		alertsFiring:         alertsFiring,
		proxyProviderUp:      proxyProviderUp,
		tunnelUp:             tunnelUp,
		configVersion:        configVersion,
		aclDenied:            aclDenied,
		acmeOrders:           acmeOrders,
		acmeOrderDuration:    acmeOrderDuration,
		certificateExpiry:    certificateExpiry,
		certificateRenewals:  certificateRenewals,
		tlsHandshakes:        tlsHandshakes,
		pkiCAExpiry:          pkiCAExpiry,
		pkiIssued:            pkiIssued,
		pkiCertificateExpiry: pkiCertificateExpiry,
	}
}

func (m *Metrics) SetTunnelUp(networkID, nodeID string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	m.tunnelUp.WithLabelValues(networkID, nodeID).Set(value)
}

func (m *Metrics) SetConfigVersion(targetType, targetID string, version int) {
	m.configVersion.WithLabelValues(targetType, targetID).Set(float64(version))
}

func (m *Metrics) IncACLDenied() {
	m.aclDenied.Inc()
}

func (m *Metrics) Handler() http.Handler {
	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		m.refreshDatabaseMetrics(ctx)
		handler.ServeHTTP(w, r)
	})
}

func (m *Metrics) refreshDatabaseMetrics(ctx context.Context) {
	type tunnelRow struct {
		NetworkID string
		NodeID    string
		Status    string
	}
	tunnels := make([]tunnelRow, 0)
	if m.db != nil {
		if err := m.db.WithContext(ctx).Raw(`
			SELECT network_members.network_id, nodes.id, nodes.status
			FROM network_members
			JOIN nodes ON nodes.id = network_members.node_id
			ORDER BY network_members.network_id, nodes.id
		`).Scan(&tunnels).Error; err == nil {
			m.tunnelUp.Reset()
			if len(tunnels) == 0 {
				m.SetTunnelUp("_none", "_none", false)
			}
			for _, tunnel := range tunnels {
				m.SetTunnelUp(tunnel.NetworkID, tunnel.NodeID, tunnel.Status == "online")
			}
		}
		type versionRow struct {
			TargetType string
			TargetID   string
			Version    int
		}
		versions := make([]versionRow, 0)
		if err := m.db.WithContext(ctx).Raw(`
			SELECT target_type, target_id, MAX(version) AS version
			FROM config_versions
			GROUP BY target_type, target_id
			ORDER BY target_type, target_id
		`).Scan(&versions).Error; err == nil {
			m.configVersion.Reset()
			if len(versions) == 0 {
				m.SetConfigVersion("_none", "_none", 0)
			}
			for _, version := range versions {
				m.SetConfigVersion(version.TargetType, version.TargetID, version.Version)
			}
		}
	}
}

func (m *Metrics) ObserveHTTP(method, path string, status int) {
	m.httpRequests.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
}

func (m *Metrics) ObserveProxyRequest(domain, status string) {
	m.proxyRequests.WithLabelValues(domain, status).Inc()
	m.proxyRequestsGauge.Inc()
}

// SetAlertsFiring replaces the alert gauge snapshot. All known rule/severity
// combinations remain present at zero so /metrics is stable and observable.
func (m *Metrics) SetAlertsFiring(counts map[string]int64) {
	m.alertsFiring.Reset()
	for _, severity := range []string{"critical", "warning", "info"} {
		for _, rule := range []string{"node_offline", "certificate_expiring", "p2p_success_rate_low", "relay_traffic_spike", "config_dispatch_failed"} {
			m.alertsFiring.WithLabelValues(severity, rule).Set(float64(counts[rule+"|"+severity]))
		}
	}
}

func (m *Metrics) SetProxyProviderUp(kind string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	m.proxyProviderUp.WithLabelValues(kind).Set(value)
}

func (m *Metrics) ObserveACMEOrder(result string, duration time.Duration) {
	m.acmeOrders.WithLabelValues(result).Inc()
	m.acmeOrderDuration.Observe(duration.Seconds())
}

func (m *Metrics) SetCertificateExpiry(domain string, days float64) {
	m.certificateExpiry.WithLabelValues(domain).Set(days)
}

func (m *Metrics) ObserveCertificateRenewal(result string) {
	m.certificateRenewals.WithLabelValues(result).Inc()
}

func (m *Metrics) ObserveTLSHandshake(result, listener string) {
	m.tlsHandshakes.WithLabelValues(result, listener).Inc()
}

func (m *Metrics) IncPKICertificatesIssued(kind string) {
	m.pkiIssued.WithLabelValues(kind).Inc()
}

func (m *Metrics) SetPKICAExpiry(timestamp float64) {
	m.pkiCAExpiry.Set(timestamp)
}

func (m *Metrics) SetPKICertificateExpiry(kind string, days float64) {
	m.pkiCertificateExpiry.WithLabelValues(kind).Set(days)
}

package metrics

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry       *prometheus.Registry
	heartbeat      *prometheus.CounterVec
	configPull     *prometheus.CounterVec
	apply          *prometheus.CounterVec
	configVersion  prometheus.Gauge
	applyDryRun    prometheus.Gauge
	peers          prometheus.Gauge
	proxyPending   prometheus.Gauge
	trafficReports prometheus.CounterVec
	trafficBytes   *prometheus.CounterVec
	dryRun         atomic.Bool
}

func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		heartbeat: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "umpp_agent_heartbeat_total", Help: "Agent heartbeat attempts by result.",
		}, []string{"result"}),
		configPull: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "umpp_agent_config_pull_total", Help: "Agent configuration pulls by result.",
		}, []string{"result"}),
		apply: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "umpp_agent_apply_total", Help: "Agent configuration applications by result.",
		}, []string{"result"}),
		configVersion: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "umpp_agent_config_version", Help: "Last applied control-plane configuration version.",
		}),
		applyDryRun: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "umpp_agent_apply_dry_run", Help: "Whether network application is in dry-run mode.",
		}),
		peers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "umpp_agent_peers", Help: "Number of configured WireGuard peers.",
		}),
		proxyPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "umpp_agent_proxy_pending", Help: "Whether an NPS proxy client is pending integration.",
		}),
		trafficReports: *prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "umpp_agent_traffic_report_total", Help: "Traffic report batches by result.",
		}, []string{"result"}),
		trafficBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "umpp_agent_traffic_bytes_total", Help: "Reported interface traffic bytes.",
		}, []string{"direction"}),
	}
	m.registry.MustRegister(m.heartbeat, m.configPull, m.apply, m.configVersion, m.applyDryRun, m.peers, m.proxyPending, &m.trafficReports, m.trafficBytes)
	m.heartbeat.WithLabelValues("success")
	m.heartbeat.WithLabelValues("failure")
	m.configPull.WithLabelValues("success")
	m.configPull.WithLabelValues("not_modified")
	m.configPull.WithLabelValues("failure")
	m.apply.WithLabelValues("success")
	m.apply.WithLabelValues("unchanged")
	m.apply.WithLabelValues("failure")
	m.trafficReports.WithLabelValues("success")
	m.trafficReports.WithLabelValues("failure")
	m.trafficBytes.WithLabelValues("in")
	m.trafficBytes.WithLabelValues("out")
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) Heartbeat(result string)      { m.heartbeat.WithLabelValues(result).Inc() }
func (m *Metrics) ConfigPull(result string)     { m.configPull.WithLabelValues(result).Inc() }
func (m *Metrics) Apply(result string)          { m.apply.WithLabelValues(result).Inc() }
func (m *Metrics) SetConfigVersion(version int) { m.configVersion.Set(float64(version)) }
func (m *Metrics) SetPeers(count int)           { m.peers.Set(float64(count)) }
func (m *Metrics) SetProxyPending(pending bool) {
	if pending {
		m.proxyPending.Set(1)
		return
	}
	m.proxyPending.Set(0)
}
func (m *Metrics) TrafficReport(result string) { m.trafficReports.WithLabelValues(result).Inc() }
func (m *Metrics) AddTraffic(direction string, bytes int64) {
	m.trafficBytes.WithLabelValues(direction).Add(float64(bytes))
}
func (m *Metrics) SetDryRun(enabled bool) {
	m.dryRun.Store(enabled)
	if enabled {
		m.applyDryRun.Set(1)
	} else {
		m.applyDryRun.Set(0)
	}
}
func (m *Metrics) DryRun() bool { return m.dryRun.Load() }

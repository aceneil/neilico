package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
)

type Metrics struct {
	registry        *prometheus.Registry
	httpRequests    *prometheus.CounterVec
	nodesOnline     prometheus.GaugeFunc
	proxyRequests   *prometheus.CounterVec
	proxyProviderUp *prometheus.GaugeVec
}

func New(db *gorm.DB) *Metrics {
	registry := prometheus.NewRegistry()
	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "umpp_http_requests_total",
		Help: "Total HTTP requests handled by the UMPP control plane.",
	}, []string{"method", "path", "status"})
	nodesOnline := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "umpp_nodes_online",
		Help: "Current number of online UMPP nodes.",
	}, func() float64 {
		var count int64
		if err := db.Model(&models.Node{}).Where("status = ?", "online").Count(&count).Error; err != nil {
			return 0
		}
		return float64(count)
	})
	proxyRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "umpp_proxy_requests_total",
		Help: "Total requests handled by the UMPP proxy plane.",
	}, []string{"domain", "status"})
	proxyProviderUp := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "umpp_proxy_provider_up",
		Help: "Whether a UMPP proxy provider is ready (1) or unavailable (0).",
	}, []string{"kind"})
	proxyProviderUp.WithLabelValues("builtin").Set(0)
	proxyProviderUp.WithLabelValues("nps").Set(0)
	registry.MustRegister(httpRequests, nodesOnline, proxyRequests, proxyProviderUp)
	return &Metrics{
		registry:        registry,
		httpRequests:    httpRequests,
		nodesOnline:     nodesOnline,
		proxyRequests:   proxyRequests,
		proxyProviderUp: proxyProviderUp,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveHTTP(method, path string, status int) {
	m.httpRequests.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
}

func (m *Metrics) ObserveProxyRequest(domain, status string) {
	m.proxyRequests.WithLabelValues(domain, status).Inc()
}

func (m *Metrics) SetProxyProviderUp(kind string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	m.proxyProviderUp.WithLabelValues(kind).Set(value)
}

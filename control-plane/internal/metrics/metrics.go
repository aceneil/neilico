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
	nodesOnlineDesc *prometheus.Desc
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
	registry.MustRegister(httpRequests, nodesOnline)
	return &Metrics{
		registry:     registry,
		httpRequests: httpRequests,
		nodesOnline:  nodesOnline,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveHTTP(method, path string, status int) {
	m.httpRequests.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
}

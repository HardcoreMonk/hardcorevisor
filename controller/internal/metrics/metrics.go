// Package metrics — Prometheus metrics for HardCoreVisor Controller
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// VMsTotal tracks VMs by state and backend.
	VMsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hcv_vms_total",
		Help: "Total number of VMs by state and backend.",
	}, []string{"state", "backend"})

	// APIRequestsTotal counts API requests by method, path, and status.
	APIRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hcv_api_requests_total",
		Help: "Total number of API requests.",
	}, []string{"method", "path", "status"})

	// APIRequestDuration tracks API request latency.
	APIRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hcv_api_request_duration_seconds",
		Help:    "API request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// NodesTotal tracks the number of cluster nodes.
	NodesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hcv_nodes_total",
		Help: "Total number of cluster nodes.",
	})

	// StoragePoolBytes tracks storage pool capacity by pool and type.
	StoragePoolBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hcv_storage_pool_bytes",
		Help: "Storage pool bytes by pool name and type (total/used).",
	}, []string{"pool", "type"})
)

// Register registers all custom metrics with the default Prometheus registry.
func Register() {
	prometheus.MustRegister(VMsTotal)
	prometheus.MustRegister(APIRequestsTotal)
	prometheus.MustRegister(APIRequestDuration)
	prometheus.MustRegister(NodesTotal)
	prometheus.MustRegister(StoragePoolBytes)
}

// responseWriterInterceptor captures the HTTP status code.
type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterInterceptor) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// InstrumentHandler returns middleware that records request metrics.
func InstrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wi := &responseWriterInterceptor{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wi, r)
		duration := time.Since(start).Seconds()

		path := r.URL.Path
		method := r.Method
		status := strconv.Itoa(wi.statusCode)

		APIRequestsTotal.WithLabelValues(method, path, status).Inc()
		APIRequestDuration.WithLabelValues(method, path).Observe(duration)
	})
}

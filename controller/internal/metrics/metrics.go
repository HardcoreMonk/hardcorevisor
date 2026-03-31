// Package metrics — HardCoreVisor Controller의 Prometheus 메트릭
//
// /metrics 엔드포인트에서 Prometheus 형식으로 메트릭을 노출한다.
//
// 노출 메트릭:
//   - hcv_vms_total: VM 수 (state, backend 레이블별 게이지)
//   - hcv_api_requests_total: API 요청 수 (method, path, status 레이블별 카운터)
//   - hcv_api_request_duration_seconds: API 요청 소요 시간 (히스토그램)
//   - hcv_nodes_total: 클러스터 노드 수 (게이지)
//   - hcv_storage_pool_bytes: 스토리지 풀 용량 (pool, type 레이블별 게이지)
//
// Grafana 대시보드: deploy/grafana/dashboards/hardcorevisor.json
// Prometheus 설정: deploy/prometheus.yml
// 알람 규칙: deploy/alert-rules.yml
package metrics

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// pathNormalizers — URL 경로의 동적 파라미터를 정규화하여
// 메트릭 카디널리티 폭발을 방지한다.
// /api/v1/vms/123 → /api/v1/vms/{id}
var pathNormalizers = []*regexp.Regexp{
	regexp.MustCompile(`/api/v1/vms/\d+`),
	regexp.MustCompile(`/api/v1/devices/[^/]+`),
	regexp.MustCompile(`/api/v1/backups/[^/]+`),
	regexp.MustCompile(`/api/v1/snapshots/[^/]+`),
	regexp.MustCompile(`/api/v1/templates/[^/]+`),
	regexp.MustCompile(`/api/v1/images/[^/]+`),
	regexp.MustCompile(`/api/v1/tasks/[^/]+`),
	regexp.MustCompile(`/api/v1/storage/volumes/[^/]+`),
	regexp.MustCompile(`/api/v1/cluster/fence/[^/]+`),
	regexp.MustCompile(`/api/v1/auth/users/[^/]+`),
	regexp.MustCompile(`/api/v1/nodes/[^/]+`),
	regexp.MustCompile(`/api/v1/network/zones/[^/]+`),
}

var pathReplacements = []string{
	"/api/v1/vms/{id}",
	"/api/v1/devices/{id}",
	"/api/v1/backups/{id}",
	"/api/v1/snapshots/{id}",
	"/api/v1/templates/{id}",
	"/api/v1/images/{id}",
	"/api/v1/tasks/{id}",
	"/api/v1/storage/volumes/{id}",
	"/api/v1/cluster/fence/{node}",
	"/api/v1/auth/users/{username}",
	"/api/v1/nodes/{id}",
	"/api/v1/network/zones/{name}",
}

// normalizePath — 동적 경로 파라미터를 플레이스홀더로 치환한다.
func normalizePath(path string) string {
	for i, re := range pathNormalizers {
		if re.MatchString(path) {
			return pathReplacements[i]
		}
	}
	return path
}

var (
	// VMsTotal 은 상태(state)와 백엔드(backend)별 VM 수를 추적한다.
	// 레이블: state (created/running/paused/stopped), backend (rustvmm/qemu)
	VMsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hcv_vms_total",
		Help: "Total number of VMs by state and backend.",
	}, []string{"state", "backend"})

	// APIRequestsTotal 은 메서드(method), 경로(path), 상태(status)별 API 요청 수를 센다.
	APIRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hcv_api_requests_total",
		Help: "Total number of API requests.",
	}, []string{"method", "path", "status"})

	// APIRequestDuration 은 API 요청 소요 시간을 초 단위로 추적한다.
	// 기본 버킷: 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10
	APIRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hcv_api_request_duration_seconds",
		Help:    "API request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// NodesTotal 은 클러스터 노드 수를 추적한다.
	NodesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hcv_nodes_total",
		Help: "Total number of cluster nodes.",
	})

	// StoragePoolBytes 는 풀 이름(pool)과 종류(type: total/used)별 스토리지 용량을 추적한다.
	StoragePoolBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hcv_storage_pool_bytes",
		Help: "Storage pool bytes by pool name and type (total/used).",
	}, []string{"pool", "type"})
)

// Register 는 모든 커스텀 메트릭을 기본 Prometheus 레지스트리에 등록한다.
//
// 호출 시점: Controller 시작 시 1회 호출
// 주의: 2회 이상 호출 시 패닉 발생 (MustRegister 사용)
func Register() {
	prometheus.MustRegister(VMsTotal)
	prometheus.MustRegister(APIRequestsTotal)
	prometheus.MustRegister(APIRequestDuration)
	prometheus.MustRegister(NodesTotal)
	prometheus.MustRegister(StoragePoolBytes)
}

// responseWriterInterceptor 는 HTTP 상태 코드를 캡처하기 위해 ResponseWriter를 래핑한다.
type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterInterceptor) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// InstrumentHandler 는 요청 메트릭(요청 수, 소요 시간)을 기록하는 미들웨어를 반환한다.
//
// 기록 메트릭:
//   - hcv_api_requests_total: 요청 수 (method, path, status)
//   - hcv_api_request_duration_seconds: 소요 시간 (method, path)
func InstrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wi := &responseWriterInterceptor{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wi, r)
		duration := time.Since(start).Seconds()

		path := normalizePath(r.URL.Path)
		method := r.Method
		status := strconv.Itoa(wi.statusCode)

		APIRequestsTotal.WithLabelValues(method, path, status).Inc()
		APIRequestDuration.WithLabelValues(method, path).Observe(duration)
	})
}

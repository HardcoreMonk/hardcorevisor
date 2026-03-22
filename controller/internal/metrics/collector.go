// 서비스 상태 수집기 — 내부 서비스에서 Prometheus 게이지 메트릭 갱신
//
// 주기적으로 호출하여 VM 수, 노드 수, 스토리지 사용량 등의
// 게이지 메트릭을 최신 상태로 유지한다.
package metrics

import (
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// ServiceRefs 는 메트릭 수집을 위한 서비스 참조를 보관한다.
// api 패키지를 임포트하지 않아 순환 의존성을 방지한다.
type ServiceRefs struct {
	Compute compute.ComputeProvider
	Storage *storage.Service
	HA      *ha.Service
}

// CollectFromServices 는 서비스 상태에서 게이지 메트릭을 갱신한다.
//
// 수집 항목:
//   - VM 수: 상태(state)와 백엔드(backend)별 집계
//   - 노드 수: HA 서비스에서 조회
//   - 스토리지 풀: 풀별 전체/사용 바이트 수
//
// 호출 시점: /metrics 요청 시 또는 주기적 갱신 타이머에서 호출
// svc가 nil이면 아무 작업도 수행하지 않는다.
func CollectFromServices(svc *ServiceRefs) {
	if svc == nil {
		return
	}

	// Collect VM counts by state and backend
	VMsTotal.Reset()
	if svc.Compute != nil {
		vms := svc.Compute.ListVMs()
		counts := make(map[[2]string]float64)
		for _, vm := range vms {
			key := [2]string{vm.State, vm.Backend}
			counts[key]++
		}
		for key, count := range counts {
			VMsTotal.WithLabelValues(key[0], key[1]).Set(count)
		}
	}

	// Collect node count
	if svc.HA != nil {
		nodes := svc.HA.ListNodes()
		NodesTotal.Set(float64(len(nodes)))
	}

	// Collect storage pool metrics
	StoragePoolBytes.Reset()
	if svc.Storage != nil {
		pools := svc.Storage.ListPools()
		for _, pool := range pools {
			StoragePoolBytes.WithLabelValues(pool.Name, "total").Set(float64(pool.TotalBytes))
			StoragePoolBytes.WithLabelValues(pool.Name, "used").Set(float64(pool.UsedBytes))
		}
	}
}

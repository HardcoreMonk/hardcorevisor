// 자동 장애 복구 — 노드 장애 시 VM 자동 재시작/마이그레이션
//
// FailoverManager는 리더 노드에서만 동작하며, 장애 발생 시
// 분산 잠금을 획득한 후 VM을 정상 노드로 마이그레이션한다.
//
// RestartPolicy에 따라 처리:
//   - "always": 항상 재시작/마이그레이션
//   - "on-failure": 장애 시에만 재시작/마이그레이션
//   - "never": 재시작하지 않음
package ha

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ComputeFailoverProvider 는 장애 복구 시 VM 정보 조회와 마이그레이션을
// 수행하기 위한 최소 인터페이스이다.
// import cycle을 방지하기 위해 compute 패키지를 직접 참조하지 않는다.
type ComputeFailoverProvider interface {
	// ListVMs 는 모든 VM의 요약 정보를 반환한다.
	ListVMs() []VMSummary
	// MigrateVM 는 VM을 다른 노드로 마이그레이션한다.
	MigrateVM(handle int32, targetNode string) error
}

// VMSummary 는 장애 복구에 필요한 최소 VM 정보이다.
type VMSummary struct {
	Handle        int32  `json:"handle"`
	Name          string `json:"name"`
	Node          string `json:"node"`
	State         string `json:"state"`
	RestartPolicy string `json:"restart_policy"`
}

// FailoverManager 는 노드 장애 시 VM 자동 복구를 관리한다.
// 리더 노드에서만 동작하며, 분산 잠금을 통해 동시 복구를 방지한다.
type FailoverManager struct {
	mu              sync.Mutex
	leaderElection  *LeaderElection
	lockManager     *LockManager
	computeProvider ComputeFailoverProvider
	driver          HADriver
}

// NewFailoverManager 는 장애 복구 관리자를 생성한다.
func NewFailoverManager(leader *LeaderElection, locks *LockManager) *FailoverManager {
	return &FailoverManager{
		leaderElection: leader,
		lockManager:    locks,
	}
}

// SetComputeProvider 는 ComputeFailoverProvider를 설정한다.
// 순환 참조를 피하기 위해 생성 후 별도로 설정한다.
func (fm *FailoverManager) SetComputeProvider(provider ComputeFailoverProvider) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.computeProvider = provider
}

// SetDriver 는 HADriver를 설정한다.
func (fm *FailoverManager) SetDriver(driver HADriver) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.driver = driver
}

// HandleNodeDown 은 노드 장애 시 호출되어 VM 복구를 수행한다.
// 리더가 아니면 건너뛴다.
// 장애 노드에서 실행 중이던 VM 중 RestartPolicy가 "always" 또는 "on-failure"인
// VM을 정상 노드로 마이그레이션한다.
func (fm *FailoverManager) HandleNodeDown(failedNode string) {
	if fm.leaderElection != nil && !fm.leaderElection.IsLeader() {
		slog.Debug("not leader, skipping failover handling", "failed_node", failedNode)
		return
	}

	fm.mu.Lock()
	provider := fm.computeProvider
	fm.mu.Unlock()

	if provider == nil {
		slog.Warn("no compute provider for failover", "failed_node", failedNode)
		return
	}

	slog.Info("handling node failure", "failed_node", failedNode)

	vms := provider.ListVMs()
	affectedVMs := make([]VMSummary, 0)
	for _, vm := range vms {
		if vm.Node == failedNode {
			affectedVMs = append(affectedVMs, vm)
		}
	}

	if len(affectedVMs) == 0 {
		slog.Info("no VMs affected by node failure", "failed_node", failedNode)
		return
	}

	slog.Info("VMs affected by node failure",
		"failed_node", failedNode,
		"count", len(affectedVMs))

	targetNode := fm.selectTargetNode(failedNode)
	if targetNode == "" {
		slog.Error("no healthy node available for failover", "failed_node", failedNode)
		return
	}

	for _, vm := range affectedVMs {
		policy := vm.RestartPolicy
		if policy == "" {
			policy = "always" // default policy
		}

		if policy == "never" {
			slog.Info("skipping VM with restart_policy=never",
				"vm", vm.Name, "handle", vm.Handle)
			continue
		}

		// Acquire distributed lock for this VM migration
		lockKey := "failover-vm-" + vm.Name
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		err := fm.lockManager.WithLock(ctx, lockKey, 30*time.Second, func() error {
			slog.Info("migrating VM due to node failure",
				"vm", vm.Name,
				"handle", vm.Handle,
				"from", failedNode,
				"to", targetNode,
				"restart_policy", policy)
			return provider.MigrateVM(vm.Handle, targetNode)
		})
		cancel()

		if err != nil {
			slog.Error("failover migration failed",
				"vm", vm.Name,
				"handle", vm.Handle,
				"error", err)
		} else {
			slog.Info("failover migration completed",
				"vm", vm.Name,
				"handle", vm.Handle,
				"target", targetNode)
		}
	}
}

// selectTargetNode 는 장애 노드를 제외한 정상 노드를 선택한다.
func (fm *FailoverManager) selectTargetNode(exclude string) string {
	fm.mu.Lock()
	driver := fm.driver
	fm.mu.Unlock()

	if driver == nil {
		return "local"
	}

	nodes, err := driver.ListNodes()
	if err != nil {
		slog.Warn("failed to list nodes for target selection", "error", err)
		return "local"
	}

	for _, node := range nodes {
		if node.Name != exclude && node.Status == NodeOnline {
			return node.Name
		}
	}
	return ""
}

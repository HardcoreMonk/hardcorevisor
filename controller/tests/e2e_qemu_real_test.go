// QEMU Real 모드 E2E 테스트 — 실제 qemu-system-x86_64 바이너리 필요
//
// 빌드 태그: qemu_real — 기본 테스트에서 제외됨.
// 실행: go test -tags qemu_real -run TestE2E_QEMURealMode ./tests/
//
//go:build qemu_real

package tests

import (
	"os/exec"
	"testing"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
)

// TestE2E_QEMURealMode — 실제 QEMU 바이너리로 VM 생성 및 QMP 연결을 검증한다.
func TestE2E_QEMURealMode(t *testing.T) {
	// qemu-system-x86_64가 PATH에 있는지 확인
	qemuPath, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		t.Skip("qemu-system-x86_64 not found in PATH, skipping real QEMU test")
	}
	t.Logf("QEMU binary: %s", qemuPath)

	// Real 모드 QEMU 백엔드 생성 (소켓 디렉터리: /tmp/hcv-test)
	backend := compute.NewQEMUBackend(compute.QEMURealMode, "/tmp/hcv-test")

	// VM 생성
	handle, err := backend.CreateVM("qemu-real-test", 1, 256)
	if err != nil {
		t.Fatalf("CreateVM failed: %v", err)
	}
	t.Logf("VM created: handle=%d", handle)

	// VM 시작 (QEMU 프로세스 + QMP 연결)
	err = backend.StartVM(handle)
	if err != nil {
		// 정리 후 실패
		_ = backend.DestroyVM(handle)
		t.Fatalf("StartVM failed: %v", err)
	}
	t.Log("VM started successfully via QMP")

	// QMP 상태 확인
	state, err := backend.GetVMState(handle)
	if err != nil {
		t.Errorf("GetVMState failed: %v", err)
	}
	t.Logf("VM state: %s", state)

	// VM 일시정지 → 재개
	if err := backend.PauseVM(handle); err != nil {
		t.Errorf("PauseVM failed: %v", err)
	}
	if err := backend.ResumeVM(handle); err != nil {
		t.Errorf("ResumeVM failed: %v", err)
	}

	// 정리: 중지 → 삭제
	if err := backend.StopVM(handle); err != nil {
		t.Errorf("StopVM failed: %v", err)
	}
	if err := backend.DestroyVM(handle); err != nil {
		t.Errorf("DestroyVM failed: %v", err)
	}
	t.Log("VM destroyed, QEMU Real mode E2E passed")
}

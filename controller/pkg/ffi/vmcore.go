// Package ffi — CGo 바인딩: Rust vmcore staticlib (libvmcore.a) 연동
//
// libvmcore.a는 `cargo build -p vmcore --release`로 빌드한다.
// 모든 FFI 함수는 i32를 반환: 양수/0 = 성공, 음수 = ErrorCode.
//
// 빌드 태그: cgo_vmcore 설정 시에만 컴파일됨.
// 사용법: go build -tags cgo_vmcore ./pkg/ffi/...
//
//go:build cgo_vmcore

package ffi

/*
#cgo LDFLAGS: -L${SRCDIR}/../../../vmcore/target/release -lvmcore -ldl -lpthread -lm
#include <stdint.h>

// ── lib.rs FFI (3개) ──
extern int32_t hcv_init(void);
extern const char* hcv_version(void);
extern int32_t hcv_shutdown(void);

// ── kvm_mgr.rs FFI (9개) ──
extern int32_t hcv_vm_create(void);
extern int32_t hcv_vm_destroy(int32_t handle);
extern int32_t hcv_vm_configure(int32_t handle, uint32_t vcpus, uint64_t memory_mb);
extern int32_t hcv_vm_start(int32_t handle);
extern int32_t hcv_vm_stop(int32_t handle);
extern int32_t hcv_vm_pause(int32_t handle);
extern int32_t hcv_vm_resume(int32_t handle);
extern int32_t hcv_vm_get_state(int32_t handle);
extern int32_t hcv_vm_count(void);
*/
import "C"

import "unsafe"

// checkResult — FFI 반환값 검사. 음수이면 FFIError로 변환한다.
func checkResult(code C.int32_t, op string) error {
	if code < 0 {
		return &FFIError{Code: int32(code), Op: op}
	}
	return nil
}

// CGoVMCore — 실제 CGo 바인딩을 통한 VMCoreBackend 구현체.
// libvmcore.a (Rust staticlib)에 링크하여 /dev/kvm을 직접 제어한다.
type CGoVMCore struct{}

// NewCGoVMCore — CGo VMCore 백엔드를 생성한다.
func NewCGoVMCore() *CGoVMCore {
	return &CGoVMCore{}
}

// 컴파일 타임 인터페이스 검증
var _ VMCoreBackend = (*CGoVMCore)(nil)

func (c *CGoVMCore) Init() error {
	return checkResult(C.hcv_init(), "init")
}

func (c *CGoVMCore) Shutdown() {
	C.hcv_shutdown()
}

func (c *CGoVMCore) Version() string {
	cstr := C.hcv_version()
	return C.GoString(cstr)
}

func (c *CGoVMCore) CreateVM() (int32, error) {
	result := C.hcv_vm_create()
	if result < 0 {
		return 0, &FFIError{Code: int32(result), Op: "vm_create"}
	}
	return int32(result), nil
}

func (c *CGoVMCore) DestroyVM(handle int32) error {
	return checkResult(C.hcv_vm_destroy(C.int32_t(handle)), "vm_destroy")
}

func (c *CGoVMCore) ConfigureVM(handle int32, vcpus uint32, memoryMB uint64) error {
	return checkResult(
		C.hcv_vm_configure(C.int32_t(handle), C.uint32_t(vcpus), C.uint64_t(memoryMB)),
		"vm_configure",
	)
}

func (c *CGoVMCore) StartVM(handle int32) error {
	return checkResult(C.hcv_vm_start(C.int32_t(handle)), "vm_start")
}

func (c *CGoVMCore) StopVM(handle int32) error {
	return checkResult(C.hcv_vm_stop(C.int32_t(handle)), "vm_stop")
}

func (c *CGoVMCore) PauseVM(handle int32) error {
	return checkResult(C.hcv_vm_pause(C.int32_t(handle)), "vm_pause")
}

func (c *CGoVMCore) ResumeVM(handle int32) error {
	return checkResult(C.hcv_vm_resume(C.int32_t(handle)), "vm_resume")
}

func (c *CGoVMCore) GetVMState(handle int32) (int32, error) {
	result := C.hcv_vm_get_state(C.int32_t(handle))
	if result < 0 {
		return -1, &FFIError{Code: int32(result), Op: "vm_get_state"}
	}
	return int32(result), nil
}

func (c *CGoVMCore) VMCount() int32 {
	return int32(C.hcv_vm_count())
}

// unsafe 임포트 사용 보장 (CGo 포인터 규칙)
var _ = unsafe.Pointer(nil)

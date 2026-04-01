// backend_cgo.go — CGo 빌드 시 실제 VMCoreBackend를 반환한다.
//
//go:build cgo_vmcore

package ffi

// NewBackend — CGo 빌드 태그 활성 시 실제 CGoVMCore 백엔드를 반환한다.
func NewBackend() VMCoreBackend {
	return NewCGoVMCore()
}

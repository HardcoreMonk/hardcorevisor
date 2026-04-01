// backend_mock.go — CGo 빌드 태그 미설정 시 Mock 백엔드를 반환한다.
//
//go:build !cgo_vmcore

package ffi

// NewBackend — CGo 미사용 시 Mock 백엔드를 반환한다.
func NewBackend() VMCoreBackend {
	return NewMockVMCore()
}

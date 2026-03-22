// Package ffi — 실제 CGo와 Mock 구현이 공유하는 에러 타입 정의.
//
// # 에러 코드 동기화
//
// 이 파일의 에러 코드 상수는 Rust 측 vmcore/src/panic_barrier.rs의
// ErrorCode 열거형과 반드시 동기화되어야 한다.
//
//	Rust: ErrorCode::InvalidState = -5
//	Go:   ErrInvalidState = -5
package ffi

import "fmt"

// vmcore 에러 코드 상수 (Rust panic_barrier::ErrorCode와 동기화 필수)
//
//	| 코드 | Rust (ErrorCode)  | Go 상수           | 의미               |
//	|------|-------------------|--------------------|-------------------|
//	| -1   | Panic             | ErrPanic           | FFI 패닉 포착      |
//	| -2   | InvalidArg        | ErrInvalidArg      | 잘못된 인자        |
//	| -3   | KvmError          | ErrKVM             | KVM ioctl 실패     |
//	| -4   | NotFound          | ErrNotFound        | 리소스 없음        |
//	| -5   | InvalidState      | ErrInvalidState    | 잘못된 상태 전이   |
//	| -6   | OutOfMemory       | ErrOutOfMemory     | 메모리 할당 실패   |
//	| -7   | NotSupported      | ErrNotSupported    | 미지원 작업        |
const (
	ErrPanic        = -1
	ErrInvalidArg   = -2
	ErrKVM          = -3
	ErrNotFound     = -4
	ErrInvalidState = -5
	ErrOutOfMemory  = -6
	ErrNotSupported = -7
)

// FFIError — vmcore 에러 코드를 래핑하는 에러 타입.
// Code는 에러 코드 상수(ErrPanic ~ ErrNotSupported), Op는 실패한 작업명.
// error 인터페이스를 구현하며, 형식: "vmcore {Op}: {설명} (code={Code})"
type FFIError struct {
	Code int32
	Op   string
}

func (e *FFIError) Error() string {
	var desc string
	switch e.Code {
	case ErrPanic:
		desc = "internal panic"
	case ErrInvalidArg:
		desc = "invalid argument"
	case ErrKVM:
		desc = "KVM error"
	case ErrNotFound:
		desc = "not found"
	case ErrInvalidState:
		desc = "invalid state"
	case ErrOutOfMemory:
		desc = "out of memory"
	case ErrNotSupported:
		desc = "not supported"
	default:
		desc = "unknown error"
	}
	return fmt.Sprintf("vmcore %s: %s (code=%d)", e.Op, desc, e.Code)
}

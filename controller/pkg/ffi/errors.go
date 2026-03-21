// Package ffi — Error types shared between real CGo and mock implementations
package ffi

import "fmt"

// Error codes from vmcore (must match panic_barrier::ErrorCode in Rust)
const (
	ErrPanic        = -1
	ErrInvalidArg   = -2
	ErrKVM          = -3
	ErrNotFound     = -4
	ErrInvalidState = -5
	ErrOutOfMemory  = -6
	ErrNotSupported = -7
)

// FFIError wraps a vmcore error code
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

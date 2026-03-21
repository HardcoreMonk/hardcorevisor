//! # Panic Barrier — FFI Safety Layer
//!
//! Every `extern "C"` function MUST use `catch()` or `catch_mut()`.
//! Converts Rust panics into C error codes, preventing UB across FFI.

use std::panic::{self, AssertUnwindSafe};

/// Error codes returned across the FFI boundary.
/// MUST be kept in sync with Go: `pkg/ffi/errors.go`
#[repr(i32)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCode {
    Panic = -1,
    InvalidArg = -2,
    KvmError = -3,
    NotFound = -4,
    InvalidState = -5,
    OutOfMemory = -6,
    NotSupported = -7,
    IoError = -8,
    BufferFull = -9,
    AlreadyExists = -10,
}

/// Wrap an `UnwindSafe` closure for FFI. Returns `ErrorCode::Panic` on panic.
pub fn catch<F>(f: F) -> i32
where
    F: FnOnce() -> i32 + panic::UnwindSafe,
{
    match panic::catch_unwind(f) {
        Ok(result) => result,
        Err(payload) => {
            let msg = extract_panic_msg(&payload);
            tracing::error!(message = %msg, "PANIC caught at FFI boundary");
            ErrorCode::Panic as i32
        }
    }
}

/// Wrap a closure that captures `&mut`. Caller guarantees invariant safety.
pub fn catch_mut<F>(f: F) -> i32
where
    F: FnOnce() -> i32,
{
    match panic::catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => {
            let msg = extract_panic_msg(&payload);
            tracing::error!(message = %msg, "PANIC caught at FFI boundary (mut)");
            ErrorCode::Panic as i32
        }
    }
}

/// Wrap a closure returning `*mut T` (pointer). Returns null on panic.
pub fn catch_ptr<T, F>(f: F) -> *mut T
where
    F: FnOnce() -> *mut T + panic::UnwindSafe,
{
    match panic::catch_unwind(f) {
        Ok(ptr) => ptr,
        Err(payload) => {
            let msg = extract_panic_msg(&payload);
            tracing::error!(message = %msg, "PANIC caught at FFI boundary (ptr)");
            std::ptr::null_mut()
        }
    }
}

fn extract_panic_msg(payload: &Box<dyn std::any::Any + Send>) -> String {
    if let Some(s) = payload.downcast_ref::<&str>() {
        s.to_string()
    } else if let Some(s) = payload.downcast_ref::<String>() {
        s.clone()
    } else {
        "unknown panic".to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_catch_success() {
        assert_eq!(catch(|| 42), 42);
    }

    #[test]
    fn test_catch_zero() {
        assert_eq!(catch(|| 0), 0);
    }

    #[test]
    fn test_catch_negative() {
        assert_eq!(catch(|| -5), -5);
    }

    #[test]
    fn test_catch_panic_returns_minus_one() {
        let result = catch(|| panic!("test panic"));
        assert_eq!(result, ErrorCode::Panic as i32);
    }

    #[test]
    fn test_catch_panic_string() {
        let result = catch(|| panic!("detailed error: {}", 42));
        assert_eq!(result, -1);
    }

    #[test]
    fn test_catch_mut_success() {
        let mut x = 0;
        let result = catch_mut(|| {
            x += 10;
            x
        });
        assert_eq!(result, 10);
    }

    #[test]
    fn test_catch_ptr_success() {
        let ptr = catch_ptr(|| Box::into_raw(Box::new(42i32)));
        assert!(!ptr.is_null());
        unsafe {
            drop(Box::from_raw(ptr));
        }
    }

    #[test]
    fn test_catch_ptr_panic_returns_null() {
        let ptr: *mut i32 = catch_ptr(|| panic!("ptr panic"));
        assert!(ptr.is_null());
    }

    #[test]
    fn test_error_codes_are_negative() {
        assert!((ErrorCode::Panic as i32) < 0);
        assert!((ErrorCode::AlreadyExists as i32) < 0);
        assert_eq!(ErrorCode::Panic as i32, -1);
        assert_eq!(ErrorCode::AlreadyExists as i32, -10);
    }
}

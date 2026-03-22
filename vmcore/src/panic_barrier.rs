//! # Panic Barrier — FFI 안전성 계층
//!
//! ## 목적
//! Rust 패닉이 FFI 경계를 넘어 Go 런타임으로 전파되면 정의되지 않은 동작(UB)이
//! 발생한다. 이 모듈은 모든 `extern "C"` 함수를 `catch_unwind`로 래핑하여
//! 패닉을 안전하게 C 에러 코드로 변환한다.
//!
//! ## 아키텍처 위치
//! ```text
//! Go Controller (CGo) → extern "C" → panic_barrier::catch() → Rust 비즈니스 로직
//! ```
//! - 모든 FFI 진입점은 반드시 `catch()`, `catch_mut()`, `catch_ptr()` 중 하나를 사용해야 한다.
//!
//! ## 핵심 개념
//! - `catch()`: `UnwindSafe` 클로저를 래핑하여 i32 반환
//! - `catch_mut()`: `&mut` 캡처가 필요한 클로저를 `AssertUnwindSafe`로 래핑
//! - `catch_ptr()`: 포인터 반환 함수용, 패닉 시 null 반환
//! - `ErrorCode`: Go 측 `pkg/ffi/errors.go`와 동기화되는 에러 코드 열거형
//!
//! ## 스레드 안전성
//! 모든 함수는 스레드 안전하다. `catch_unwind`는 Send + 'static이 아닌
//! 페이로드도 캡처할 수 있으며, 로깅 후 에러 코드를 반환한다.

use std::panic::{self, AssertUnwindSafe};

/// FFI 경계를 통해 반환되는 에러 코드 열거형.
///
/// Go 측 `pkg/ffi/errors.go` 상수와 반드시 동기화해야 한다.
/// 음수 값은 에러를 나타내며, 양수/0은 성공을 의미한다.
#[repr(i32)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCode {
    /// FFI 경계에서 포착된 Rust 내부 패닉
    Panic = -1,
    /// 잘못된 인자 (null 포인터, 범위 초과 등)
    InvalidArg = -2,
    /// KVM ioctl 실패
    KvmError = -3,
    /// 요청한 리소스를 찾을 수 없음
    NotFound = -4,
    /// 잘못된 상태 전이 (예: Created → Running)
    InvalidState = -5,
    /// 메모리 할당 실패 (mmap 등)
    OutOfMemory = -6,
    /// 지원되지 않는 작업
    NotSupported = -7,
    /// I/O 오류
    IoError = -8,
    /// 링 버퍼가 가득 참
    BufferFull = -9,
    /// 리소스가 이미 존재함
    AlreadyExists = -10,
}

/// `UnwindSafe` 클로저를 FFI용으로 래핑한다.
///
/// 패닉 발생 시 `ErrorCode::Panic`(-1)을 반환하고 tracing으로 에러를 기록한다.
///
/// # 매개변수
/// - `f`: 실행할 클로저. i32를 반환해야 한다.
///
/// # 반환값
/// - 정상: 클로저의 반환값 (양수/0 = 성공, 음수 = 에러)
/// - 패닉: `ErrorCode::Panic` (-1)
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

/// `&mut` 캡처가 필요한 클로저를 FFI용으로 래핑한다.
///
/// `AssertUnwindSafe`로 래핑하므로, 호출자가 패닉 후에도
/// 데이터 불변성이 유지됨을 보장해야 한다.
///
/// # 매개변수
/// - `f`: 가변 참조를 캡처하는 클로저
///
/// # 반환값
/// - 정상: 클로저의 반환값
/// - 패닉: `ErrorCode::Panic` (-1)
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

/// 포인터를 반환하는 클로저를 FFI용으로 래핑한다.
///
/// 패닉 발생 시 null 포인터를 반환한다. Go 측에서 반환값이
/// null인지 반드시 확인해야 한다.
///
/// # 매개변수
/// - `f`: `*mut T`를 반환하는 클로저
///
/// # 반환값
/// - 정상: 클로저가 반환한 포인터
/// - 패닉: `std::ptr::null_mut()`
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

/// 패닉 페이로드에서 사람이 읽을 수 있는 메시지를 추출한다.
///
/// `&str`, `String` 타입의 페이로드를 처리하며,
/// 그 외에는 "unknown panic"을 반환한다.
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

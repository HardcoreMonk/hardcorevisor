//! # KVM Manager — VM 인스턴스 레지스트리
//!
//! ## 목적
//! VM의 생성, 삭제, 상태 전이를 관리하는 인메모리 레지스트리.
//! FFI를 통해 Go Controller에 VM 생명주기 CRUD API를 제공한다.
//!
//! ## 아키텍처 위치
//! ```text
//! Go Controller (CGo) → hcv_vm_* FFI 함수들 → kvm_mgr (이 모듈)
//! ```
//! - `kvm_sys.rs`(실제 KVM ioctl)와 분리된 인메모리 상태 머신
//! - 런타임에서 VM 상태 전이를 검증하여 잘못된 전이를 방지
//!
//! ## 핵심 개념
//! - `VmState`: Created → Configured → Running ⇄ Paused, → Stopped 상태 전이
//! - `VmInstance`: 핸들, 상태, vCPU 수, 메모리 크기를 포함하는 VM 인스턴스
//! - 전역 레지스트리: `OnceLock<Mutex<HashMap>>` 기반 싱글턴 패턴
//!
//! ## 스레드 안전성
//! `Mutex`로 보호되는 전역 레지스트리를 사용하므로 모든 FFI 함수는 스레드 안전하다.
//! `NEXT_HANDLE`은 `AtomicI32`로 원자적 핸들 생성을 보장한다.

use crate::panic_barrier::ErrorCode;
use std::collections::HashMap;
use std::sync::{
    atomic::{AtomicI32, Ordering},
    Mutex, OnceLock,
};

/// VM 런타임 상태 열거형 — FFI용 `repr(C)`.
///
/// Go 호출자를 위해 Rust Typestate 패턴을 런타임 수준에서 미러링한다.
/// 허용되는 상태 전이:
/// ```text
/// Created → Configured → Running ⇄ Paused
///                      ↘ Stopped ↙
/// ```
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VmState {
    /// 초기 생성 상태
    Created = 0,
    /// vCPU/메모리 설정 완료
    Configured = 1,
    /// 실행 중
    Running = 2,
    /// 일시 중지
    Paused = 3,
    /// 종료됨
    Stopped = 4,
}

impl VmState {
    /// 현재 상태에서 `target` 상태로의 전이가 유효한지 검사한다.
    ///
    /// # 매개변수
    /// - `target`: 전이하려는 목표 상태
    ///
    /// # 반환값
    /// - `true`: 유효한 전이
    /// - `false`: 잘못된 전이 (예: Created → Running은 불허)
    pub fn can_transition_to(self, target: VmState) -> bool {
        matches!(
            (self, target),
            (VmState::Created, VmState::Configured)
                | (VmState::Configured, VmState::Running)
                | (VmState::Configured, VmState::Stopped)
                | (VmState::Running, VmState::Paused)
                | (VmState::Running, VmState::Stopped)
                | (VmState::Paused, VmState::Running)
                | (VmState::Paused, VmState::Stopped)
        )
    }
}

/// VM 관리 에러 타입
#[derive(Debug, thiserror::Error)]
pub enum VmError {
    #[error("KVM system error: {0}")]
    Kvm(String),
    #[error("VM not found: handle={0}")]
    NotFound(i32),
    #[error("invalid argument: {0}")]
    InvalidArg(String),
    #[error("invalid state transition: {current:?} -> {target:?}")]
    InvalidState { current: VmState, target: VmState },
    #[error("VM already exists: handle={0}")]
    AlreadyExists(i32),
}

impl VmError {
    /// VmError를 FFI 에러 코드(음수 i32)로 변환한다.
    pub fn to_error_code(&self) -> i32 {
        match self {
            VmError::Kvm(_) => ErrorCode::KvmError as i32,
            VmError::NotFound(_) => ErrorCode::NotFound as i32,
            VmError::InvalidArg(_) => ErrorCode::InvalidArg as i32,
            VmError::InvalidState { .. } => ErrorCode::InvalidState as i32,
            VmError::AlreadyExists(_) => ErrorCode::AlreadyExists as i32,
        }
    }
}

/// 내부 VM 인스턴스 표현.
///
/// 레지스트리에 저장되며, 핸들을 통해 FFI에서 참조된다.
#[derive(Debug)]
pub struct VmInstance {
    /// 고유 VM 핸들 (양수, AtomicI32로 생성)
    pub handle: i32,
    /// 현재 VM 상태
    pub state: VmState,
    /// 할당된 vCPU 수
    pub vcpu_count: u32,
    /// 할당된 메모리 크기 (MB)
    pub memory_mb: u64,
}

// ── 전역 VM 레지스트리 (OnceLock + Mutex 싱글턴) ────────────────
/// 다음 핸들 번호 (원자적 증가, 양수 보장)
static NEXT_HANDLE: AtomicI32 = AtomicI32::new(1);

/// 전역 VM 레지스트리의 싱글턴 참조를 반환한다.
/// `OnceLock`으로 한 번만 초기화되며, `Mutex`로 스레드 안전성을 보장한다.
fn registry() -> &'static Mutex<HashMap<i32, VmInstance>> {
    static REG: OnceLock<Mutex<HashMap<i32, VmInstance>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

/// 레지스트리 잠금을 획득하고 클로저를 실행한다.
/// 뮤텍스 중독(poisoned) 시 `VmError::Kvm`을 반환한다.
fn with_registry<F, R>(f: F) -> Result<R, VmError>
where
    F: FnOnce(&mut HashMap<i32, VmInstance>) -> Result<R, VmError>,
{
    let mut guard = registry()
        .lock()
        .map_err(|e| VmError::Kvm(format!("registry lock poisoned: {e}")))?;
    f(&mut guard)
}

/// 특정 핸들의 VM 인스턴스에 대해 클로저를 실행한다.
/// VM이 존재하지 않으면 `VmError::NotFound`를 반환한다.
fn with_vm<F, R>(handle: i32, f: F) -> Result<R, VmError>
where
    F: FnOnce(&mut VmInstance) -> Result<R, VmError>,
{
    with_registry(|reg| {
        let vm = reg.get_mut(&handle).ok_or(VmError::NotFound(handle))?;
        f(vm)
    })
}

// ── Public API ───────────────────────────────────────────

/// 새 VM 인스턴스를 `Created` 상태로 생성하고 핸들을 반환한다.
///
/// # 반환값
/// - `Ok(handle)`: 양수 핸들 (이후 모든 VM 조작에 사용)
/// - `Err(VmError)`: 레지스트리 잠금 실패 시
pub fn create_vm() -> Result<i32, VmError> {
    let handle = NEXT_HANDLE.fetch_add(1, Ordering::SeqCst);
    let vm = VmInstance {
        handle,
        state: VmState::Created,
        vcpu_count: 0,
        memory_mb: 0,
    };
    with_registry(|reg| {
        reg.insert(handle, vm);
        tracing::info!(handle, "VM created");
        Ok(handle)
    })
}

/// VM 인스턴스를 파괴하고 레지스트리에서 제거한다.
///
/// # 매개변수
/// - `handle`: 삭제할 VM의 핸들
///
/// # 반환값
/// - `Ok(())`: 성공
/// - `Err(VmError::NotFound)`: 해당 핸들의 VM이 없음
pub fn destroy_vm(handle: i32) -> Result<(), VmError> {
    with_registry(|reg| {
        reg.remove(&handle).ok_or(VmError::NotFound(handle))?;
        tracing::info!(handle, "VM destroyed");
        Ok(())
    })
}

/// VM을 새 상태로 전이한다 (유효성 검증 포함).
///
/// # 매개변수
/// - `handle`: VM 핸들
/// - `target`: 전이할 목표 상태
///
/// # 반환값
/// - `Ok(VmState)`: 전이 후 새 상태
/// - `Err(VmError::InvalidState)`: 잘못된 상태 전이
pub fn transition_vm(handle: i32, target: VmState) -> Result<VmState, VmError> {
    with_vm(handle, |vm| {
        if !vm.state.can_transition_to(target) {
            return Err(VmError::InvalidState {
                current: vm.state,
                target,
            });
        }
        let prev = vm.state;
        vm.state = target;
        tracing::info!(handle, ?prev, ?target, "VM state transition");
        Ok(target)
    })
}

/// VM의 현재 상태를 조회한다.
///
/// # 반환값
/// - `Ok(VmState)`: 현재 상태
/// - `Err(VmError::NotFound)`: 해당 핸들의 VM이 없음
pub fn get_vm_state(handle: i32) -> Result<VmState, VmError> {
    with_vm(handle, |vm| Ok(vm.state))
}

/// VM을 설정한다 (vCPU 수, 메모리 크기). Created → Configured 전이를 수행한다.
///
/// # 매개변수
/// - `handle`: VM 핸들
/// - `vcpu_count`: 할당할 vCPU 수
/// - `memory_mb`: 할당할 메모리 크기 (MB)
///
/// # 반환값
/// - `Ok(())`: 설정 성공
/// - `Err(VmError::InvalidState)`: VM이 `Created` 상태가 아님
pub fn configure_vm(handle: i32, vcpu_count: u32, memory_mb: u64) -> Result<(), VmError> {
    with_vm(handle, |vm| {
        if vm.state != VmState::Created {
            return Err(VmError::InvalidState {
                current: vm.state,
                target: VmState::Configured,
            });
        }
        vm.vcpu_count = vcpu_count;
        vm.memory_mb = memory_mb;
        vm.state = VmState::Configured;
        tracing::info!(handle, vcpu_count, memory_mb, "VM configured");
        Ok(())
    })
}

/// 현재 활성 VM 수를 반환한다.
pub fn vm_count() -> i32 {
    registry().lock().map(|r| r.len() as i32).unwrap_or(0)
}

// ── FFI 함수들 ────────────────────────────────────────

// FFI: Go에서 호출. 새 VM을 생성하고 양수 핸들을 반환한다. 실패 시 음수 에러 코드.
#[no_mangle]
pub extern "C" fn hcv_vm_create() -> i32 {
    crate::panic_barrier::catch(|| match create_vm() {
        Ok(handle) => handle,
        Err(e) => {
            tracing::error!(%e, "hcv_vm_create failed");
            e.to_error_code()
        }
    })
}

// FFI: Go에서 호출. VM을 삭제한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_destroy(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match destroy_vm(handle) {
        Ok(()) => 0,
        Err(e) => {
            tracing::error!(handle, %e, "hcv_vm_destroy failed");
            e.to_error_code()
        }
    })
}

// FFI: Go에서 호출. VM 상태를 VmState 값(0~4)으로 반환. 실패 시 음수 에러 코드.
#[no_mangle]
pub extern "C" fn hcv_vm_get_state(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match get_vm_state(handle) {
        Ok(state) => state as i32,
        Err(e) => e.to_error_code(),
    })
}

// FFI: Go에서 호출. VM의 vCPU 수와 메모리를 설정한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_configure(handle: i32, vcpu_count: u32, memory_mb: u64) -> i32 {
    crate::panic_barrier::catch(|| match configure_vm(handle, vcpu_count, memory_mb) {
        Ok(()) => 0,
        Err(e) => {
            tracing::error!(handle, %e, "hcv_vm_configure failed");
            e.to_error_code()
        }
    })
}

// FFI: Go에서 호출. VM을 시작한다 (Configured → Running). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_start(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Running) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

// FFI: Go에서 호출. VM을 중지한다 (Running/Paused → Stopped). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_stop(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Stopped) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

// FFI: Go에서 호출. VM을 일시 중지한다 (Running → Paused). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_pause(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Paused) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

// FFI: Go에서 호출. VM을 재개한다 (Paused → Running). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vm_resume(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Running) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

// FFI: Go에서 호출. 현재 활성 VM 수를 반환한다.
#[no_mangle]
pub extern "C" fn hcv_vm_count() -> i32 {
    crate::panic_barrier::catch(vm_count)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_create_destroy() {
        let h = create_vm().unwrap();
        assert!(h > 0);
        assert_eq!(get_vm_state(h).unwrap(), VmState::Created);
        destroy_vm(h).unwrap();
        assert!(get_vm_state(h).is_err());
    }

    #[test]
    fn test_full_lifecycle() {
        let h = create_vm().unwrap();
        configure_vm(h, 4, 8192).unwrap();
        assert_eq!(get_vm_state(h).unwrap(), VmState::Configured);

        transition_vm(h, VmState::Running).unwrap();
        assert_eq!(get_vm_state(h).unwrap(), VmState::Running);

        transition_vm(h, VmState::Paused).unwrap();
        transition_vm(h, VmState::Running).unwrap();
        transition_vm(h, VmState::Stopped).unwrap();
        assert_eq!(get_vm_state(h).unwrap(), VmState::Stopped);

        destroy_vm(h).unwrap();
    }

    #[test]
    fn test_invalid_transition() {
        let h = create_vm().unwrap();
        // Created -> Running (invalid, must go through Configured)
        let err = transition_vm(h, VmState::Running);
        assert!(err.is_err());
        destroy_vm(h).unwrap();
    }

    #[test]
    fn test_state_matrix() {
        assert!(VmState::Created.can_transition_to(VmState::Configured));
        assert!(!VmState::Created.can_transition_to(VmState::Running));
        assert!(VmState::Running.can_transition_to(VmState::Paused));
        assert!(VmState::Running.can_transition_to(VmState::Stopped));
        assert!(VmState::Paused.can_transition_to(VmState::Running));
        assert!(!VmState::Stopped.can_transition_to(VmState::Running));
    }

    #[test]
    fn test_destroy_nonexistent() {
        assert!(destroy_vm(999_999).is_err());
    }

    #[test]
    fn test_vm_count() {
        let h1 = create_vm().unwrap();
        let h2 = create_vm().unwrap();
        let c = vm_count();
        assert!(c >= 2);
        destroy_vm(h1).unwrap();
        destroy_vm(h2).unwrap();
    }
}

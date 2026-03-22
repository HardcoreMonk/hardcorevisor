//! # vCPU 매니저 — Typestate 패턴 + FFI 래퍼
//!
//! ## 목적
//! vCPU의 생명주기를 관리한다. Rust 내부에서는 컴파일 타임 Typestate 패턴으로
//! 잘못된 상태 전이를 방지하고, FFI 경계에서는 런타임 `VCpuState` 열거형으로 검증한다.
//!
//! ## 아키텍처 위치
//! ```text
//! Go Controller → hcv_vcpu_* FFI → vcpu_mgr (런타임 상태 검증)
//! Rust 내부      → VCpu<S>       (컴파일 타임 상태 강제)
//! ```
//!
//! ## 핵심 개념
//! - Typestate: `VCpu<TCreated>` → `VCpu<TConfigured>` → `VCpu<TRunning>` ⇄ `VCpu<TPaused>`
//! - 제로 크기 마커 타입으로 상태를 인코딩하여 잘못된 전이는 컴파일 에러 발생
//! - FFI: `VCpuEntry`에 `VCpuState` 필드로 동적 상태 검증 수행
//!
//! ## 스레드 안전성
//! 전역 vCPU 레지스트리는 `Mutex<HashMap>`으로 보호된다.

use crate::panic_barrier::ErrorCode;
use std::collections::HashMap;
use std::marker::PhantomData;
use std::sync::{Mutex, OnceLock};

// ═══════════════════════════════════════════════════════════
// FFI-safe types (repr(C))
// ═══════════════════════════════════════════════════════════

/// FFI 런타임 검증용 vCPU 상태 열거형
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VCpuState {
    Created = 0,
    Configured = 1,
    Running = 2,
    Paused = 3,
}

/// x86-64 범용 레지스터 (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VCpuRegs {
    pub rax: u64,
    pub rbx: u64,
    pub rcx: u64,
    pub rdx: u64,
    pub rsi: u64,
    pub rdi: u64,
    pub rsp: u64,
    pub rbp: u64,
    pub r8: u64,
    pub r9: u64,
    pub r10: u64,
    pub r11: u64,
    pub r12: u64,
    pub r13: u64,
    pub r14: u64,
    pub r15: u64,
    pub rip: u64,
    pub rflags: u64,
}

/// x86 세그먼트 레지스터 (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct SegmentReg {
    pub base: u64,
    pub limit: u32,
    pub selector: u16,
    pub type_: u8,
    pub present: u8,
    pub dpl: u8,
    pub db: u8,
    pub s: u8,
    pub l: u8,
    pub g: u8,
    pub _pad: u8,
}

/// x86 특수 레지스터 (제어 레지스터, 세그먼트 등, FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VCpuSRegs {
    pub cs: SegmentReg,
    pub ds: SegmentReg,
    pub es: SegmentReg,
    pub fs: SegmentReg,
    pub gs: SegmentReg,
    pub ss: SegmentReg,
    pub cr0: u64,
    pub cr2: u64,
    pub cr3: u64,
    pub cr4: u64,
    pub efer: u64,
}

// ═══════════════════════════════════════════════════════════
// Typestate (Rust 내부 전용 — FFI로 노출하지 않음)
// ═══════════════════════════════════════════════════════════

/// Typestate 마커 트레이트 — 상태를 타입 파라미터로 인코딩
pub trait TypestateMarker {}
/// 생성 상태 마커
pub enum TCreated {}
/// 설정 완료 상태 마커
pub enum TConfigured {}
/// 실행 중 상태 마커
pub enum TRunning {}
/// 일시 중지 상태 마커
pub enum TPaused {}
impl TypestateMarker for TCreated {}
impl TypestateMarker for TConfigured {}
impl TypestateMarker for TRunning {}
impl TypestateMarker for TPaused {}

/// 컴파일 타임 상태 추적 vCPU (Rust 내부 전용).
///
/// 상태 전이 시 `self`를 소비하고 새로운 타입의 `VCpu`를 반환하므로,
/// 잘못된 상태 전이는 컴파일 에러를 발생시킨다.
pub struct VCpu<S: TypestateMarker> {
    pub id: u32,
    pub vm_handle: i32,
    _state: PhantomData<S>,
}

impl VCpu<TCreated> {
    pub fn new(id: u32, vm_handle: i32) -> Self {
        Self {
            id,
            vm_handle,
            _state: PhantomData,
        }
    }
    pub fn configure(self, _regs: &VCpuRegs, _sregs: &VCpuSRegs) -> VCpu<TConfigured> {
        VCpu {
            id: self.id,
            vm_handle: self.vm_handle,
            _state: PhantomData,
        }
    }
}

impl VCpu<TConfigured> {
    pub fn start(self) -> VCpu<TRunning> {
        VCpu {
            id: self.id,
            vm_handle: self.vm_handle,
            _state: PhantomData,
        }
    }
}

impl VCpu<TRunning> {
    pub fn pause(self) -> VCpu<TPaused> {
        VCpu {
            id: self.id,
            vm_handle: self.vm_handle,
            _state: PhantomData,
        }
    }
    pub fn inject_interrupt(&self, _irq: u32) { /* KVM_INTERRUPT ioctl stub */
    }
}

impl VCpu<TPaused> {
    pub fn resume(self) -> VCpu<TRunning> {
        VCpu {
            id: self.id,
            vm_handle: self.vm_handle,
            _state: PhantomData,
        }
    }
}

impl<S: TypestateMarker> VCpu<S> {
    pub fn id(&self) -> u32 {
        self.id
    }
}

// ═══════════════════════════════════════════════════════════
// 런타임 vCPU 레지스트리 (FFI용 동적 상태 검증)
// ═══════════════════════════════════════════════════════════

#[derive(Debug)]
#[allow(dead_code)]
struct VCpuEntry {
    vm_handle: i32,
    id: u32,
    state: VCpuState,
    regs: VCpuRegs,
    sregs: VCpuSRegs,
}

type VCpuKey = (i32, u32); // (vm_handle, vcpu_id)

fn vcpu_registry() -> &'static Mutex<HashMap<VCpuKey, VCpuEntry>> {
    static REG: OnceLock<Mutex<HashMap<VCpuKey, VCpuEntry>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

fn with_vcpu<F, R>(vm_handle: i32, vcpu_id: u32, f: F) -> Result<R, i32>
where
    F: FnOnce(&mut VCpuEntry) -> Result<R, i32>,
{
    let mut guard = vcpu_registry()
        .lock()
        .map_err(|_| ErrorCode::KvmError as i32)?;
    let key = (vm_handle, vcpu_id);
    let entry = guard.get_mut(&key).ok_or(ErrorCode::NotFound as i32)?;
    f(entry)
}

// ═══════════════════════════════════════════════════════════
// FFI 함수들
// ═══════════════════════════════════════════════════════════

// FFI: Go에서 호출. vCPU를 생성한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_create(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match vcpu_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        let key = (vm_handle, vcpu_id);
        if guard.contains_key(&key) {
            return ErrorCode::AlreadyExists as i32;
        }
        guard.insert(
            key,
            VCpuEntry {
                vm_handle,
                id: vcpu_id,
                state: VCpuState::Created,
                regs: VCpuRegs::default(),
                sregs: VCpuSRegs::default(),
            },
        );
        tracing::info!(vm_handle, vcpu_id, "vCPU created");
        0
    })
}

// FFI: Go에서 호출. vCPU를 설정한다 (Created → Configured). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_configure(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| {
            if e.state != VCpuState::Created {
                return Err(ErrorCode::InvalidState as i32);
            }
            e.state = VCpuState::Configured;
            tracing::info!(vm_handle, vcpu_id, "vCPU configured");
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. 범용 레지스터를 설정한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_vcpu_set_regs(vm_handle: i32, vcpu_id: u32, regs: *const VCpuRegs) -> i32 {
    crate::panic_barrier::catch(|| {
        if regs.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: caller guarantees regs points to a valid VCpuRegs
        let regs_val = unsafe { *regs };
        with_vcpu(vm_handle, vcpu_id, |e| {
            e.regs = regs_val;
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. 특수 레지스터를 설정한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_vcpu_set_sregs(vm_handle: i32, vcpu_id: u32, sregs: *const VCpuSRegs) -> i32 {
    crate::panic_barrier::catch(|| {
        if sregs.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let sregs_val = unsafe { *sregs };
        with_vcpu(vm_handle, vcpu_id, |e| {
            e.sregs = sregs_val;
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. vCPU를 시작한다 (Configured → Running). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_start(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| {
            if e.state != VCpuState::Configured {
                return Err(ErrorCode::InvalidState as i32);
            }
            e.state = VCpuState::Running;
            tracing::info!(vm_handle, vcpu_id, "vCPU started");
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. vCPU를 일시 중지한다 (Running → Paused). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_pause(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| {
            if e.state != VCpuState::Running {
                return Err(ErrorCode::InvalidState as i32);
            }
            e.state = VCpuState::Paused;
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. vCPU를 재개한다 (Paused → Running). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_resume(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| {
            if e.state != VCpuState::Paused {
                return Err(ErrorCode::InvalidState as i32);
            }
            e.state = VCpuState::Running;
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. 인터럽트를 주입한다 (Running 상태에서만). 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_inject_irq(vm_handle: i32, vcpu_id: u32, irq: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| {
            if e.state != VCpuState::Running {
                return Err(ErrorCode::InvalidState as i32);
            }
            tracing::debug!(vm_handle, vcpu_id, irq, "interrupt injected");
            Ok(0)
        })
        .unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. vCPU 상태를 VCpuState 값(0~3)으로 반환. 실패 시 음수 에러 코드.
#[no_mangle]
pub extern "C" fn hcv_vcpu_get_state(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        with_vcpu(vm_handle, vcpu_id, |e| Ok(e.state as i32)).unwrap_or_else(|e| e)
    })
}

// FFI: Go에서 호출. vCPU를 파괴한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_vcpu_destroy(vm_handle: i32, vcpu_id: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match vcpu_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.remove(&(vm_handle, vcpu_id)) {
            Some(_) => {
                tracing::info!(vm_handle, vcpu_id, "vCPU destroyed");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_typestate_lifecycle() {
        let vcpu = VCpu::<TCreated>::new(0, 1);
        let vcpu = vcpu.configure(&VCpuRegs::default(), &VCpuSRegs::default());
        let vcpu = vcpu.start();
        let vcpu = vcpu.pause();
        let _vcpu = vcpu.resume();
    }

    #[test]
    fn test_ffi_lifecycle() {
        let r = hcv_vcpu_create(100, 0);
        assert_eq!(r, 0);
        assert_eq!(hcv_vcpu_get_state(100, 0), VCpuState::Created as i32);
        assert_eq!(hcv_vcpu_configure(100, 0), 0);
        assert_eq!(hcv_vcpu_start(100, 0), 0);
        assert_eq!(hcv_vcpu_get_state(100, 0), VCpuState::Running as i32);
        assert_eq!(hcv_vcpu_pause(100, 0), 0);
        assert_eq!(hcv_vcpu_resume(100, 0), 0);
        assert_eq!(hcv_vcpu_destroy(100, 0), 0);
    }

    #[test]
    fn test_ffi_invalid_transition() {
        hcv_vcpu_create(101, 0);
        // Created -> Running (invalid)
        assert_eq!(hcv_vcpu_start(101, 0), ErrorCode::InvalidState as i32);
        hcv_vcpu_destroy(101, 0);
    }

    #[test]
    fn test_ffi_null_regs() {
        hcv_vcpu_create(102, 0);
        assert_eq!(
            hcv_vcpu_set_regs(102, 0, std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );
        hcv_vcpu_destroy(102, 0);
    }

    #[test]
    fn test_ffi_duplicate_create() {
        hcv_vcpu_create(103, 0);
        assert_eq!(hcv_vcpu_create(103, 0), ErrorCode::AlreadyExists as i32);
        hcv_vcpu_destroy(103, 0);
    }
}

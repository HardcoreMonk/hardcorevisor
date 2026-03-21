//! # KVM Manager — VM Instance Registry
//!
//! Thread-safe VM registry with runtime state machine validation.
//! All state transitions are validated at runtime for FFI callers.

use crate::panic_barrier::ErrorCode;
use std::collections::HashMap;
use std::sync::{
    atomic::{AtomicI32, Ordering},
    Mutex, OnceLock,
};

/// VM runtime state — exposed via FFI as `repr(C)`.
/// Mirrors the Typestate at runtime for Go callers.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VmState {
    Created = 0,
    Configured = 1,
    Running = 2,
    Paused = 3,
    Stopped = 4,
}

impl VmState {
    /// Check if transition to `target` is valid.
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

/// VM management errors
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

/// Internal VM instance representation
#[derive(Debug)]
pub struct VmInstance {
    pub handle: i32,
    pub state: VmState,
    pub vcpu_count: u32,
    pub memory_mb: u64,
}

// ── Global VM Registry (OnceLock + Mutex) ────────────────
static NEXT_HANDLE: AtomicI32 = AtomicI32::new(1);

fn registry() -> &'static Mutex<HashMap<i32, VmInstance>> {
    static REG: OnceLock<Mutex<HashMap<i32, VmInstance>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

fn with_registry<F, R>(f: F) -> Result<R, VmError>
where
    F: FnOnce(&mut HashMap<i32, VmInstance>) -> Result<R, VmError>,
{
    let mut guard = registry()
        .lock()
        .map_err(|e| VmError::Kvm(format!("registry lock poisoned: {e}")))?;
    f(&mut guard)
}

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

/// Create a new VM instance in `Created` state. Returns handle.
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

/// Destroy a VM instance.
pub fn destroy_vm(handle: i32) -> Result<(), VmError> {
    with_registry(|reg| {
        reg.remove(&handle).ok_or(VmError::NotFound(handle))?;
        tracing::info!(handle, "VM destroyed");
        Ok(())
    })
}

/// Transition VM to a new state with validation.
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

/// Get VM state.
pub fn get_vm_state(handle: i32) -> Result<VmState, VmError> {
    with_vm(handle, |vm| Ok(vm.state))
}

/// Configure VM (set vCPU count and memory). Transitions Created -> Configured.
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

/// Get count of active VMs.
pub fn vm_count() -> i32 {
    registry().lock().map(|r| r.len() as i32).unwrap_or(0)
}

// ── FFI Functions ────────────────────────────────────────

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

#[no_mangle]
pub extern "C" fn hcv_vm_get_state(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match get_vm_state(handle) {
        Ok(state) => state as i32,
        Err(e) => e.to_error_code(),
    })
}

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

#[no_mangle]
pub extern "C" fn hcv_vm_start(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Running) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

#[no_mangle]
pub extern "C" fn hcv_vm_stop(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Stopped) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

#[no_mangle]
pub extern "C" fn hcv_vm_pause(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Paused) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

#[no_mangle]
pub extern "C" fn hcv_vm_resume(handle: i32) -> i32 {
    crate::panic_barrier::catch(|| match transition_vm(handle, VmState::Running) {
        Ok(_) => 0,
        Err(e) => e.to_error_code(),
    })
}

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

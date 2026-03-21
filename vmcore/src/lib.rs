//! # vmcore — HardCoreVisor KVM Core Staticlib
//!
//! ## Architecture
//! ```text
//! Go Controller (CGo) ──── extern "C" ────→ vmcore (Rust staticlib)
//!                                              │
//!         panic_barrier::catch()  ←────────────┤
//!              │                                │
//!    ┌─────────┴──────────┐         ┌───────────┴───────────┐
//!    │ kvm_mgr (VM CRUD)  │         │ vcpu_mgr (Typestate)  │
//!    │ memory_mgr (EPT)   │         │ event_ring (SPSC)     │
//!    │ virtio_blk (Block) │         │ virtio_split_queue    │
//!    └────────────────────┘         └───────────────────────┘
//! ```
//!
//! ## FFI Convention
//! - All `extern "C"` functions wrapped with `panic_barrier::catch()`
//! - Return `i32`: positive/zero = success, negative = `ErrorCode`
//! - `repr(C)` on all structures crossing FFI boundary
//! - Rust allocations freed via `hcv_*_free()` functions
//!
//! ## Modules
//! | Module | FFI Functions | Purpose |
//! |--------|--------------|---------|
//! | `panic_barrier` | 0 (internal) | catch_unwind FFI safety |
//! | `kvm_mgr` | 9 | VM lifecycle CRUD + state machine |
//! | `vcpu_mgr` | 10 | Typestate vCPU + register management |
//! | `memory_mgr` | 8 | Guest memory regions + dirty log |
//! | `virtio_split_queue` | 0 (internal) | Virtio Split Queue implementation |
//! | `virtio_blk` | 7 | Virtio block device emulation |
//! | `event_ring` | 6 | Lock-free SPSC event bus |
//! | `lib` (this) | 3 | init, version, shutdown |
//! | **Total** | **43** | |

pub mod event_ring;
pub mod io_engine;
pub mod kvm_mgr;
pub mod kvm_sys;
pub mod memory_mgr;
pub mod panic_barrier;
pub mod vcpu_mgr;
pub mod virtio_blk;
pub mod virtio_split_queue;

// Re-export FFI types for cbindgen
pub use event_ring::{Event, EventRingHandle, EventType};
pub use kvm_mgr::VmState;
pub use memory_mgr::MemoryRegion;
pub use panic_barrier::ErrorCode;
pub use vcpu_mgr::{SegmentReg, VCpuRegs, VCpuSRegs, VCpuState};
pub use virtio_blk::{VirtioBlkConfig, VirtioBlkStats};

/// Library version
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

// ═══════════════════════════════════════════════════════════
// Common FFI Entry Points
// ═══════════════════════════════════════════════════════════

use std::sync::atomic::{AtomicBool, Ordering};

static INITIALIZED: AtomicBool = AtomicBool::new(false);

/// Initialize the vmcore library. Must be called once before any other function.
/// Returns 0 on success, negative error code on failure.
/// Safe to call multiple times — second call is a no-op returning 0.
#[no_mangle]
pub extern "C" fn hcv_init() -> i32 {
    panic_barrier::catch(|| {
        if INITIALIZED.swap(true, Ordering::SeqCst) {
            return 0; // already initialized
        }

        // Initialize tracing (structured logging)
        let _ = tracing_subscriber::fmt()
            .with_env_filter(
                tracing_subscriber::EnvFilter::try_from_default_env()
                    .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
            )
            .try_init();

        tracing::info!("vmcore v{} initialized", VERSION);
        0
    })
}

/// Get the vmcore library version as a null-terminated C string.
/// Returned pointer is static — caller must NOT free it.
#[no_mangle]
pub extern "C" fn hcv_version() -> *const libc::c_char {
    static VERSION_CSTR: &[u8] = concat!(env!("CARGO_PKG_VERSION"), "\0").as_bytes();
    VERSION_CSTR.as_ptr() as *const libc::c_char
}

/// Shutdown the vmcore library. Cleans up global state.
/// Returns 0 on success.
#[no_mangle]
pub extern "C" fn hcv_shutdown() -> i32 {
    panic_barrier::catch(|| {
        if !INITIALIZED.swap(false, Ordering::SeqCst) {
            return 0; // not initialized, no-op
        }
        tracing::info!("vmcore shutting down");
        // Future: cleanup VM registry, event rings, etc.
        0
    })
}

// ═══════════════════════════════════════════════════════════
// FFI Function Summary (for documentation / cbindgen)
// ═══════════════════════════════════════════════════════════
//
// Common:
//   hcv_init()                                           -> i32
//   hcv_version()                                        -> *const c_char
//   hcv_shutdown()                                       -> i32
//
// kvm_mgr:
//   hcv_vm_create()                                      -> i32 (handle)
//   hcv_vm_destroy(handle)                               -> i32
//   hcv_vm_get_state(handle)                             -> i32 (VmState)
//   hcv_vm_configure(handle, vcpu_count, memory_mb)      -> i32
//   hcv_vm_start(handle)                                 -> i32
//   hcv_vm_stop(handle)                                  -> i32
//   hcv_vm_pause(handle)                                 -> i32
//   hcv_vm_resume(handle)                                -> i32
//   hcv_vm_count()                                       -> i32
//
// vcpu_mgr:
//   hcv_vcpu_create(vm_handle, vcpu_id)                  -> i32
//   hcv_vcpu_configure(vm_handle, vcpu_id)               -> i32
//   hcv_vcpu_set_regs(vm_handle, vcpu_id, *regs)        -> i32
//   hcv_vcpu_set_sregs(vm_handle, vcpu_id, *sregs)      -> i32
//   hcv_vcpu_start(vm_handle, vcpu_id)                   -> i32
//   hcv_vcpu_pause(vm_handle, vcpu_id)                   -> i32
//   hcv_vcpu_resume(vm_handle, vcpu_id)                  -> i32
//   hcv_vcpu_inject_irq(vm_handle, vcpu_id, irq)        -> i32
//   hcv_vcpu_get_state(vm_handle, vcpu_id)               -> i32
//   hcv_vcpu_destroy(vm_handle, vcpu_id)                 -> i32
//
// memory_mgr:
//   hcv_mem_add_region(vm_handle, *region)               -> i32
//   hcv_mem_remove_region(vm_handle, slot)               -> i32
//   hcv_mem_get_region(vm_handle, slot, *out)            -> i32
//   hcv_mem_set_dirty_log(vm_handle, slot, enable)       -> i32
//   hcv_mem_get_dirty_log(vm_handle, slot, *bitmap, len) -> i32
//   hcv_mem_alloc_guest(size_bytes)                      -> i64
//   hcv_mem_free_guest(addr, size_bytes)                 -> i32
//   hcv_mem_balloon(vm_handle, target_mb)                -> i32
//
// virtio_blk:
//   hcv_virtio_blk_create(vm_handle, *config)            -> i32 (handle)
//   hcv_virtio_blk_destroy(dev_handle)                   -> i32
//   hcv_virtio_blk_process_queue(dev_handle)             -> i32
//   hcv_virtio_blk_get_stats(dev_handle, *stats)         -> i32
//   hcv_virtio_blk_resize(dev_handle, new_sectors)       -> i32
//   hcv_virtio_blk_attach(vm_handle, dev_handle)         -> i32
//   hcv_virtio_blk_detach(vm_handle, dev_handle)         -> i32
//
// event_ring:
//   hcv_event_ring_create(capacity)                      -> *mut Handle
//   hcv_event_ring_destroy(*handle)                      -> void
//   hcv_event_ring_push(*handle, *event)                 -> i32
//   hcv_event_ring_pop(*handle, *out)                    -> i32
//   hcv_event_ring_len(*handle)                          -> u32
//   hcv_event_ring_is_empty(*handle)                     -> i32

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_init_idempotent() {
        assert_eq!(hcv_init(), 0);
        assert_eq!(hcv_init(), 0); // second call is no-op
    }

    #[test]
    fn test_version() {
        let ptr = hcv_version();
        assert!(!ptr.is_null());
        let cstr = unsafe { std::ffi::CStr::from_ptr(ptr) };
        let version = cstr.to_str().unwrap();
        assert_eq!(version, "0.1.0");
    }

    #[test]
    fn test_shutdown() {
        assert_eq!(hcv_shutdown(), 0);
    }

    /// End-to-end: init → create VM → configure → start → pause → resume → stop → destroy → shutdown
    #[test]
    fn test_full_e2e() {
        hcv_init();

        // VM lifecycle
        let vm = kvm_mgr::hcv_vm_create();
        assert!(vm > 0);
        assert_eq!(kvm_mgr::hcv_vm_configure(vm, 2, 4096), 0);
        assert_eq!(kvm_mgr::hcv_vm_start(vm), 0);
        assert_eq!(kvm_mgr::hcv_vm_pause(vm), 0);
        assert_eq!(kvm_mgr::hcv_vm_resume(vm), 0);
        assert_eq!(kvm_mgr::hcv_vm_stop(vm), 0);
        assert_eq!(kvm_mgr::hcv_vm_destroy(vm), 0);

        // vCPU lifecycle
        let vm2 = kvm_mgr::hcv_vm_create();
        assert_eq!(vcpu_mgr::hcv_vcpu_create(vm2, 0), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_configure(vm2, 0), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_start(vm2, 0), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_inject_irq(vm2, 0, 32), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_pause(vm2, 0), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_resume(vm2, 0), 0);
        assert_eq!(vcpu_mgr::hcv_vcpu_destroy(vm2, 0), 0);
        kvm_mgr::hcv_vm_destroy(vm2);

        // Event ring
        let ring = event_ring::hcv_event_ring_create(16);
        assert!(!ring.is_null());
        let evt = event_ring::Event {
            event_type: event_ring::EventType::VmStateChanged,
            vm_handle: 1,
            vcpu_id: 0,
            data: 2,
            timestamp_ns: 0,
        };
        assert_eq!(event_ring::hcv_event_ring_push(ring, &evt), 0);
        let mut out = event_ring::Event::default();
        assert_eq!(event_ring::hcv_event_ring_pop(ring, &mut out), 0);
        assert_eq!(out.vm_handle, 1);
        event_ring::hcv_event_ring_destroy(ring);

        hcv_shutdown();
    }
}

//! # Event Ring — SPSC Lock-Free Ring Buffer with FFI
//!
//! Producer: vmcore (Rust) — `push`
//! Consumer: Go Controller (via CGo) — `pop`
//! Uses Atomic Acquire/Release ordering for synchronization.

use crate::panic_barrier::ErrorCode;
use std::sync::atomic::{AtomicU64, Ordering};

/// Event types sent from vmcore to Go Controller
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventType {
    VmStateChanged = 1,
    VCpuExit = 2,
    IoRequest = 3,
    InterruptInjected = 4,
    MigrationProgress = 5,
    Error = 6,
}

/// A single event (FFI-safe, fixed 32 bytes)
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct Event {
    pub event_type: EventType,
    pub vm_handle: i32,
    pub vcpu_id: u32,
    pub data: u64,
    pub timestamp_ns: u64,
}

impl Default for Event {
    fn default() -> Self {
        Self {
            event_type: EventType::VmStateChanged,
            vm_handle: 0,
            vcpu_id: 0,
            data: 0,
            timestamp_ns: 0,
        }
    }
}

/// Get current timestamp in nanoseconds
fn now_ns() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos() as u64
}

/// Internal ring buffer (heap-allocated, fixed capacity)
struct EventRingInner {
    buffer: Vec<Event>,
    capacity: u64,
    head: AtomicU64, // write position (producer)
    tail: AtomicU64, // read position (consumer)
}

impl EventRingInner {
    fn new(capacity: u32) -> Self {
        let cap = capacity.next_power_of_two() as u64;
        Self {
            buffer: vec![Event::default(); cap as usize],
            capacity: cap,
            head: AtomicU64::new(0),
            tail: AtomicU64::new(0),
        }
    }

    fn push(&mut self, mut event: Event) -> bool {
        let head = self.head.load(Ordering::Relaxed);
        let tail = self.tail.load(Ordering::Acquire);
        if head - tail >= self.capacity {
            return false; // full
        }
        if event.timestamp_ns == 0 {
            event.timestamp_ns = now_ns();
        }
        let idx = (head % self.capacity) as usize;
        self.buffer[idx] = event;
        self.head.store(head + 1, Ordering::Release);
        true
    }

    fn pop(&self) -> Option<Event> {
        let tail = self.tail.load(Ordering::Relaxed);
        let head = self.head.load(Ordering::Acquire);
        if tail >= head {
            return None; // empty
        }
        let idx = (tail % self.capacity) as usize;
        let event = self.buffer[idx];
        self.tail.store(tail + 1, Ordering::Release);
        Some(event)
    }

    fn len(&self) -> u32 {
        let head = self.head.load(Ordering::Acquire);
        let tail = self.tail.load(Ordering::Acquire);
        (head - tail) as u32
    }

    fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

/// Opaque handle for FFI (Go receives this as `*mut EventRingHandle`)
pub struct EventRingHandle {
    inner: EventRingInner,
}

// ── FFI Functions ────────────────────────────────────────

/// Create a new event ring. Caller owns the returned pointer.
/// Free with `hcv_event_ring_destroy`.
#[no_mangle]
pub extern "C" fn hcv_event_ring_create(capacity: u32) -> *mut EventRingHandle {
    crate::panic_barrier::catch_ptr(|| {
        if capacity == 0 {
            return std::ptr::null_mut();
        }
        let ring = EventRingHandle {
            inner: EventRingInner::new(capacity),
        };
        let ptr = Box::into_raw(Box::new(ring));
        tracing::info!(?ptr, capacity, "event ring created");
        ptr
    })
}

/// Destroy an event ring. Must pass the pointer from `hcv_event_ring_create`.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_destroy(ring: *mut EventRingHandle) {
    if ring.is_null() {
        return;
    }
    // SAFETY: ring was created by Box::into_raw in hcv_event_ring_create
    let _owned = unsafe { Box::from_raw(ring) };
    tracing::info!(?ring, "event ring destroyed");
    // _owned is dropped here, freeing the memory
}

/// Push an event into the ring (producer side).
/// Returns 0 on success, ErrorCode::BufferFull (-9) if full.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_push(ring: *mut EventRingHandle, event: *const Event) -> i32 {
    crate::panic_barrier::catch_mut(|| {
        if ring.is_null() || event.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: caller guarantees ring is valid (from create) and event is readable
        let ring_ref = unsafe { &mut *ring };
        let evt = unsafe { *event };
        if ring_ref.inner.push(evt) {
            0
        } else {
            ErrorCode::BufferFull as i32
        }
    })
}

/// Pop an event from the ring (consumer side).
/// Returns 0 and writes to `out` on success, ErrorCode::NotFound (-4) if empty.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_pop(ring: *mut EventRingHandle, out: *mut Event) -> i32 {
    crate::panic_barrier::catch(|| {
        if ring.is_null() || out.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: ring is valid, out is writable
        let ring_ref = unsafe { &*ring };
        match ring_ref.inner.pop() {
            Some(event) => {
                unsafe {
                    *out = event;
                }
                0
            }
            None => ErrorCode::NotFound as i32, // empty (not an error, just no data)
        }
    })
}

/// Get the number of events in the ring.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_len(ring: *const EventRingHandle) -> u32 {
    if ring.is_null() {
        return 0;
    }
    let ring_ref = unsafe { &*ring };
    ring_ref.inner.len()
}

/// Check if the ring is empty. Returns 1 if empty, 0 if not.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_is_empty(ring: *const EventRingHandle) -> i32 {
    if ring.is_null() {
        return 1;
    }
    let ring_ref = unsafe { &*ring };
    if ring_ref.inner.is_empty() {
        1
    } else {
        0
    }
}

// ── Convenience: push event from vmcore internals ────────

/// Helper to create and push a VM state change event.
pub fn emit_vm_state_changed(ring: &mut EventRingHandle, vm_handle: i32, new_state: u64) -> bool {
    ring.inner.push(Event {
        event_type: EventType::VmStateChanged,
        vm_handle,
        vcpu_id: 0,
        data: new_state,
        timestamp_ns: 0, // auto-filled by push()
    })
}

/// Helper to create and push a vCPU exit event.
pub fn emit_vcpu_exit(
    ring: &mut EventRingHandle,
    vm_handle: i32,
    vcpu_id: u32,
    exit_reason: u64,
) -> bool {
    ring.inner.push(Event {
        event_type: EventType::VCpuExit,
        vm_handle,
        vcpu_id,
        data: exit_reason,
        timestamp_ns: 0,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_create_destroy() {
        let ring = hcv_event_ring_create(16);
        assert!(!ring.is_null());
        assert_eq!(hcv_event_ring_is_empty(ring), 1);
        hcv_event_ring_destroy(ring);
    }

    #[test]
    fn test_push_pop() {
        let ring = hcv_event_ring_create(4);
        let evt = Event {
            event_type: EventType::VmStateChanged,
            vm_handle: 1,
            vcpu_id: 0,
            data: 2, // Running
            timestamp_ns: 0,
        };
        assert_eq!(hcv_event_ring_push(ring, &evt), 0);
        assert_eq!(hcv_event_ring_len(ring), 1);

        let mut out = Event::default();
        assert_eq!(hcv_event_ring_pop(ring, &mut out), 0);
        assert_eq!(out.vm_handle, 1);
        assert_eq!(out.data, 2);
        assert!(out.timestamp_ns > 0); // auto-filled

        assert_eq!(hcv_event_ring_is_empty(ring), 1);
        hcv_event_ring_destroy(ring);
    }

    #[test]
    fn test_buffer_full() {
        let ring = hcv_event_ring_create(2); // rounds to 2
        let evt = Event::default();
        assert_eq!(hcv_event_ring_push(ring, &evt), 0);
        assert_eq!(hcv_event_ring_push(ring, &evt), 0);
        assert_eq!(
            hcv_event_ring_push(ring, &evt),
            ErrorCode::BufferFull as i32
        );
        hcv_event_ring_destroy(ring);
    }

    #[test]
    fn test_pop_empty() {
        let ring = hcv_event_ring_create(4);
        let mut out = Event::default();
        assert_eq!(
            hcv_event_ring_pop(ring, &mut out),
            ErrorCode::NotFound as i32
        );
        hcv_event_ring_destroy(ring);
    }

    #[test]
    fn test_null_safety() {
        assert!(hcv_event_ring_create(0).is_null());
        hcv_event_ring_destroy(std::ptr::null_mut()); // should not crash
        assert_eq!(
            hcv_event_ring_push(std::ptr::null_mut(), std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );
        assert_eq!(hcv_event_ring_len(std::ptr::null()), 0);
        assert_eq!(hcv_event_ring_is_empty(std::ptr::null()), 1);
    }

    #[test]
    fn test_emit_helpers() {
        let ring_ptr = hcv_event_ring_create(8);
        let ring = unsafe { &mut *ring_ptr };
        assert!(emit_vm_state_changed(ring, 1, 2));
        assert!(emit_vcpu_exit(ring, 1, 0, 0x80));
        assert_eq!(hcv_event_ring_len(ring_ptr), 2);
        hcv_event_ring_destroy(ring_ptr);
    }

    #[test]
    fn test_wrap_around() {
        let ring = hcv_event_ring_create(4); // capacity=4
        let evt = Event::default();
        let mut out = Event::default();

        // Fill and drain multiple times to test wrap-around
        for _ in 0..3 {
            for _ in 0..4 {
                assert_eq!(hcv_event_ring_push(ring, &evt), 0);
            }
            for _ in 0..4 {
                assert_eq!(hcv_event_ring_pop(ring, &mut out), 0);
            }
            assert_eq!(hcv_event_ring_is_empty(ring), 1);
        }
        hcv_event_ring_destroy(ring);
    }
}

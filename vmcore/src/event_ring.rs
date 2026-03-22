//! # 이벤트 링 — SPSC Lock-Free 링 버퍼 (FFI 포함)
//!
//! ## 목적
//! vmcore(Rust)에서 발생하는 이벤트를 Go Controller에 비동기적으로 전달하는
//! 단일 생산자/단일 소비자(SPSC) 락-프리 링 버퍼.
//!
//! ## 아키텍처 위치
//! ```text
//! vmcore (Rust) ──push──→ EventRing ──pop──→ Go Controller (CGo)
//! ```
//! - 생산자: vmcore 내부 (VM 상태 변경, vCPU 종료 등)
//! - 소비자: Go Controller (CGo를 통해 `hcv_event_ring_pop` 호출)
//!
//! ## 핵심 개념
//! - 용량은 2의 거듭제곱으로 반올림 (모듈러 연산 최적화)
//! - `AtomicU64`의 `Acquire`/`Release` 오더링으로 동기화
//! - `head`: 쓰기 위치 (생산자가 증가), `tail`: 읽기 위치 (소비자가 증가)
//! - 타임스탬프는 push 시 자동 채워짐 (0이면 `now_ns()` 호출)
//!
//! ## 스레드 안전성
//! SPSC 설계: 정확히 하나의 생산자 스레드와 하나의 소비자 스레드만 허용.
//! 여러 생산자/소비자가 접근하면 데이터 경합이 발생한다.
//! FFI 함수는 `panic_barrier`로 래핑되어 패닉 안전하다.

use crate::panic_barrier::ErrorCode;
use std::sync::atomic::{AtomicU64, Ordering};

/// vmcore에서 Go Controller로 전송되는 이벤트 유형
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventType {
    /// VM 상태 변경 (data 필드에 새 VmState 값)
    VmStateChanged = 1,
    /// vCPU 종료 (data 필드에 종료 사유)
    VCpuExit = 2,
    /// I/O 요청 발생
    IoRequest = 3,
    /// 인터럽트 주입 완료
    InterruptInjected = 4,
    /// 마이그레이션 진행 상황
    MigrationProgress = 5,
    /// 에러 발생
    Error = 6,
}

/// 단일 이벤트 구조체 (FFI-safe, 고정 32바이트).
///
/// Go 측에서 C 구조체로 직접 읽을 수 있다.
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct Event {
    /// 이벤트 유형
    pub event_type: EventType,
    /// 이벤트가 발생한 VM의 핸들
    pub vm_handle: i32,
    /// 이벤트가 발생한 vCPU ID (해당 없으면 0)
    pub vcpu_id: u32,
    /// 이벤트별 데이터 (예: 새 상태 값, 종료 사유 등)
    pub data: u64,
    /// 이벤트 발생 시각 (UNIX epoch 기준 나노초, push 시 자동 설정)
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

/// 현재 시각을 UNIX epoch 기준 나노초로 반환한다.
fn now_ns() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos() as u64
}

/// 내부 링 버퍼 (힙 할당, 고정 용량).
/// head와 tail은 절대값으로 단조 증가하며, 모듈러 연산으로 인덱스를 계산한다.
struct EventRingInner {
    buffer: Vec<Event>,
    /// 용량 (2의 거듭제곱, 모듈러 연산 최적화)
    capacity: u64,
    /// 쓰기 위치 (생산자만 수정)
    head: AtomicU64,
    /// 읽기 위치 (소비자만 수정)
    tail: AtomicU64,
}

impl EventRingInner {
    /// 주어진 용량으로 링 버퍼를 생성한다.
    /// 용량은 2의 거듭제곱으로 반올림되어 모듈러 연산을 비트 AND로 최적화한다.
    fn new(capacity: u32) -> Self {
        let cap = capacity.next_power_of_two() as u64;
        Self {
            buffer: vec![Event::default(); cap as usize],
            capacity: cap,
            head: AtomicU64::new(0),
            tail: AtomicU64::new(0),
        }
    }

    /// 이벤트를 링에 추가한다 (생산자 측).
    /// 버퍼가 가득 차면 `false`를 반환한다. timestamp_ns가 0이면 자동으로 현재 시각을 설정한다.
    fn push(&mut self, mut event: Event) -> bool {
        // WHY: head는 Relaxed — 생산자만 수정하므로 다른 스레드와의 동기화 불필요
        let head = self.head.load(Ordering::Relaxed);
        // WHY: tail은 Acquire — 소비자가 Release로 쓴 tail을 최신 값으로 읽어야 함
        let tail = self.tail.load(Ordering::Acquire);
        if head - tail >= self.capacity {
            return false; // 버퍼 가득 참
        }
        if event.timestamp_ns == 0 {
            event.timestamp_ns = now_ns();
        }
        let idx = (head % self.capacity) as usize;
        self.buffer[idx] = event;
        // WHY: Release — 소비자가 head를 Acquire로 읽을 때 buffer[idx] 쓰기가 보이도록 보장
        self.head.store(head + 1, Ordering::Release);
        true
    }

    /// 이벤트를 링에서 꺼낸다 (소비자 측).
    /// 버퍼가 비어 있으면 `None`을 반환한다.
    fn pop(&self) -> Option<Event> {
        // WHY: tail은 Relaxed — 소비자만 수정하므로 다른 스레드와의 동기화 불필요
        let tail = self.tail.load(Ordering::Relaxed);
        // WHY: head는 Acquire — 생산자가 Release로 쓴 head를 최신 값으로 읽어야 함
        let head = self.head.load(Ordering::Acquire);
        if tail >= head {
            return None; // 비어 있음
        }
        let idx = (tail % self.capacity) as usize;
        let event = self.buffer[idx];
        // WHY: Release — 생산자가 tail을 Acquire로 읽을 때 이 소비 완료가 보이도록 보장
        self.tail.store(tail + 1, Ordering::Release);
        Some(event)
    }

    /// 링에 있는 이벤트 수를 반환한다.
    fn len(&self) -> u32 {
        let head = self.head.load(Ordering::Acquire);
        let tail = self.tail.load(Ordering::Acquire);
        (head - tail) as u32
    }

    /// 링이 비어 있는지 확인한다.
    fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

/// FFI용 불투명 핸들 (Go 측에서 `*mut EventRingHandle`로 수신).
/// `hcv_event_ring_create`로 생성하고 `hcv_event_ring_destroy`로 해제해야 한다.
pub struct EventRingHandle {
    inner: EventRingInner,
}

// ── FFI 함수들 ────────────────────────────────────────

// FFI: Go에서 호출. 새 이벤트 링을 생성한다. 호출자가 반환된 포인터를 소유한다.
// 반환값: 유효한 포인터 또는 null (capacity=0일 때).
// 반드시 `hcv_event_ring_destroy`로 해제해야 한다.
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

// FFI: Go에서 호출. 이벤트 링을 파괴하고 메모리를 해제한다. null 포인터는 무시.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_destroy(ring: *mut EventRingHandle) {
    if ring.is_null() {
        return;
    }
    // SAFETY: ring은 hcv_event_ring_create에서 Box::into_raw로 생성된 유효한 포인터
    let _owned = unsafe { Box::from_raw(ring) };
    tracing::info!(?ring, "event ring destroyed");
    // _owned is dropped here, freeing the memory
}

// FFI: Go에서 호출. 이벤트를 링에 추가한다 (생산자 측).
// 반환값: 0=성공, -9(BufferFull)=링이 가득 참, -2(InvalidArg)=null 포인터.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_push(ring: *mut EventRingHandle, event: *const Event) -> i32 {
    crate::panic_barrier::catch_mut(|| {
        if ring.is_null() || event.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: 호출자가 ring은 create에서 받은 유효한 포인터이고
        //         event는 읽기 가능한 유효한 Event 포인터임을 보장
        let ring_ref = unsafe { &mut *ring };
        let evt = unsafe { *event };
        if ring_ref.inner.push(evt) {
            0
        } else {
            ErrorCode::BufferFull as i32
        }
    })
}

// FFI: Go에서 호출. 이벤트를 링에서 꺼낸다 (소비자 측).
// 반환값: 0=성공(out에 이벤트 기록), -4(NotFound)=비어 있음, -2(InvalidArg)=null 포인터.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_pop(ring: *mut EventRingHandle, out: *mut Event) -> i32 {
    crate::panic_barrier::catch(|| {
        if ring.is_null() || out.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: ring은 유효한 포인터, out은 쓰기 가능한 유효한 Event 포인터
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

// FFI: Go에서 호출. 링에 있는 이벤트 수를 반환한다. null이면 0.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_event_ring_len(ring: *const EventRingHandle) -> u32 {
    if ring.is_null() {
        return 0;
    }
    let ring_ref = unsafe { &*ring };
    ring_ref.inner.len()
}

// FFI: Go에서 호출. 링이 비어 있으면 1, 아니면 0을 반환한다.
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

/// VM 상태 변경 이벤트를 생성하여 링에 추가하는 헬퍼 함수.
///
/// # 매개변수
/// - `ring`: 이벤트 링 핸들
/// - `vm_handle`: 상태가 변경된 VM의 핸들
/// - `new_state`: 새로운 VmState 값 (u64로 캐스팅)
///
/// # 반환값
/// - `true`: 성공, `false`: 링이 가득 참
pub fn emit_vm_state_changed(ring: &mut EventRingHandle, vm_handle: i32, new_state: u64) -> bool {
    ring.inner.push(Event {
        event_type: EventType::VmStateChanged,
        vm_handle,
        vcpu_id: 0,
        data: new_state,
        timestamp_ns: 0, // auto-filled by push()
    })
}

/// vCPU 종료 이벤트를 생성하여 링에 추가하는 헬퍼 함수.
///
/// # 매개변수
/// - `ring`: 이벤트 링 핸들
/// - `vm_handle`: VM 핸들
/// - `vcpu_id`: 종료된 vCPU ID
/// - `exit_reason`: KVM 종료 사유 코드
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

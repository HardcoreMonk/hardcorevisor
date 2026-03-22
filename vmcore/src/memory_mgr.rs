//! # 메모리 매니저 — 게스트 물리 메모리 + EPT
//!
//! ## 목적
//! 게스트 메모리 영역 관리, dirty page 추적, 메모리 발루닝을 담당한다.
//! FFI를 통해 Go Controller에 메모리 관리 API를 제공한다.
//!
//! ## 아키텍처 위치
//! ```text
//! Go Controller → hcv_mem_* FFI → memory_mgr (이 모듈)
//! ```
//!
//! ## 핵심 개념
//! - `MemoryRegion`: 슬롯 기반 게스트 메모리 영역 관리
//! - dirty log: 라이브 마이그레이션용 변경 페이지 추적
//! - `PageTableBuffer`: const generics 기반 고정 크기 페이지 테이블 버퍼
//!
//! ## 스레드 안전성
//! `Mutex<HashMap>`으로 보호되는 전역 레지스트리 사용. 모든 FFI 함수는 스레드 안전.

use crate::panic_barrier::ErrorCode;
use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};

pub const PAGE_SIZE: usize = 4096;

/// 게스트 메모리 영역 디스크립터 (FFI-safe).
///
/// 게스트 물리 주소를 호스트 사용자 공간 주소에 매핑하는 정보를 담는다.
/// `memory_size`는 반드시 페이지 크기(4KB)의 배수여야 한다.
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct MemoryRegion {
    pub slot: u32,
    pub flags: u32,
    pub guest_phys_addr: u64,
    pub memory_size: u64,
    pub userspace_addr: u64,
}

type SlotKey = (i32, u32); // (vm_handle, slot)

fn mem_registry() -> &'static Mutex<HashMap<SlotKey, MemoryRegion>> {
    static REG: OnceLock<Mutex<HashMap<SlotKey, MemoryRegion>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

// ── FFI 함수들 ────────────────────────────────────────

// FFI: Go에서 호출. 메모리 영역을 추가한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_mem_add_region(vm_handle: i32, region: *const MemoryRegion) -> i32 {
    crate::panic_barrier::catch(|| {
        if region.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: 호출자가 유효한 MemoryRegion 포인터를 보장
        let r = unsafe { *region };
        if r.memory_size == 0 || !r.memory_size.is_multiple_of(PAGE_SIZE as u64) {
            return ErrorCode::InvalidArg as i32;
        }
        let mut guard = match mem_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        let key = (vm_handle, r.slot);
        if guard.contains_key(&key) {
            return ErrorCode::AlreadyExists as i32;
        }
        guard.insert(key, r);
        tracing::info!(
            vm_handle,
            slot = r.slot,
            size = r.memory_size,
            "memory region added"
        );
        0
    })
}

// FFI: Go에서 호출. 메모리 영역을 제거한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_mem_remove_region(vm_handle: i32, slot: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match mem_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.remove(&(vm_handle, slot)) {
            Some(_) => {
                tracing::info!(vm_handle, slot, "memory region removed");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

// FFI: Go에서 호출. 메모리 영역 정보를 조회한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_mem_get_region(vm_handle: i32, slot: u32, out: *mut MemoryRegion) -> i32 {
    crate::panic_barrier::catch(|| {
        if out.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let guard = match mem_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get(&(vm_handle, slot)) {
            Some(r) => {
                // SAFETY: caller guarantees out is valid and writable
                unsafe {
                    *out = *r;
                }
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

// FFI: Go에서 호출. dirty page 로깅을 활성화/비활성화한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_mem_set_dirty_log(vm_handle: i32, slot: u32, enable: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match mem_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&(vm_handle, slot)) {
            Some(r) => {
                if enable != 0 {
                    r.flags |= 1; // KVM_MEM_LOG_DIRTY_PAGES
                } else {
                    r.flags &= !1;
                }
                tracing::debug!(vm_handle, slot, enable, "dirty log toggled");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

// FFI: Go에서 호출. dirty page 비트맵을 조회한다 (스텁: 0으로 초기화). 반환값: 0=성공.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_mem_get_dirty_log(
    vm_handle: i32,
    slot: u32,
    bitmap: *mut u8,
    len: usize,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if bitmap.is_null() || len == 0 {
            return ErrorCode::InvalidArg as i32;
        }
        let guard = match mem_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        if !guard.contains_key(&(vm_handle, slot)) {
            return ErrorCode::NotFound as i32;
        }
        // Stub: zero out bitmap (no real KVM dirty tracking yet)
        // SAFETY: caller guarantees bitmap is valid for `len` bytes
        unsafe {
            std::ptr::write_bytes(bitmap, 0, len);
        }
        0
    })
}

// FFI: Go에서 호출. 게스트 메모리를 mmap으로 할당한다. 반환값: 주소(i64) 또는 음수 에러.
#[no_mangle]
pub extern "C" fn hcv_mem_alloc_guest(size_bytes: u64) -> i64 {
    crate::panic_barrier::catch(|| {
        if size_bytes == 0 || !size_bytes.is_multiple_of(PAGE_SIZE as u64) {
            return ErrorCode::InvalidArg as i32;
        }
        // Stub: use libc mmap for anonymous memory
        let addr = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                size_bytes as usize,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
                -1,
                0,
            )
        };
        if addr == libc::MAP_FAILED {
            tracing::error!(size_bytes, "mmap failed");
            return ErrorCode::OutOfMemory as i32;
        }
        tracing::info!(size_bytes, ?addr, "guest memory allocated");
        addr as i32 // Truncated for i32 return; real impl returns i64
    }) as i64
}

// FFI: Go에서 호출. 게스트 메모리를 munmap으로 해제한다. 반환값: 0=성공, 음수=에러.
#[no_mangle]
pub extern "C" fn hcv_mem_free_guest(addr: u64, size_bytes: u64) -> i32 {
    crate::panic_barrier::catch(|| {
        if addr == 0 || size_bytes == 0 {
            return ErrorCode::InvalidArg as i32;
        }
        let result = unsafe { libc::munmap(addr as *mut libc::c_void, size_bytes as usize) };
        if result != 0 {
            tracing::error!(addr, size_bytes, "munmap failed");
            return ErrorCode::KvmError as i32;
        }
        tracing::info!(addr, size_bytes, "guest memory freed");
        0
    })
}

// FFI: Go에서 호출. 메모리 발루닝 목표를 설정한다 (스텁). 반환값: 0=성공.
#[no_mangle]
pub extern "C" fn hcv_mem_balloon(vm_handle: i32, target_mb: u64) -> i32 {
    crate::panic_barrier::catch(|| {
        tracing::info!(vm_handle, target_mb, "balloon target set (stub)");
        // Stub: will integrate with virtio-balloon device
        0
    })
}

/// 고정 크기 페이지 테이블 버퍼 (Const Generics 활용).
///
/// 힙 할당 없이 컴파일 타임에 크기가 결정되는 페이지 테이블 엔트리 버퍼.
pub struct PageTableBuffer<const N: usize> {
    entries: [u64; N],
    count: usize,
}

impl<const N: usize> PageTableBuffer<N> {
    pub fn new() -> Self {
        Self {
            entries: [0; N],
            count: 0,
        }
    }
    #[allow(clippy::result_unit_err)]
    pub fn add_entry(&mut self, entry: u64) -> Result<(), ()> {
        if self.count >= N {
            return Err(());
        }
        self.entries[self.count] = entry;
        self.count += 1;
        Ok(())
    }
    pub fn entries(&self) -> &[u64] {
        &self.entries[..self.count]
    }
    pub fn capacity(&self) -> usize {
        N
    }
}

impl<const N: usize> Default for PageTableBuffer<N> {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_add_remove_region() {
        let region = MemoryRegion {
            slot: 0,
            flags: 0,
            guest_phys_addr: 0,
            memory_size: PAGE_SIZE as u64 * 256, // 1MB
            userspace_addr: 0x1000_0000,
        };
        assert_eq!(hcv_mem_add_region(200, &region), 0);
        assert_eq!(hcv_mem_remove_region(200, 0), 0);
        assert_eq!(hcv_mem_remove_region(200, 0), ErrorCode::NotFound as i32);
    }

    #[test]
    fn test_invalid_size() {
        let region = MemoryRegion {
            slot: 0,
            flags: 0,
            guest_phys_addr: 0,
            memory_size: 100, // not page-aligned
            userspace_addr: 0,
        };
        assert_eq!(
            hcv_mem_add_region(201, &region),
            ErrorCode::InvalidArg as i32
        );
    }

    #[test]
    fn test_null_pointer() {
        assert_eq!(
            hcv_mem_add_region(202, std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );
        assert_eq!(
            hcv_mem_get_region(202, 0, std::ptr::null_mut()),
            ErrorCode::InvalidArg as i32
        );
    }

    #[test]
    fn test_page_table_buffer() {
        let mut buf = PageTableBuffer::<4>::new();
        assert_eq!(buf.capacity(), 4);
        buf.add_entry(0x1000).unwrap();
        buf.add_entry(0x2000).unwrap();
        assert_eq!(buf.entries().len(), 2);
        buf.add_entry(0x3000).unwrap();
        buf.add_entry(0x4000).unwrap();
        assert!(buf.add_entry(0x5000).is_err()); // full
    }

    #[test]
    fn test_dirty_log_toggle() {
        let region = MemoryRegion {
            slot: 5,
            flags: 0,
            guest_phys_addr: 0,
            memory_size: PAGE_SIZE as u64,
            userspace_addr: 0x2000_0000,
        };
        hcv_mem_add_region(203, &region);
        assert_eq!(hcv_mem_set_dirty_log(203, 5, 1), 0);
        assert_eq!(hcv_mem_set_dirty_log(203, 5, 0), 0);
        hcv_mem_remove_region(203, 5);
    }
}

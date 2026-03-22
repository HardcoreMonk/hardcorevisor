//! # KVM 시스템 인터페이스 — Raw ioctl 래퍼
//!
//! ## 목적
//! Linux `/dev/kvm` ioctl을 안전한 Rust 추상화로 감싸는 저수준 모듈.
//! KVM API를 통해 VM 생성, 게스트 메모리 매핑, vCPU 생성 등을 수행한다.
//!
//! ## 아키텍처 위치
//! ```text
//! kvm_mgr.rs (인메모리 상태 머신, FFI 레이어)
//! kvm_sys.rs (이 모듈: 실제 /dev/kvm ioctl, 하이퍼바이저 연동)
//! ```
//! - `kvm_mgr.rs`와 분리: kvm_mgr는 상태 관리, kvm_sys는 하드웨어 제어
//!
//! ## 핵심 개념
//! - `KvmSystem`: `/dev/kvm` fd 핸들. API 버전(v12) 검증 및 확장 확인
//! - `KvmVm`: VM fd 핸들. 메모리 영역 설정, vCPU 생성, dirty log 관리
//! - `KvmVcpu`: vCPU fd 핸들. KVM_RUN 실행에 필요
//! - 게스트 메모리는 반드시 `mmap(MAP_PRIVATE | MAP_ANONYMOUS)`으로 할당해야 함
//!
//! ## 스레드 안전성
//! KVM fd는 커널 수준에서 스레드 안전하나, 이 모듈의 구조체는 `Send`만 구현한다.
//! 동시 접근이 필요하면 호출자가 동기화를 책임져야 한다.

use std::fs::File;
use std::os::unix::io::{AsRawFd, FromRawFd, RawFd};

// KVM ioctl 번호 (linux/kvm.h에서 정의)
const KVM_GET_API_VERSION: libc::c_ulong = 0xAE00;
const KVM_CREATE_VM: libc::c_ulong = 0xAE01;
const KVM_CHECK_EXTENSION: libc::c_ulong = 0xAE03;
const KVM_GET_VCPU_MMAP_SIZE: libc::c_ulong = 0xAE04;
const KVM_CREATE_VCPU: libc::c_ulong = 0xAE41;
const KVM_SET_USER_MEMORY_REGION: libc::c_ulong = 0x4020AE46;
const KVM_GET_DIRTY_LOG: libc::c_ulong = 0x4010AE42;
#[allow(dead_code)]
const KVM_RUN: libc::c_ulong = 0xAE80;

/// dirty page 로깅 활성화 플래그
const KVM_MEM_LOG_DIRTY_PAGES: u32 = 1;

// KVM 확장 기능 ID
const KVM_CAP_USER_MEMORY: libc::c_int = 3;
const KVM_CAP_NR_VCPUS: libc::c_int = 9;
const KVM_CAP_MAX_VCPUS: libc::c_int = 66;

/// KVM 사용자 공간 메모리 영역 (커널 `struct kvm_userspace_memory_region`과 일치).
///
/// 게스트 물리 주소를 호스트 사용자 공간 주소에 매핑한다.
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct KvmUserspaceMemoryRegion {
    /// 메모리 슬롯 번호 (0부터 시작)
    pub slot: u32,
    /// 플래그 (예: KVM_MEM_LOG_DIRTY_PAGES)
    pub flags: u32,
    /// 게스트 물리 주소
    pub guest_phys_addr: u64,
    /// 메모리 영역 크기 (바이트)
    pub memory_size: u64,
    /// 호스트 사용자 공간 주소 (mmap으로 할당)
    pub userspace_addr: u64,
}

/// KVM dirty log 요청 (커널 `struct kvm_dirty_log`과 일치).
/// 라이브 마이그레이션 시 변경된 페이지를 추적하는 데 사용된다.
#[repr(C)]
pub struct KvmDirtyLog {
    pub slot: u32,
    pub _padding: u32,
    pub dirty_bitmap: *mut u64,
}

/// KVM 작업 에러 타입
#[derive(Debug, thiserror::Error)]
pub enum KvmSysError {
    #[error("failed to open /dev/kvm: {0}")]
    OpenFailed(std::io::Error),
    #[error("KVM API version mismatch: expected 12, got {0}")]
    ApiVersion(i32),
    #[error("KVM ioctl failed ({op}): {source}")]
    Ioctl {
        op: &'static str,
        source: std::io::Error,
    },
    #[error("KVM extension not supported: {0}")]
    ExtensionMissing(&'static str),
    #[error("mmap failed: {0}")]
    MmapFailed(std::io::Error),
}

/// `/dev/kvm` 시스템 fd 핸들.
/// KVM 하이퍼바이저와의 최상위 인터페이스로, VM 생성 및 기능 조회를 담당한다.
pub struct KvmSystem {
    fd: File,
}

/// KVM VM fd 핸들.
/// 메모리 영역 설정, vCPU 생성, dirty log 관리 등 VM 수준 작업을 수행한다.
pub struct KvmVm {
    fd: File,
}

/// KVM vCPU fd 핸들.
/// KVM_RUN ioctl로 게스트 코드를 실행하는 데 사용된다.
pub struct KvmVcpu {
    fd: File,
    pub kvm_run_mmap_size: usize,
}

// ── Unsafe ioctl 헬퍼 ──────────────────────────────

/// raw ioctl 시스템 호출 래퍼.
/// # Safety
/// `fd`는 유효한 파일 디스크립터여야 하고, `arg`는 `request`에 맞는 유효한 값이어야 한다.
unsafe fn ioctl_raw(fd: RawFd, request: libc::c_ulong, arg: libc::c_ulong) -> i32 {
    libc::ioctl(fd, request, arg)
}

/// ioctl을 호출하고 에러 시 `KvmSysError::Ioctl`을 반환하는 안전한 래퍼.
fn ioctl(
    fd: RawFd,
    request: libc::c_ulong,
    arg: libc::c_ulong,
    op: &'static str,
) -> Result<i32, KvmSysError> {
    // SAFETY: fd는 KvmSystem/KvmVm/KvmVcpu에서 관리하는 유효한 fd이고,
    //         arg는 각 ioctl 명세에 맞는 값이 전달됨
    let ret = unsafe { ioctl_raw(fd, request, arg) };
    if ret < 0 {
        return Err(KvmSysError::Ioctl {
            op,
            source: std::io::Error::last_os_error(),
        });
    }
    Ok(ret)
}

// ── KvmSystem ────────────────────────────────────────

impl KvmSystem {
    /// `/dev/kvm`을 열고 API 버전을 검증한다.
    ///
    /// KVM API 버전이 12(안정 ABI)인지 확인하고,
    /// `KVM_CAP_USER_MEMORY` 확장 기능을 필수로 요구한다.
    ///
    /// # 반환값
    /// - `Ok(KvmSystem)`: 성공 시 KVM 시스템 핸들
    /// - `Err(KvmSysError::OpenFailed)`: `/dev/kvm`을 열 수 없음
    /// - `Err(KvmSysError::ApiVersion)`: API 버전 불일치
    pub fn open() -> Result<Self, KvmSysError> {
        let fd = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/kvm")
            .map_err(KvmSysError::OpenFailed)?;

        let sys = KvmSystem { fd };

        // API 버전이 12(안정 ABI)인지 검증 — KVM은 이 버전을 영구 유지
        let version = sys.api_version()?;
        if version != 12 {
            return Err(KvmSysError::ApiVersion(version));
        }

        // 필수 확장 기능 확인 (사용자 공간 메모리 지원)
        if sys.check_extension(KVM_CAP_USER_MEMORY)? == 0 {
            return Err(KvmSysError::ExtensionMissing("KVM_CAP_USER_MEMORY"));
        }

        tracing::info!(version, "KVM system opened");
        Ok(sys)
    }

    /// KVM API 버전을 조회한다 (정상이면 12).
    pub fn api_version(&self) -> Result<i32, KvmSysError> {
        ioctl(
            self.fd.as_raw_fd(),
            KVM_GET_API_VERSION,
            0,
            "KVM_GET_API_VERSION",
        )
    }

    /// KVM 확장 기능이 지원되는지 확인한다.
    ///
    /// # 반환값
    /// - `Ok(0)`: 미지원
    /// - `Ok(n)`: 지원됨 (n은 확장별 의미가 다름)
    pub fn check_extension(&self, extension: libc::c_int) -> Result<i32, KvmSysError> {
        ioctl(
            self.fd.as_raw_fd(),
            KVM_CHECK_EXTENSION,
            extension as libc::c_ulong,
            "KVM_CHECK_EXTENSION",
        )
    }

    /// 지원되는 최대 vCPU 수를 조회한다.
    /// KVM_CAP_MAX_VCPUS를 먼저 확인하고, 미지원 시 KVM_CAP_NR_VCPUS로 폴백한다.
    pub fn max_vcpus(&self) -> Result<i32, KvmSysError> {
        let max = self.check_extension(KVM_CAP_MAX_VCPUS)?;
        if max == 0 {
            // Fallback to KVM_CAP_NR_VCPUS
            self.check_extension(KVM_CAP_NR_VCPUS)
        } else {
            Ok(max)
        }
    }

    /// vCPU fd의 `kvm_run` 구조체에 필요한 mmap 크기를 조회한다.
    /// vCPU 생성 후 이 크기만큼 mmap하여 KVM_RUN 결과를 읽는다.
    pub fn vcpu_mmap_size(&self) -> Result<usize, KvmSysError> {
        let size = ioctl(
            self.fd.as_raw_fd(),
            KVM_GET_VCPU_MMAP_SIZE,
            0,
            "KVM_GET_VCPU_MMAP_SIZE",
        )?;
        Ok(size as usize)
    }

    /// 새 VM을 생성한다. `KvmVm` 핸들을 반환한다.
    pub fn create_vm(&self) -> Result<KvmVm, KvmSysError> {
        let vm_fd = ioctl(self.fd.as_raw_fd(), KVM_CREATE_VM, 0, "KVM_CREATE_VM")?;
        // SAFETY: KVM_CREATE_VM ioctl은 우리가 소유하는 새로운 유효한 fd를 반환한다
        let file = unsafe { File::from_raw_fd(vm_fd) };
        tracing::debug!(vm_fd, "KVM VM created");
        Ok(KvmVm { fd: file })
    }
}

// ── KvmVm ────────────────────────────────────────────

impl KvmVm {
    /// 게스트 메모리 영역을 설정한다.
    ///
    /// 호스트 mmap 메모리를 게스트 물리 주소 공간에 매핑한다.
    /// 메모리는 반드시 페이지 정렬(4KB)되어야 하며,
    /// `mmap(MAP_PRIVATE | MAP_ANONYMOUS)`로 할당해야 한다.
    pub fn set_user_memory_region(
        &self,
        region: &KvmUserspaceMemoryRegion,
    ) -> Result<(), KvmSysError> {
        ioctl(
            self.fd.as_raw_fd(),
            KVM_SET_USER_MEMORY_REGION,
            region as *const KvmUserspaceMemoryRegion as libc::c_ulong,
            "KVM_SET_USER_MEMORY_REGION",
        )?;
        tracing::debug!(
            slot = region.slot,
            guest_phys = format!("{:#x}", region.guest_phys_addr),
            size = region.memory_size,
            "memory region set"
        );
        Ok(())
    }

    /// 메모리 영역에 대해 dirty page 로깅을 활성화한다.
    /// 라이브 마이그레이션 시 변경된 페이지를 추적하는 데 사용된다.
    pub fn enable_dirty_log(
        &self,
        slot: u32,
        guest_phys_addr: u64,
        memory_size: u64,
        userspace_addr: u64,
    ) -> Result<(), KvmSysError> {
        let region = KvmUserspaceMemoryRegion {
            slot,
            flags: KVM_MEM_LOG_DIRTY_PAGES,
            guest_phys_addr,
            memory_size,
            userspace_addr,
        };
        self.set_user_memory_region(&region)
    }

    /// 슬롯의 dirty page 비트맵을 조회한다.
    /// 각 비트가 하나의 4KB 페이지를 나타내는 `Vec<u64>` 비트맵을 반환한다.
    pub fn get_dirty_log(&self, slot: u32, memory_size: u64) -> Result<Vec<u64>, KvmSysError> {
        let page_count = (memory_size as usize).div_ceil(4096);
        let bitmap_size = page_count.div_ceil(64);
        let mut bitmap = vec![0u64; bitmap_size];

        let dirty_log = KvmDirtyLog {
            slot,
            _padding: 0,
            dirty_bitmap: bitmap.as_mut_ptr(),
        };

        ioctl(
            self.as_raw_fd(),
            KVM_GET_DIRTY_LOG,
            &dirty_log as *const _ as libc::c_ulong,
            "KVM_GET_DIRTY_LOG",
        )?;

        Ok(bitmap)
    }

    /// 비트맵에서 dirty page 수를 계산한다.
    /// `popcount`를 사용하여 설정된 비트(변경된 페이지)를 센다.
    pub fn count_dirty_pages(bitmap: &[u64]) -> u64 {
        bitmap.iter().map(|w| w.count_ones() as u64).sum()
    }

    /// vCPU를 생성한다.
    ///
    /// # 매개변수
    /// - `vcpu_id`: vCPU 번호 (0부터 시작)
    /// - `mmap_size`: `KvmSystem::vcpu_mmap_size()`에서 얻은 kvm_run mmap 크기
    pub fn create_vcpu(&self, vcpu_id: u32, mmap_size: usize) -> Result<KvmVcpu, KvmSysError> {
        let vcpu_fd = ioctl(
            self.fd.as_raw_fd(),
            KVM_CREATE_VCPU,
            vcpu_id as libc::c_ulong,
            "KVM_CREATE_VCPU",
        )?;
        // SAFETY: KVM_CREATE_VCPU ioctl은 우리가 소유하는 새로운 유효한 fd를 반환한다
        let file = unsafe { File::from_raw_fd(vcpu_fd) };
        tracing::debug!(vcpu_id, vcpu_fd, "vCPU created");
        Ok(KvmVcpu {
            fd: file,
            kvm_run_mmap_size: mmap_size,
        })
    }

    /// 고급 작업을 위한 raw fd를 반환한다.
    pub fn as_raw_fd(&self) -> RawFd {
        self.fd.as_raw_fd()
    }
}

// ── KvmVcpu ──────────────────────────────────────────

impl KvmVcpu {
    /// 고급 작업을 위한 raw fd를 반환한다 (KVM_RUN mmap 등에 사용).
    pub fn as_raw_fd(&self) -> RawFd {
        self.fd.as_raw_fd()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_open_kvm() {
        // This test requires /dev/kvm access
        let result = KvmSystem::open();
        match result {
            Ok(sys) => {
                let ver = sys.api_version().unwrap();
                assert_eq!(ver, 12);
                let max = sys.max_vcpus().unwrap();
                assert!(max > 0);
                let mmap_size = sys.vcpu_mmap_size().unwrap();
                assert!(mmap_size > 0);
                tracing::info!(max_vcpus = max, mmap_size, "KVM system info");
            }
            Err(KvmSysError::OpenFailed(_)) => {
                // /dev/kvm not available — skip
                eprintln!("SKIP: /dev/kvm not available");
            }
            Err(e) => panic!("unexpected error: {e}"),
        }
    }

    #[test]
    fn test_create_vm() {
        let sys = match KvmSystem::open() {
            Ok(s) => s,
            Err(KvmSysError::OpenFailed(_)) => {
                eprintln!("SKIP: /dev/kvm not available");
                return;
            }
            Err(e) => panic!("unexpected error: {e}"),
        };

        let vm = sys.create_vm().unwrap();
        assert!(vm.as_raw_fd() > 0);

        // Allocate page-aligned guest memory via mmap (4KB)
        let mem_size: usize = 4096;
        let guest_mem = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                mem_size,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
                -1,
                0,
            )
        };
        assert_ne!(guest_mem, libc::MAP_FAILED);

        let region = KvmUserspaceMemoryRegion {
            slot: 0,
            flags: 0,
            guest_phys_addr: 0,
            memory_size: mem_size as u64,
            userspace_addr: guest_mem as u64,
        };
        vm.set_user_memory_region(&region).unwrap();

        // Create a vCPU
        let mmap_size = sys.vcpu_mmap_size().unwrap();
        let vcpu = vm.create_vcpu(0, mmap_size).unwrap();
        assert!(vcpu.as_raw_fd() > 0);

        // Cleanup
        unsafe {
            libc::munmap(guest_mem, mem_size);
        }
    }

    #[test]
    fn test_dirty_log() {
        let sys = match KvmSystem::open() {
            Ok(s) => s,
            Err(KvmSysError::OpenFailed(_)) => {
                eprintln!("SKIP: /dev/kvm not available");
                return;
            }
            Err(e) => panic!("unexpected error: {e}"),
        };
        let vm = sys.create_vm().unwrap();

        let mem_size: usize = 4096 * 4; // 4 pages
        let guest_mem = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                mem_size,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
                -1,
                0,
            )
        };
        assert_ne!(guest_mem, libc::MAP_FAILED);

        // Enable dirty logging
        vm.enable_dirty_log(0, 0, mem_size as u64, guest_mem as u64)
            .unwrap();

        // Write to page 0 and 2
        unsafe {
            *(guest_mem as *mut u8) = 0x42;
            *((guest_mem as *mut u8).add(4096 * 2)) = 0x43;
        }

        // Note: dirty log only works after KVM_RUN, so just verify the API works
        let bitmap = vm.get_dirty_log(0, mem_size as u64).unwrap();
        assert!(!bitmap.is_empty());

        unsafe {
            libc::munmap(guest_mem, mem_size);
        }
    }
}

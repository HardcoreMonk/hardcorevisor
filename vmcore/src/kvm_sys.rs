//! # KVM System Interface — Raw ioctl wrappers
//!
//! Low-level /dev/kvm ioctl wrappers. Only compiled with `real-kvm` feature.
//! Provides safe Rust abstractions over the KVM API.

use std::fs::File;
use std::os::unix::io::{AsRawFd, FromRawFd, RawFd};

// KVM ioctl numbers (from linux/kvm.h)
const KVM_GET_API_VERSION: libc::c_ulong = 0xAE00;
const KVM_CREATE_VM: libc::c_ulong = 0xAE01;
const KVM_CHECK_EXTENSION: libc::c_ulong = 0xAE03;
const KVM_GET_VCPU_MMAP_SIZE: libc::c_ulong = 0xAE04;
const KVM_CREATE_VCPU: libc::c_ulong = 0xAE41;
const KVM_SET_USER_MEMORY_REGION: libc::c_ulong = 0x4020AE46;
#[allow(dead_code)]
const KVM_RUN: libc::c_ulong = 0xAE80;

// KVM extension IDs
const KVM_CAP_USER_MEMORY: libc::c_int = 3;
const KVM_CAP_NR_VCPUS: libc::c_int = 9;
const KVM_CAP_MAX_VCPUS: libc::c_int = 66;

/// KVM userspace memory region (matches struct kvm_userspace_memory_region)
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct KvmUserspaceMemoryRegion {
    pub slot: u32,
    pub flags: u32,
    pub guest_phys_addr: u64,
    pub memory_size: u64,
    pub userspace_addr: u64,
}

/// Error type for KVM operations
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

/// Handle to /dev/kvm system fd
pub struct KvmSystem {
    fd: File,
}

/// Handle to a KVM VM fd
pub struct KvmVm {
    fd: File,
}

/// Handle to a KVM vCPU fd
pub struct KvmVcpu {
    fd: File,
    pub kvm_run_mmap_size: usize,
}

// ── Unsafe ioctl helper ──────────────────────────────

unsafe fn ioctl_raw(fd: RawFd, request: libc::c_ulong, arg: libc::c_ulong) -> i32 {
    libc::ioctl(fd, request, arg)
}

fn ioctl(
    fd: RawFd,
    request: libc::c_ulong,
    arg: libc::c_ulong,
    op: &'static str,
) -> Result<i32, KvmSysError> {
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
    /// Open /dev/kvm and verify API version.
    pub fn open() -> Result<Self, KvmSysError> {
        let fd = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/kvm")
            .map_err(KvmSysError::OpenFailed)?;

        let sys = KvmSystem { fd };

        // Verify API version is 12 (stable ABI)
        let version = sys.api_version()?;
        if version != 12 {
            return Err(KvmSysError::ApiVersion(version));
        }

        // Check required extensions
        if sys.check_extension(KVM_CAP_USER_MEMORY)? == 0 {
            return Err(KvmSysError::ExtensionMissing("KVM_CAP_USER_MEMORY"));
        }

        tracing::info!(version, "KVM system opened");
        Ok(sys)
    }

    /// Get KVM API version (should be 12).
    pub fn api_version(&self) -> Result<i32, KvmSysError> {
        ioctl(
            self.fd.as_raw_fd(),
            KVM_GET_API_VERSION,
            0,
            "KVM_GET_API_VERSION",
        )
    }

    /// Check if a KVM extension is supported.
    pub fn check_extension(&self, extension: libc::c_int) -> Result<i32, KvmSysError> {
        ioctl(
            self.fd.as_raw_fd(),
            KVM_CHECK_EXTENSION,
            extension as libc::c_ulong,
            "KVM_CHECK_EXTENSION",
        )
    }

    /// Get the max number of vCPUs supported.
    pub fn max_vcpus(&self) -> Result<i32, KvmSysError> {
        let max = self.check_extension(KVM_CAP_MAX_VCPUS)?;
        if max == 0 {
            // Fallback to KVM_CAP_NR_VCPUS
            self.check_extension(KVM_CAP_NR_VCPUS)
        } else {
            Ok(max)
        }
    }

    /// Get the mmap size for vCPU fd's kvm_run struct.
    pub fn vcpu_mmap_size(&self) -> Result<usize, KvmSysError> {
        let size = ioctl(
            self.fd.as_raw_fd(),
            KVM_GET_VCPU_MMAP_SIZE,
            0,
            "KVM_GET_VCPU_MMAP_SIZE",
        )?;
        Ok(size as usize)
    }

    /// Create a new VM. Returns a KvmVm handle.
    pub fn create_vm(&self) -> Result<KvmVm, KvmSysError> {
        let vm_fd = ioctl(self.fd.as_raw_fd(), KVM_CREATE_VM, 0, "KVM_CREATE_VM")?;
        // SAFETY: KVM_CREATE_VM returns a new valid fd owned by us
        let file = unsafe { File::from_raw_fd(vm_fd) };
        tracing::debug!(vm_fd, "KVM VM created");
        Ok(KvmVm { fd: file })
    }
}

// ── KvmVm ────────────────────────────────────────────

impl KvmVm {
    /// Set a guest memory region.
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

    /// Create a vCPU.
    pub fn create_vcpu(&self, vcpu_id: u32, mmap_size: usize) -> Result<KvmVcpu, KvmSysError> {
        let vcpu_fd = ioctl(
            self.fd.as_raw_fd(),
            KVM_CREATE_VCPU,
            vcpu_id as libc::c_ulong,
            "KVM_CREATE_VCPU",
        )?;
        let file = unsafe { File::from_raw_fd(vcpu_fd) };
        tracing::debug!(vcpu_id, vcpu_fd, "vCPU created");
        Ok(KvmVcpu {
            fd: file,
            kvm_run_mmap_size: mmap_size,
        })
    }

    /// Raw fd for advanced operations.
    pub fn as_raw_fd(&self) -> RawFd {
        self.fd.as_raw_fd()
    }
}

// ── KvmVcpu ──────────────────────────────────────────

impl KvmVcpu {
    /// Raw fd for advanced operations.
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
}

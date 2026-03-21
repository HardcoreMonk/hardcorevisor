//! # TAP Device Interface
//!
//! Provides a TAP device abstraction for virtio-net networking.
//! Supports open/close, read/write, and FFI functions for Go integration.

use std::os::unix::io::{AsRawFd, RawFd};

// ── Constants ────────────────────────────────────────────

const TUNSETIFF: libc::c_ulong = 0x400454ca;
const IFF_TAP: libc::c_short = 0x0002;
const IFF_NO_PI: libc::c_short = 0x1000;

// ── Error type ───────────────────────────────────────────

/// Errors from TAP device operations
#[derive(Debug)]
pub enum TapError {
    /// Failed to open /dev/net/tun
    OpenFailed(std::io::Error),
    /// TUNSETIFF ioctl failed
    IoctlFailed(std::io::Error),
    /// Read from TAP device failed
    ReadFailed(std::io::Error),
    /// Write to TAP device failed
    WriteFailed(std::io::Error),
}

impl std::fmt::Display for TapError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            TapError::OpenFailed(e) => write!(f, "failed to open /dev/net/tun: {e}"),
            TapError::IoctlFailed(e) => write!(f, "TUNSETIFF ioctl failed: {e}"),
            TapError::ReadFailed(e) => write!(f, "TAP read failed: {e}"),
            TapError::WriteFailed(e) => write!(f, "TAP write failed: {e}"),
        }
    }
}

impl std::error::Error for TapError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            TapError::OpenFailed(e)
            | TapError::IoctlFailed(e)
            | TapError::ReadFailed(e)
            | TapError::WriteFailed(e) => Some(e),
        }
    }
}

// ── TAP Device ───────────────────────────────────────────

/// A Linux TAP device handle.
pub struct TapDevice {
    fd: RawFd,
    name: String,
}

impl TapDevice {
    /// Open a TAP device. If name is empty, kernel assigns one (e.g., tap0).
    pub fn open(name: &str) -> Result<Self, TapError> {
        let fd = unsafe { libc::open(c"/dev/net/tun".as_ptr(), libc::O_RDWR) };
        if fd < 0 {
            return Err(TapError::OpenFailed(std::io::Error::last_os_error()));
        }

        // Set up ifreq for TUNSETIFF
        let mut ifr: libc::ifreq = unsafe { std::mem::zeroed() };
        ifr.ifr_ifru.ifru_flags = IFF_TAP | IFF_NO_PI;

        if !name.is_empty() {
            let bytes = name.as_bytes();
            let len = std::cmp::min(bytes.len(), 15);
            unsafe {
                std::ptr::copy_nonoverlapping(
                    bytes.as_ptr(),
                    ifr.ifr_name.as_mut_ptr() as *mut u8,
                    len,
                );
            }
        }

        let ret = unsafe { libc::ioctl(fd, TUNSETIFF, &ifr) };
        if ret < 0 {
            let err = std::io::Error::last_os_error();
            unsafe { libc::close(fd) };
            return Err(TapError::IoctlFailed(err));
        }

        // Extract assigned name
        let assigned_name = unsafe {
            std::ffi::CStr::from_ptr(ifr.ifr_name.as_ptr())
                .to_string_lossy()
                .into_owned()
        };

        Ok(TapDevice {
            fd,
            name: assigned_name,
        })
    }

    /// Get the TAP device name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Read a packet from the TAP device (non-blocking capable).
    pub fn read(&self, buf: &mut [u8]) -> Result<usize, TapError> {
        let n = unsafe { libc::read(self.fd, buf.as_mut_ptr() as *mut _, buf.len()) };
        if n < 0 {
            return Err(TapError::ReadFailed(std::io::Error::last_os_error()));
        }
        Ok(n as usize)
    }

    /// Write a packet to the TAP device.
    pub fn write(&self, buf: &[u8]) -> Result<usize, TapError> {
        let n = unsafe { libc::write(self.fd, buf.as_ptr() as *const _, buf.len()) };
        if n < 0 {
            return Err(TapError::WriteFailed(std::io::Error::last_os_error()));
        }
        Ok(n as usize)
    }
}

impl Drop for TapDevice {
    fn drop(&mut self) {
        unsafe {
            libc::close(self.fd);
        }
    }
}

impl AsRawFd for TapDevice {
    fn as_raw_fd(&self) -> RawFd {
        self.fd
    }
}

// ═══════════════════════════════════════════════════════════
// FFI
// ═══════════════════════════════════════════════════════════

/// Open a TAP device. Returns a heap-allocated TapDevice pointer, or null on error.
/// If `name` is null, kernel assigns a name.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_tap_open(name: *const libc::c_char) -> *mut TapDevice {
    let dev_name = if name.is_null() {
        String::new()
    } else {
        match unsafe { std::ffi::CStr::from_ptr(name) }.to_str() {
            Ok(s) => s.to_owned(),
            Err(_) => return std::ptr::null_mut(),
        }
    };

    match TapDevice::open(&dev_name) {
        Ok(dev) => {
            tracing::info!(name = %dev.name(), "TAP device opened");
            Box::into_raw(Box::new(dev))
        }
        Err(e) => {
            tracing::error!(%e, "failed to open TAP device");
            std::ptr::null_mut()
        }
    }
}

/// Close and free a TAP device.
/// # Safety
/// `dev` must be a pointer returned by `hcv_tap_open` (or null, which is a no-op).
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_tap_close(dev: *mut TapDevice) {
    if dev.is_null() {
        return;
    }
    let dev = unsafe { Box::from_raw(dev) };
    tracing::info!(name = %dev.name(), "TAP device closed");
    drop(dev);
}

/// Read a packet from the TAP device.
/// Returns bytes read on success, or a negative value on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_tap_read(dev: *mut TapDevice, buf: *mut u8, len: u32) -> i32 {
    if dev.is_null() || buf.is_null() || len == 0 {
        return crate::panic_barrier::ErrorCode::InvalidArg as i32;
    }
    let dev = unsafe { &*dev };
    let slice = unsafe { std::slice::from_raw_parts_mut(buf, len as usize) };
    match dev.read(slice) {
        Ok(n) => n as i32,
        Err(_) => crate::panic_barrier::ErrorCode::KvmError as i32,
    }
}

/// Write a packet to the TAP device.
/// Returns bytes written on success, or a negative value on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_tap_write(dev: *mut TapDevice, buf: *const u8, len: u32) -> i32 {
    if dev.is_null() || buf.is_null() || len == 0 {
        return crate::panic_barrier::ErrorCode::InvalidArg as i32;
    }
    let dev = unsafe { &*dev };
    let slice = unsafe { std::slice::from_raw_parts(buf, len as usize) };
    match dev.write(slice) {
        Ok(n) => n as i32,
        Err(_) => crate::panic_barrier::ErrorCode::KvmError as i32,
    }
}

/// Get the TAP device name. Writes a null-terminated string to `out_buf`.
/// Returns the length of the name (excluding null terminator), or negative on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_tap_name(dev: *const TapDevice, out_buf: *mut u8, buf_len: u32) -> i32 {
    if dev.is_null() || out_buf.is_null() || buf_len == 0 {
        return crate::panic_barrier::ErrorCode::InvalidArg as i32;
    }
    let dev = unsafe { &*dev };
    let name_bytes = dev.name().as_bytes();
    let copy_len = std::cmp::min(name_bytes.len(), (buf_len - 1) as usize);
    unsafe {
        std::ptr::copy_nonoverlapping(name_bytes.as_ptr(), out_buf, copy_len);
        *out_buf.add(copy_len) = 0; // null terminate
    }
    copy_len as i32
}

// ═══════════════════════════════════════════════════════════
// Tests
// ═══════════════════════════════════════════════════════════

#[cfg(test)]
mod tests {
    use super::*;

    /// Check that /dev/net/tun is available (skip test if not).
    fn tun_available() -> bool {
        std::path::Path::new("/dev/net/tun").exists()
    }

    #[test]
    fn test_tap_error_without_permissions() {
        if !tun_available() {
            eprintln!("SKIP: /dev/net/tun not available");
            return;
        }

        // Opening a TAP device typically requires root or CAP_NET_ADMIN.
        // In unprivileged CI, we expect an error — not a panic.
        match TapDevice::open("") {
            Ok(dev) => {
                // If we're running as root, this might succeed
                println!("TAP device opened (running as root?): {}", dev.name());
            }
            Err(TapError::OpenFailed(e)) => {
                println!("Expected open error (no permissions): {e}");
            }
            Err(TapError::IoctlFailed(e)) => {
                println!("Expected ioctl error (no permissions): {e}");
            }
            Err(e) => {
                // Any TapError is acceptable — we just shouldn't panic
                println!("Got TAP error (acceptable): {e}");
            }
        }
    }

    #[test]
    fn test_tap_error_display() {
        let err = TapError::OpenFailed(std::io::Error::from_raw_os_error(13));
        let msg = format!("{err}");
        assert!(msg.contains("failed to open /dev/net/tun"));

        let err = TapError::IoctlFailed(std::io::Error::from_raw_os_error(1));
        let msg = format!("{err}");
        assert!(msg.contains("TUNSETIFF ioctl failed"));

        let err = TapError::ReadFailed(std::io::Error::from_raw_os_error(11));
        let msg = format!("{err}");
        assert!(msg.contains("TAP read failed"));

        let err = TapError::WriteFailed(std::io::Error::from_raw_os_error(5));
        let msg = format!("{err}");
        assert!(msg.contains("TAP write failed"));
    }

    #[test]
    fn test_ffi_null_safety() {
        // All FFI functions should handle null pointers gracefully
        hcv_tap_close(std::ptr::null_mut());

        assert_eq!(
            hcv_tap_read(std::ptr::null_mut(), std::ptr::null_mut(), 0),
            crate::panic_barrier::ErrorCode::InvalidArg as i32
        );
        assert_eq!(
            hcv_tap_write(std::ptr::null_mut(), std::ptr::null_mut(), 0),
            crate::panic_barrier::ErrorCode::InvalidArg as i32
        );
        assert_eq!(
            hcv_tap_name(std::ptr::null(), std::ptr::null_mut(), 0),
            crate::panic_barrier::ErrorCode::InvalidArg as i32
        );
    }
}

//! # io_uring Async I/O Engine
//!
//! Linux 6.x io_uring 기반 비동기 디스크 I/O 엔진.
//! virtio-blk 디스크 요청을 io_uring SQ(Submission Queue)에 제출하고
//! CQ(Completion Queue)에서 완료를 수집하여 zero-copy 고성능 I/O를 제공한다.
//!
//! ## Architecture
//! ```text
//! virtio_blk (게스트 요청)
//!     │
//!     ▼
//! IoEngine (이 모듈)
//!     │
//!     ├── submit_read()   → SQE (IORING_OP_READ)
//!     ├── submit_write()  → SQE (IORING_OP_WRITE)
//!     ├── submit_flush()  → SQE (IORING_OP_FSYNC)
//!     │
//!     ▼
//! io_uring (커널)
//!     │
//!     ▼
//! CQ → poll_completions() → IoCompletion { user_data, result }
//! ```
//!
//! ## FFI Convention
//! - `hcv_io_engine_create()` → `*mut IoEngineHandle`
//! - `hcv_io_engine_submit_*()` → i32 (0=success)
//! - `hcv_io_engine_poll()` → i32 (completed count)
//! - `hcv_io_engine_destroy()` → void

use crate::panic_barrier::ErrorCode;
use std::collections::HashMap;
use std::fs::{File, OpenOptions};
use std::os::unix::io::AsRawFd;
use std::sync::atomic::{AtomicU64, Ordering};

/// I/O operation type
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum IoOpType {
    #[default]
    Read = 0,
    Write = 1,
    Flush = 2,
}

/// I/O completion result returned to caller
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct IoCompletion {
    /// User-provided request ID (passed in submit)
    pub user_data: u64,
    /// Bytes transferred (>= 0) or negative errno on error
    pub result: i32,
    /// Operation type
    pub op: IoOpType,
}

/// io_uring I/O engine errors
#[derive(Debug, thiserror::Error)]
pub enum IoEngineError {
    #[error("io_uring setup failed: {0}")]
    Setup(std::io::Error),
    #[error("file open failed: {0}")]
    FileOpen(std::io::Error),
    #[error("submit failed: {0}")]
    Submit(std::io::Error),
    #[error("invalid argument: {0}")]
    InvalidArg(String),
    #[error("ring full — no SQE available")]
    RingFull,
    #[error("file not registered: fd_index={0}")]
    FileNotRegistered(u32),
}

/// Registered backing file for I/O
#[allow(dead_code)]
struct BackingFile {
    file: File,
    path: String,
    size_bytes: u64,
}

/// io_uring-based async I/O engine
pub struct IoEngine {
    ring: io_uring::IoUring,
    files: HashMap<u32, BackingFile>,
    next_file_id: u32,
    inflight: u64,
    submitted_total: AtomicU64,
    completed_total: AtomicU64,
}

/// Opaque handle for FFI
pub struct IoEngineHandle {
    engine: IoEngine,
}

/// I/O engine statistics
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct IoEngineStats {
    pub submitted: u64,
    pub completed: u64,
    pub inflight: u64,
    pub ring_capacity: u32,
    pub registered_files: u32,
}

impl IoEngine {
    /// Create a new io_uring engine with the given queue depth.
    pub fn new(queue_depth: u32) -> Result<Self, IoEngineError> {
        let ring = io_uring::IoUring::builder()
            .build(queue_depth)
            .map_err(IoEngineError::Setup)?;

        tracing::info!(queue_depth, "io_uring engine created");

        Ok(IoEngine {
            ring,
            files: HashMap::new(),
            next_file_id: 0,
            inflight: 0,
            submitted_total: AtomicU64::new(0),
            completed_total: AtomicU64::new(0),
        })
    }

    /// Register a backing file for I/O. Returns a file index.
    pub fn register_file(&mut self, path: &str, read_only: bool) -> Result<u32, IoEngineError> {
        let file = if read_only {
            File::open(path).map_err(IoEngineError::FileOpen)?
        } else {
            OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .truncate(false)
                .open(path)
                .map_err(IoEngineError::FileOpen)?
        };

        let metadata = file.metadata().map_err(IoEngineError::FileOpen)?;
        let size = metadata.len();
        let id = self.next_file_id;
        self.next_file_id += 1;

        tracing::info!(id, path, size, read_only, "backing file registered");

        self.files.insert(
            id,
            BackingFile {
                file,
                path: path.to_string(),
                size_bytes: size,
            },
        );

        Ok(id)
    }

    /// Unregister a backing file.
    pub fn unregister_file(&mut self, fd_index: u32) -> Result<(), IoEngineError> {
        self.files
            .remove(&fd_index)
            .ok_or(IoEngineError::FileNotRegistered(fd_index))?;
        Ok(())
    }

    /// Submit an async read operation.
    ///
    /// - `fd_index`: registered file index
    /// - `buf`: buffer to read into (must remain valid until completion)
    /// - `offset`: file offset in bytes
    /// - `user_data`: caller-provided ID returned in completion
    ///
    /// # Safety
    /// `buf` must point to valid memory of at least `len` bytes and must
    /// remain valid until the corresponding completion is polled.
    pub unsafe fn submit_read(
        &mut self,
        fd_index: u32,
        buf: *mut u8,
        len: u32,
        offset: u64,
        user_data: u64,
    ) -> Result<(), IoEngineError> {
        let backing = self
            .files
            .get(&fd_index)
            .ok_or(IoEngineError::FileNotRegistered(fd_index))?;
        let fd = io_uring::types::Fd(backing.file.as_raw_fd());

        let read_op = io_uring::opcode::Read::new(fd, buf, len)
            .offset(offset)
            .build()
            .user_data(user_data);

        // Push SQE
        {
            let mut sq = self.ring.submission();
            sq.push(&read_op).map_err(|_| IoEngineError::RingFull)?;
        }

        self.ring.submit().map_err(IoEngineError::Submit)?;
        self.inflight += 1;
        self.submitted_total.fetch_add(1, Ordering::Relaxed);

        Ok(())
    }

    /// Submit an async write operation.
    ///
    /// # Safety
    /// `buf` must point to valid memory of at least `len` bytes and must
    /// remain valid until the corresponding completion is polled.
    pub unsafe fn submit_write(
        &mut self,
        fd_index: u32,
        buf: *const u8,
        len: u32,
        offset: u64,
        user_data: u64,
    ) -> Result<(), IoEngineError> {
        let backing = self
            .files
            .get(&fd_index)
            .ok_or(IoEngineError::FileNotRegistered(fd_index))?;
        let fd = io_uring::types::Fd(backing.file.as_raw_fd());

        let write_op = io_uring::opcode::Write::new(fd, buf, len)
            .offset(offset)
            .build()
            .user_data(user_data);

        {
            let mut sq = self.ring.submission();
            sq.push(&write_op).map_err(|_| IoEngineError::RingFull)?;
        }

        self.ring.submit().map_err(IoEngineError::Submit)?;
        self.inflight += 1;
        self.submitted_total.fetch_add(1, Ordering::Relaxed);

        Ok(())
    }

    /// Submit an async fsync (flush) operation.
    pub fn submit_flush(&mut self, fd_index: u32, user_data: u64) -> Result<(), IoEngineError> {
        let backing = self
            .files
            .get(&fd_index)
            .ok_or(IoEngineError::FileNotRegistered(fd_index))?;
        let fd = io_uring::types::Fd(backing.file.as_raw_fd());

        let fsync_op = io_uring::opcode::Fsync::new(fd)
            .build()
            .user_data(user_data);

        unsafe {
            let mut sq = self.ring.submission();
            sq.push(&fsync_op).map_err(|_| IoEngineError::RingFull)?;
        }

        self.ring.submit().map_err(IoEngineError::Submit)?;
        self.inflight += 1;
        self.submitted_total.fetch_add(1, Ordering::Relaxed);

        Ok(())
    }

    /// Poll for completed I/O operations (non-blocking).
    /// Returns completed entries up to `max_completions`.
    pub fn poll_completions(&mut self, out: &mut [IoCompletion]) -> usize {
        let cq = self.ring.completion();
        let mut count = 0;

        for cqe in cq {
            if count >= out.len() {
                break;
            }
            out[count] = IoCompletion {
                user_data: cqe.user_data(),
                result: cqe.result(),
                op: IoOpType::Read, // caller tracks op type via user_data
            };
            count += 1;
            self.inflight = self.inflight.saturating_sub(1);
            self.completed_total.fetch_add(1, Ordering::Relaxed);
        }

        count
    }

    /// Wait for at least one completion (blocking).
    pub fn wait_completions(&mut self, out: &mut [IoCompletion]) -> Result<usize, IoEngineError> {
        self.ring
            .submit_and_wait(1)
            .map_err(IoEngineError::Submit)?;
        Ok(self.poll_completions(out))
    }

    /// Get engine statistics.
    pub fn stats(&self) -> IoEngineStats {
        IoEngineStats {
            submitted: self.submitted_total.load(Ordering::Relaxed),
            completed: self.completed_total.load(Ordering::Relaxed),
            inflight: self.inflight,
            ring_capacity: self.ring.params().sq_entries(),
            registered_files: self.files.len() as u32,
        }
    }
}

// ═══════════════════════════════════════════════════════════
// FFI Entry Points
// ═══════════════════════════════════════════════════════════

/// Create a new io_uring engine. Returns opaque handle or null on failure.
/// `queue_depth` must be power of 2, recommended 256 or 1024.
#[no_mangle]
pub extern "C" fn hcv_io_engine_create(queue_depth: u32) -> *mut IoEngineHandle {
    match IoEngine::new(queue_depth) {
        Ok(engine) => {
            let handle = Box::new(IoEngineHandle { engine });
            Box::into_raw(handle)
        }
        Err(e) => {
            tracing::error!(%e, "io_uring engine creation failed");
            std::ptr::null_mut()
        }
    }
}

/// Destroy an io_uring engine and free its resources.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_destroy(handle: *mut IoEngineHandle) {
    if !handle.is_null() {
        let _ = unsafe { Box::from_raw(handle) };
        tracing::info!("io_uring engine destroyed");
    }
}

/// Register a backing file. Returns file index (>= 0) or negative error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_register_file(
    handle: *mut IoEngineHandle,
    path: *const libc::c_char,
    read_only: i32,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() || path.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &mut (*handle).engine };
        let path_str = unsafe { std::ffi::CStr::from_ptr(path) };
        let path = match path_str.to_str() {
            Ok(s) => s,
            Err(_) => return ErrorCode::InvalidArg as i32,
        };
        match engine.register_file(path, read_only != 0) {
            Ok(id) => id as i32,
            Err(e) => {
                tracing::error!(%e, "register_file failed");
                ErrorCode::KvmError as i32
            }
        }
    })
}

/// Submit an async read. Returns 0 on success, negative on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_submit_read(
    handle: *mut IoEngineHandle,
    fd_index: u32,
    buf: *mut u8,
    len: u32,
    offset: u64,
    user_data: u64,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() || buf.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &mut (*handle).engine };
        match unsafe { engine.submit_read(fd_index, buf, len, offset, user_data) } {
            Ok(()) => 0,
            Err(_) => ErrorCode::KvmError as i32,
        }
    })
}

/// Submit an async write. Returns 0 on success, negative on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_submit_write(
    handle: *mut IoEngineHandle,
    fd_index: u32,
    buf: *const u8,
    len: u32,
    offset: u64,
    user_data: u64,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() || buf.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &mut (*handle).engine };
        match unsafe { engine.submit_write(fd_index, buf, len, offset, user_data) } {
            Ok(()) => 0,
            Err(_) => ErrorCode::KvmError as i32,
        }
    })
}

/// Submit an async flush (fsync). Returns 0 on success, negative on error.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_submit_flush(
    handle: *mut IoEngineHandle,
    fd_index: u32,
    user_data: u64,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &mut (*handle).engine };
        match engine.submit_flush(fd_index, user_data) {
            Ok(()) => 0,
            Err(_) => ErrorCode::KvmError as i32,
        }
    })
}

/// Poll for completions (non-blocking). Returns number of completed ops.
/// `out` must point to an array of at least `max_completions` IoCompletion.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_poll(
    handle: *mut IoEngineHandle,
    out: *mut IoCompletion,
    max_completions: u32,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() || out.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &mut (*handle).engine };
        let completions = unsafe { std::slice::from_raw_parts_mut(out, max_completions as usize) };
        engine.poll_completions(completions) as i32
    })
}

/// Get engine statistics.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_io_engine_stats(handle: *mut IoEngineHandle, out: *mut IoEngineStats) -> i32 {
    crate::panic_barrier::catch(|| {
        if handle.is_null() || out.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let engine = unsafe { &(*handle).engine };
        unsafe {
            *out = engine.stats();
        }
        0
    })
}

// ═══════════════════════════════════════════════════════════
// Tests
// ═══════════════════════════════════════════════════════════

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn create_temp_file(size: usize) -> (tempfile::NamedTempFile, String) {
        let mut f = tempfile::NamedTempFile::new().unwrap();
        let data = vec![0xABu8; size];
        f.write_all(&data).unwrap();
        f.flush().unwrap();
        let path = f.path().to_str().unwrap().to_string();
        (f, path)
    }

    #[test]
    fn test_engine_create_destroy() {
        let engine = IoEngine::new(64).unwrap();
        let stats = engine.stats();
        assert_eq!(stats.submitted, 0);
        assert_eq!(stats.completed, 0);
        assert!(stats.ring_capacity >= 64);
    }

    #[test]
    fn test_register_file() {
        let (_tmp, path) = create_temp_file(4096);
        let mut engine = IoEngine::new(64).unwrap();
        let id = engine.register_file(&path, false).unwrap();
        assert_eq!(id, 0);

        let stats = engine.stats();
        assert_eq!(stats.registered_files, 1);

        engine.unregister_file(id).unwrap();
        assert_eq!(engine.stats().registered_files, 0);
    }

    #[test]
    fn test_submit_read_write() {
        let (_tmp, path) = create_temp_file(4096);
        let mut engine = IoEngine::new(64).unwrap();
        let fd = engine.register_file(&path, false).unwrap();

        // Write data
        let write_buf = vec![0x42u8; 512];
        unsafe {
            engine
                .submit_write(fd, write_buf.as_ptr(), 512, 0, 1001)
                .unwrap();
        }

        // Wait for write completion
        let mut completions = vec![IoCompletion::default(); 16];
        let n = engine.wait_completions(&mut completions).unwrap();
        assert!(n >= 1);
        assert_eq!(completions[0].user_data, 1001);
        assert_eq!(completions[0].result, 512); // 512 bytes written

        // Read back
        let mut read_buf = vec![0u8; 512];
        unsafe {
            engine
                .submit_read(fd, read_buf.as_mut_ptr(), 512, 0, 2002)
                .unwrap();
        }

        let n = engine.wait_completions(&mut completions).unwrap();
        assert!(n >= 1);
        assert_eq!(completions[0].user_data, 2002);
        assert_eq!(completions[0].result, 512);
        assert_eq!(read_buf[0], 0x42);
        assert_eq!(read_buf[511], 0x42);

        let stats = engine.stats();
        assert_eq!(stats.submitted, 2);
        assert_eq!(stats.completed, 2);
        assert_eq!(stats.inflight, 0);
    }

    #[test]
    fn test_submit_flush() {
        let (_tmp, path) = create_temp_file(4096);
        let mut engine = IoEngine::new(64).unwrap();
        let fd = engine.register_file(&path, false).unwrap();

        engine.submit_flush(fd, 3003).unwrap();

        let mut completions = vec![IoCompletion::default(); 16];
        let n = engine.wait_completions(&mut completions).unwrap();
        assert!(n >= 1);
        assert_eq!(completions[0].user_data, 3003);
        assert_eq!(completions[0].result, 0); // fsync returns 0 on success
    }

    #[test]
    fn test_batch_io() {
        let (_tmp, path) = create_temp_file(8192);
        let mut engine = IoEngine::new(256).unwrap();
        let fd = engine.register_file(&path, false).unwrap();

        // Submit 8 writes in batch
        let buf = vec![0xCDu8; 1024];
        for i in 0..8u64 {
            unsafe {
                engine
                    .submit_write(fd, buf.as_ptr(), 1024, i * 1024, 100 + i)
                    .unwrap();
            }
        }

        assert_eq!(engine.stats().inflight, 8);

        // Drain all completions
        let mut completions = vec![IoCompletion::default(); 16];
        let mut total = 0;
        while total < 8 {
            let n = engine.wait_completions(&mut completions).unwrap();
            total += n;
        }
        assert_eq!(total, 8);
        assert_eq!(engine.stats().inflight, 0);
        assert_eq!(engine.stats().submitted, 8);
        assert_eq!(engine.stats().completed, 8);
    }

    #[test]
    fn test_ffi_lifecycle() {
        let (_tmp, path) = create_temp_file(4096);
        let c_path = std::ffi::CString::new(path).unwrap();

        let handle = hcv_io_engine_create(64);
        assert!(!handle.is_null());

        let fd = hcv_io_engine_register_file(handle, c_path.as_ptr(), 0);
        assert!(fd >= 0);

        // Write via FFI
        let write_buf = vec![0x99u8; 256];
        let rc = hcv_io_engine_submit_write(handle, fd as u32, write_buf.as_ptr(), 256, 0, 5005);
        assert_eq!(rc, 0);

        // Poll via FFI
        let mut completions = vec![IoCompletion::default(); 8];
        // wait a bit for completion
        std::thread::sleep(std::time::Duration::from_millis(10));
        let n = hcv_io_engine_poll(handle, completions.as_mut_ptr(), 8);
        assert!(n >= 0);

        // Stats via FFI
        let mut stats = IoEngineStats::default();
        assert_eq!(hcv_io_engine_stats(handle, &mut stats), 0);
        assert!(stats.submitted >= 1);

        hcv_io_engine_destroy(handle);
    }
}

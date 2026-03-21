//! # Virtio Block Device — Emulation with FFI
//!
//! Provides a virtual block device using the Virtio Split Queue.
//! Manages device lifecycle, queue processing, and I/O statistics.

use crate::panic_barrier::ErrorCode;
use crate::virtio_split_queue::SplitVirtqueue;
use std::collections::HashMap;
use std::sync::{
    atomic::{AtomicI32, Ordering},
    Mutex, OnceLock,
};

/// Virtio block device configuration (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct VirtioBlkConfig {
    pub capacity_sectors: u64,
    pub seg_max: u32,
    pub num_queues: u16,
    pub queue_size: u16,
    pub readonly: u8,
    pub _padding: [u8; 3],
}

impl Default for VirtioBlkConfig {
    fn default() -> Self {
        Self {
            capacity_sectors: 0,
            seg_max: 128,
            num_queues: 1,
            queue_size: 256,
            readonly: 0,
            _padding: [0; 3],
        }
    }
}

/// I/O statistics (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VirtioBlkStats {
    pub read_ops: u64,
    pub write_ops: u64,
    pub read_bytes: u64,
    pub write_bytes: u64,
    pub flush_ops: u64,
    pub errors: u64,
}

/// Internal block device instance
#[allow(dead_code)]
struct BlkDevice {
    handle: i32,
    vm_handle: i32,
    config: VirtioBlkConfig,
    queues: Vec<SplitVirtqueue>,
    stats: VirtioBlkStats,
    attached: bool,
}

static NEXT_DEV_HANDLE: AtomicI32 = AtomicI32::new(1);

fn blk_registry() -> &'static Mutex<HashMap<i32, BlkDevice>> {
    static REG: OnceLock<Mutex<HashMap<i32, BlkDevice>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

// ── FFI Functions ────────────────────────────────────────

#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_virtio_blk_create(vm_handle: i32, cfg: *const VirtioBlkConfig) -> i32 {
    crate::panic_barrier::catch(|| {
        if cfg.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: caller guarantees valid pointer
        let config = unsafe { *cfg };
        if config.capacity_sectors == 0 || !config.queue_size.is_power_of_two() {
            return ErrorCode::InvalidArg as i32;
        }

        let handle = NEXT_DEV_HANDLE.fetch_add(1, Ordering::SeqCst);
        let queues: Vec<SplitVirtqueue> = (0..config.num_queues.max(1))
            .map(|_| SplitVirtqueue::new(config.queue_size))
            .collect();

        let dev = BlkDevice {
            handle,
            vm_handle,
            config,
            queues,
            stats: VirtioBlkStats::default(),
            attached: false,
        };

        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        guard.insert(handle, dev);
        tracing::info!(
            handle,
            vm_handle,
            sectors = config.capacity_sectors,
            "virtio-blk created"
        );
        handle
    })
}

#[no_mangle]
pub extern "C" fn hcv_virtio_blk_destroy(dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.remove(&dev_handle) {
            Some(_) => {
                tracing::info!(dev_handle, "virtio-blk destroyed");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[no_mangle]
pub extern "C" fn hcv_virtio_blk_process_queue(dev_handle: i32) -> i32 {
    crate::panic_barrier::catch_mut(|| {
        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        let dev = match guard.get_mut(&dev_handle) {
            Some(d) => d,
            None => return ErrorCode::NotFound as i32,
        };

        let mut processed = 0i32;
        for queue in &mut dev.queues {
            while let Some(head) = queue.pop_avail() {
                let chain = queue.walk_chain(head);
                // Stub: process I/O request from descriptor chain
                let total_len: u32 = chain.iter().map(|d| d.len).sum();
                let is_write = chain.iter().any(|d| d.flags & 2 != 0);

                if is_write {
                    dev.stats.write_ops += 1;
                    dev.stats.write_bytes += total_len as u64;
                } else {
                    dev.stats.read_ops += 1;
                    dev.stats.read_bytes += total_len as u64;
                }

                queue.push_used(head as u32, total_len);
                processed += 1;
            }
        }
        processed
    })
}

#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_virtio_blk_get_stats(dev_handle: i32, stats: *mut VirtioBlkStats) -> i32 {
    crate::panic_barrier::catch(|| {
        if stats.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get(&dev_handle) {
            Some(dev) => {
                // SAFETY: caller guarantees out is valid
                unsafe {
                    *stats = dev.stats;
                }
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[no_mangle]
pub extern "C" fn hcv_virtio_blk_resize(dev_handle: i32, new_capacity_sectors: u64) -> i32 {
    crate::panic_barrier::catch(|| {
        if new_capacity_sectors == 0 {
            return ErrorCode::InvalidArg as i32;
        }
        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&dev_handle) {
            Some(dev) => {
                dev.config.capacity_sectors = new_capacity_sectors;
                tracing::info!(dev_handle, new_capacity_sectors, "virtio-blk resized");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[no_mangle]
pub extern "C" fn hcv_virtio_blk_attach(vm_handle: i32, dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&dev_handle) {
            Some(dev) => {
                if dev.attached {
                    return ErrorCode::AlreadyExists as i32;
                }
                dev.vm_handle = vm_handle;
                dev.attached = true;
                tracing::info!(vm_handle, dev_handle, "virtio-blk attached");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[no_mangle]
pub extern "C" fn hcv_virtio_blk_detach(_vm_handle: i32, dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match blk_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&dev_handle) {
            Some(dev) => {
                if !dev.attached {
                    return ErrorCode::InvalidState as i32;
                }
                dev.attached = false;
                tracing::info!(dev_handle, "virtio-blk detached");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_config() -> VirtioBlkConfig {
        VirtioBlkConfig {
            capacity_sectors: 2048, // 1MB
            queue_size: 16,
            ..Default::default()
        }
    }

    #[test]
    fn test_create_destroy() {
        let cfg = test_config();
        let h = hcv_virtio_blk_create(1, &cfg);
        assert!(h > 0);
        assert_eq!(hcv_virtio_blk_destroy(h), 0);
    }

    #[test]
    fn test_attach_detach() {
        let cfg = test_config();
        let h = hcv_virtio_blk_create(1, &cfg);
        assert_eq!(hcv_virtio_blk_attach(1, h), 0);
        assert_eq!(hcv_virtio_blk_attach(1, h), ErrorCode::AlreadyExists as i32); // already attached
        assert_eq!(hcv_virtio_blk_detach(1, h), 0);
        hcv_virtio_blk_destroy(h);
    }

    #[test]
    fn test_stats() {
        let cfg = test_config();
        let h = hcv_virtio_blk_create(1, &cfg);
        let mut stats = VirtioBlkStats::default();
        assert_eq!(hcv_virtio_blk_get_stats(h, &mut stats), 0);
        assert_eq!(stats.read_ops, 0);
        hcv_virtio_blk_destroy(h);
    }

    #[test]
    fn test_resize() {
        let cfg = test_config();
        let h = hcv_virtio_blk_create(1, &cfg);
        assert_eq!(hcv_virtio_blk_resize(h, 4096), 0);
        assert_eq!(hcv_virtio_blk_resize(h, 0), ErrorCode::InvalidArg as i32);
        hcv_virtio_blk_destroy(h);
    }

    #[test]
    fn test_null_config() {
        assert_eq!(
            hcv_virtio_blk_create(1, std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );
    }
}

//! # Virtio Network Device — Emulation with FFI
//!
//! Provides a virtual network device using the Virtio Split Queue.
//! Manages device lifecycle, RX/TX queue processing, and network statistics.

use crate::panic_barrier::ErrorCode;
use crate::virtio_split_queue::SplitVirtqueue;
use std::collections::HashMap;
use std::sync::{
    atomic::{AtomicI32, Ordering},
    Mutex, OnceLock,
};

// ── Constants ────────────────────────────────────────────

/// Feature bit: device has a MAC address
pub const VIRTIO_NET_F_MAC: u32 = 1 << 5;
/// Feature bit: device has a link status
pub const VIRTIO_NET_F_STATUS: u32 = 1 << 16;

/// RX queue index
pub const RX_QUEUE: u16 = 0;
/// TX queue index
pub const TX_QUEUE: u16 = 1;

/// Default queue size for network queues
const DEFAULT_QUEUE_SIZE: u16 = 256;

// ── FFI-safe structs ─────────────────────────────────────

/// Virtio network device configuration (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct VirtioNetConfig {
    pub mac: [u8; 6],
    pub status: u16,
    pub max_queue_pairs: u16,
    pub _padding: [u8; 6],
}

impl Default for VirtioNetConfig {
    fn default() -> Self {
        Self {
            mac: [0x52, 0x54, 0x00, 0x12, 0x34, 0x56], // default QEMU-style MAC
            status: 1,                                 // link up
            max_queue_pairs: 1,
            _padding: [0; 6],
        }
    }
}

/// Network I/O statistics (FFI-safe)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VirtioNetStats {
    pub rx_packets: u64,
    pub tx_packets: u64,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub rx_drops: u64,
    pub tx_drops: u64,
}

// ── Internal device ──────────────────────────────────────

/// Internal network device instance
#[allow(dead_code)]
struct NetDevice {
    handle: i32,
    vm_handle: i32,
    config: VirtioNetConfig,
    rx_queue: SplitVirtqueue,
    tx_queue: SplitVirtqueue,
    stats: VirtioNetStats,
    attached: bool,
}

// ── Global registry ──────────────────────────────────────

static NEXT_NET_HANDLE: AtomicI32 = AtomicI32::new(1);

fn net_registry() -> &'static Mutex<HashMap<i32, NetDevice>> {
    static REG: OnceLock<Mutex<HashMap<i32, NetDevice>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

// ── FFI Functions ────────────────────────────────────────

/// Create a new virtio-net device.
/// Returns a positive handle on success, or a negative error code.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_virtio_net_create(vm_handle: i32, cfg: *const VirtioNetConfig) -> i32 {
    crate::panic_barrier::catch(|| {
        if cfg.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        // SAFETY: caller guarantees valid pointer
        let config = unsafe { *cfg };

        let handle = NEXT_NET_HANDLE.fetch_add(1, Ordering::SeqCst);
        let dev = NetDevice {
            handle,
            vm_handle,
            config,
            rx_queue: SplitVirtqueue::new(DEFAULT_QUEUE_SIZE),
            tx_queue: SplitVirtqueue::new(DEFAULT_QUEUE_SIZE),
            stats: VirtioNetStats::default(),
            attached: false,
        };

        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        guard.insert(handle, dev);
        tracing::info!(handle, vm_handle, "virtio-net created");
        handle
    })
}

/// Destroy a virtio-net device.
/// Returns 0 on success, or a negative error code.
#[no_mangle]
pub extern "C" fn hcv_virtio_net_destroy(dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.remove(&dev_handle) {
            Some(_) => {
                tracing::info!(dev_handle, "virtio-net destroyed");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

/// Process received packets on the RX queue.
/// Returns the number of processed descriptors, or a negative error code.
#[no_mangle]
pub extern "C" fn hcv_virtio_net_process_rx(dev_handle: i32) -> i32 {
    crate::panic_barrier::catch_mut(|| {
        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        let dev = match guard.get_mut(&dev_handle) {
            Some(d) => d,
            None => return ErrorCode::NotFound as i32,
        };

        let mut processed = 0i32;
        while let Some(head) = dev.rx_queue.pop_avail() {
            let chain = dev.rx_queue.walk_chain(head);
            let total_len: u32 = chain.iter().map(|d| d.len).sum();

            dev.stats.rx_packets += 1;
            dev.stats.rx_bytes += total_len as u64;

            dev.rx_queue.push_used(head as u32, total_len);
            processed += 1;
        }
        processed
    })
}

/// Process packets to transmit on the TX queue.
/// Returns the number of processed descriptors, or a negative error code.
#[no_mangle]
pub extern "C" fn hcv_virtio_net_process_tx(dev_handle: i32) -> i32 {
    crate::panic_barrier::catch_mut(|| {
        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        let dev = match guard.get_mut(&dev_handle) {
            Some(d) => d,
            None => return ErrorCode::NotFound as i32,
        };

        let mut processed = 0i32;
        while let Some(head) = dev.tx_queue.pop_avail() {
            let chain = dev.tx_queue.walk_chain(head);
            let total_len: u32 = chain.iter().map(|d| d.len).sum();

            dev.stats.tx_packets += 1;
            dev.stats.tx_bytes += total_len as u64;

            dev.tx_queue.push_used(head as u32, total_len);
            processed += 1;
        }
        processed
    })
}

/// Get network device statistics.
/// Returns 0 on success, or a negative error code.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_virtio_net_get_stats(dev_handle: i32, stats: *mut VirtioNetStats) -> i32 {
    crate::panic_barrier::catch(|| {
        if stats.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let guard = match net_registry().lock() {
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

/// Set the MAC address of a virtio-net device.
/// `mac` must point to a 6-byte array.
/// Returns 0 on success, or a negative error code.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_virtio_net_set_mac(dev_handle: i32, mac: *const u8) -> i32 {
    crate::panic_barrier::catch(|| {
        if mac.is_null() {
            return ErrorCode::InvalidArg as i32;
        }
        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&dev_handle) {
            Some(dev) => {
                // SAFETY: caller guarantees mac points to at least 6 bytes
                let mac_bytes = unsafe { std::slice::from_raw_parts(mac, 6) };
                dev.config.mac.copy_from_slice(mac_bytes);
                tracing::info!(dev_handle, mac = ?dev.config.mac, "virtio-net MAC set");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

/// Attach a virtio-net device to a VM.
/// Returns 0 on success, or a negative error code.
#[no_mangle]
pub extern "C" fn hcv_virtio_net_attach(vm_handle: i32, dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match net_registry().lock() {
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
                tracing::info!(vm_handle, dev_handle, "virtio-net attached");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

/// Detach a virtio-net device from a VM.
/// Returns 0 on success, or a negative error code.
#[no_mangle]
pub extern "C" fn hcv_virtio_net_detach(_vm_handle: i32, dev_handle: i32) -> i32 {
    crate::panic_barrier::catch(|| {
        let mut guard = match net_registry().lock() {
            Ok(g) => g,
            Err(_) => return ErrorCode::KvmError as i32,
        };
        match guard.get_mut(&dev_handle) {
            Some(dev) => {
                if !dev.attached {
                    return ErrorCode::InvalidState as i32;
                }
                dev.attached = false;
                tracing::info!(dev_handle, "virtio-net detached");
                0
            }
            None => ErrorCode::NotFound as i32,
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_config() -> VirtioNetConfig {
        VirtioNetConfig::default()
    }

    #[test]
    fn test_create_destroy() {
        let cfg = test_config();
        let h = hcv_virtio_net_create(1, &cfg);
        assert!(h > 0);
        assert_eq!(hcv_virtio_net_destroy(h), 0);
    }

    #[test]
    fn test_attach_detach() {
        let cfg = test_config();
        let h = hcv_virtio_net_create(1, &cfg);
        assert_eq!(hcv_virtio_net_attach(1, h), 0);
        assert_eq!(hcv_virtio_net_attach(1, h), ErrorCode::AlreadyExists as i32); // already attached
        assert_eq!(hcv_virtio_net_detach(1, h), 0);
        assert_eq!(hcv_virtio_net_detach(1, h), ErrorCode::InvalidState as i32); // not attached
        hcv_virtio_net_destroy(h);
    }

    #[test]
    fn test_stats() {
        let cfg = test_config();
        let h = hcv_virtio_net_create(1, &cfg);
        let mut stats = VirtioNetStats::default();
        assert_eq!(hcv_virtio_net_get_stats(h, &mut stats), 0);
        assert_eq!(stats.rx_packets, 0);
        assert_eq!(stats.tx_packets, 0);
        assert_eq!(stats.rx_bytes, 0);
        assert_eq!(stats.tx_bytes, 0);
        assert_eq!(stats.rx_drops, 0);
        assert_eq!(stats.tx_drops, 0);
        hcv_virtio_net_destroy(h);
    }

    #[test]
    fn test_null_config() {
        assert_eq!(
            hcv_virtio_net_create(1, std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );
    }

    #[test]
    fn test_set_mac() {
        let cfg = test_config();
        let h = hcv_virtio_net_create(1, &cfg);
        let new_mac: [u8; 6] = [0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF];
        assert_eq!(hcv_virtio_net_set_mac(h, new_mac.as_ptr()), 0);

        // Verify MAC was set by checking via the registry
        let guard = net_registry().lock().unwrap();
        let dev = guard.get(&h).unwrap();
        assert_eq!(dev.config.mac, new_mac);
        drop(guard);

        // Test null pointer
        assert_eq!(
            hcv_virtio_net_set_mac(h, std::ptr::null()),
            ErrorCode::InvalidArg as i32
        );

        hcv_virtio_net_destroy(h);
    }
}

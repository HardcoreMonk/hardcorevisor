//! # Virtio Split Queue — Internal Module
//!
//! Implements the Virtio 1.x Split Virtqueue ring buffer.
//! Not directly exposed via FFI — used by `virtio_blk` and future virtio devices.

use std::sync::atomic::{AtomicU16, Ordering};

/// Virtio descriptor flags
pub const VRING_DESC_F_NEXT: u16 = 1;
pub const VRING_DESC_F_WRITE: u16 = 2;
pub const VRING_DESC_F_INDIRECT: u16 = 4;

/// A single descriptor in the descriptor table
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VringDesc {
    pub addr: u64,
    pub len: u32,
    pub flags: u16,
    pub next: u16,
}

/// Available ring — guest writes, host reads
#[derive(Debug)]
pub struct VringAvail {
    pub flags: u16,
    pub idx: AtomicU16,
    pub ring: Vec<u16>,
}

/// Used ring element
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VringUsedElem {
    pub id: u32,
    pub len: u32,
}

/// Used ring — host writes, guest reads
#[derive(Debug)]
pub struct VringUsed {
    pub flags: u16,
    pub idx: AtomicU16,
    pub ring: Vec<VringUsedElem>,
}

/// Complete Split Virtqueue
pub struct SplitVirtqueue {
    pub queue_size: u16,
    pub desc_table: Vec<VringDesc>,
    pub avail: VringAvail,
    pub used: VringUsed,
    last_avail_idx: u16,
}

impl SplitVirtqueue {
    /// Create a new split virtqueue with the given size (must be power of 2).
    pub fn new(queue_size: u16) -> Self {
        assert!(
            queue_size.is_power_of_two(),
            "queue_size must be power of 2"
        );
        Self {
            queue_size,
            desc_table: vec![VringDesc::default(); queue_size as usize],
            avail: VringAvail {
                flags: 0,
                idx: AtomicU16::new(0),
                ring: vec![0; queue_size as usize],
            },
            used: VringUsed {
                flags: 0,
                idx: AtomicU16::new(0),
                ring: vec![VringUsedElem::default(); queue_size as usize],
            },
            last_avail_idx: 0,
        }
    }

    /// Check if there are available descriptors to process.
    pub fn has_available(&self) -> bool {
        let avail_idx = self.avail.idx.load(Ordering::Acquire);
        avail_idx != self.last_avail_idx
    }

    /// Pop the next available descriptor chain head index.
    /// Returns `None` if no descriptors available.
    pub fn pop_avail(&mut self) -> Option<u16> {
        let avail_idx = self.avail.idx.load(Ordering::Acquire);
        if avail_idx == self.last_avail_idx {
            return None;
        }
        let ring_idx = (self.last_avail_idx % self.queue_size) as usize;
        let head = self.avail.ring[ring_idx];
        self.last_avail_idx = self.last_avail_idx.wrapping_add(1);
        Some(head)
    }

    /// Walk a descriptor chain starting from `head`.
    /// Returns the list of descriptors in the chain.
    pub fn walk_chain(&self, head: u16) -> Vec<VringDesc> {
        let mut chain = Vec::new();
        let mut idx = head;
        let mut visited = 0u32;
        loop {
            if idx >= self.queue_size {
                break;
            }
            let desc = self.desc_table[idx as usize];
            chain.push(desc);
            visited += 1;
            if visited > self.queue_size as u32 {
                tracing::warn!("infinite descriptor chain detected");
                break;
            }
            if desc.flags & VRING_DESC_F_NEXT == 0 {
                break;
            }
            idx = desc.next;
        }
        chain
    }

    /// Push a used element back to the guest.
    pub fn push_used(&mut self, id: u32, len: u32) {
        let used_idx = self.used.idx.load(Ordering::Relaxed);
        let ring_idx = (used_idx % self.queue_size) as usize;
        self.used.ring[ring_idx] = VringUsedElem { id, len };
        // Release ordering: guest must see the written VringUsedElem
        self.used
            .idx
            .store(used_idx.wrapping_add(1), Ordering::Release);
    }

    /// Get the number of pending available descriptors.
    pub fn pending_count(&self) -> u16 {
        let avail_idx = self.avail.idx.load(Ordering::Acquire);
        avail_idx.wrapping_sub(self.last_avail_idx)
    }

    /// Reset the queue to initial state.
    pub fn reset(&mut self) {
        self.last_avail_idx = 0;
        self.avail.idx.store(0, Ordering::Relaxed);
        self.used.idx.store(0, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_new_queue() {
        let q = SplitVirtqueue::new(256);
        assert_eq!(q.queue_size, 256);
        assert!(!q.has_available());
    }

    #[test]
    fn test_avail_used_cycle() {
        let mut q = SplitVirtqueue::new(4);
        // Simulate guest making descriptor 0 available
        q.avail.ring[0] = 0;
        q.avail.idx.store(1, Ordering::Release);

        assert!(q.has_available());
        let head = q.pop_avail().unwrap();
        assert_eq!(head, 0);
        assert!(!q.has_available());

        // Host returns used
        q.push_used(0, 512);
        assert_eq!(q.used.idx.load(Ordering::Acquire), 1);
    }

    #[test]
    fn test_chain_walk() {
        let mut q = SplitVirtqueue::new(4);
        // Chain: desc[0] -> desc[1] -> desc[2] (no next)
        q.desc_table[0] = VringDesc {
            addr: 0x1000,
            len: 512,
            flags: VRING_DESC_F_NEXT,
            next: 1,
        };
        q.desc_table[1] = VringDesc {
            addr: 0x2000,
            len: 256,
            flags: VRING_DESC_F_NEXT | VRING_DESC_F_WRITE,
            next: 2,
        };
        q.desc_table[2] = VringDesc {
            addr: 0x3000,
            len: 1,
            flags: VRING_DESC_F_WRITE,
            next: 0,
        };

        let chain = q.walk_chain(0);
        assert_eq!(chain.len(), 3);
        assert_eq!(chain[0].addr, 0x1000);
        assert_eq!(chain[2].addr, 0x3000);
    }

    #[test]
    fn test_reset() {
        let mut q = SplitVirtqueue::new(4);
        q.avail.idx.store(5, Ordering::Relaxed);
        q.used.idx.store(3, Ordering::Relaxed);
        q.last_avail_idx = 5;
        q.reset();
        assert_eq!(q.avail.idx.load(Ordering::Relaxed), 0);
        assert!(!q.has_available());
    }

    #[test]
    #[should_panic]
    fn test_non_power_of_two() {
        SplitVirtqueue::new(3);
    }
}

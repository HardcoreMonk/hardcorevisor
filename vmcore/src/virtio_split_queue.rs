//! # Virtio Split Queue — 내부 모듈
//!
//! ## 목적
//! Virtio 1.x Split Virtqueue 링 버퍼를 구현한다.
//! FFI로 직접 노출하지 않으며, `virtio_blk`, `virtio_net` 등 virtio 디바이스가 사용한다.
//!
//! ## 아키텍처 위치
//! ```text
//! 게스트 (avail 링에 디스크립터 추가) → SplitVirtqueue → 호스트 (used 링에 결과 기록)
//! ```
//!
//! ## 핵심 개념
//! - Descriptor Table: 데이터 버퍼의 주소/길이/플래그를 기술
//! - Available Ring: 게스트가 처리할 디스크립터 헤드를 넣는 링 (게스트 쓰기, 호스트 읽기)
//! - Used Ring: 호스트가 처리 완료한 디스크립터를 넣는 링 (호스트 쓰기, 게스트 읽기)
//! - 큐 크기는 반드시 2의 거듭제곱이어야 한다
//!
//! ## 스레드 안전성
//! `AtomicU16`으로 avail/used 인덱스를 관리하여 lock-free 동기화를 수행한다.
//! 단, 단일 생산자/단일 소비자 모델을 전제한다.

use std::sync::atomic::{AtomicU16, Ordering};

/// Virtio 디스크립터 플래그: 체인의 다음 디스크립터가 있음
pub const VRING_DESC_F_NEXT: u16 = 1;
/// Virtio 디스크립터 플래그: 디바이스가 쓸 수 있는 버퍼 (읽기용이면 미설정)
pub const VRING_DESC_F_WRITE: u16 = 2;
/// Virtio 디스크립터 플래그: 간접 디스크립터 테이블
pub const VRING_DESC_F_INDIRECT: u16 = 4;

/// 디스크립터 테이블의 단일 엔트리.
/// 게스트 메모리의 데이터 버퍼 위치와 크기를 기술한다.
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VringDesc {
    /// 게스트 물리 주소 (데이터 버퍼 위치)
    pub addr: u64,
    /// 버퍼 길이 (바이트)
    pub len: u32,
    /// 플래그 (VRING_DESC_F_NEXT, VRING_DESC_F_WRITE 등)
    pub flags: u16,
    /// 다음 디스크립터 인덱스 (VRING_DESC_F_NEXT 설정 시 유효)
    pub next: u16,
}

/// Available 링 — 게스트가 쓰고, 호스트가 읽는다
#[derive(Debug)]
pub struct VringAvail {
    pub flags: u16,
    pub idx: AtomicU16,
    pub ring: Vec<u16>,
}

/// Used 링 엘리먼트 (디스크립터 ID + 처리된 바이트 수)
#[repr(C)]
#[derive(Debug, Clone, Copy, Default)]
pub struct VringUsedElem {
    pub id: u32,
    pub len: u32,
}

/// Used 링 — 호스트가 쓰고, 게스트가 읽는다
#[derive(Debug)]
pub struct VringUsed {
    pub flags: u16,
    pub idx: AtomicU16,
    pub ring: Vec<VringUsedElem>,
}

/// 완전한 Split Virtqueue 구현.
/// 디스크립터 테이블, available 링, used 링을 포함한다.
pub struct SplitVirtqueue {
    pub queue_size: u16,
    pub desc_table: Vec<VringDesc>,
    pub avail: VringAvail,
    pub used: VringUsed,
    last_avail_idx: u16,
}

impl SplitVirtqueue {
    /// 주어진 크기로 새 split virtqueue를 생성한다 (2의 거듭제곱이어야 함).
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

    /// 처리할 가용 디스크립터가 있는지 확인한다.
    pub fn has_available(&self) -> bool {
        let avail_idx = self.avail.idx.load(Ordering::Acquire);
        avail_idx != self.last_avail_idx
    }

    /// 다음 가용 디스크립터 체인의 헤드 인덱스를 꺼낸다.
    /// 가용 디스크립터가 없으면 `None`을 반환한다.
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

    /// `head`부터 시작하는 디스크립터 체인을 순회한다.
    /// 체인에 포함된 디스크립터 목록을 반환한다.
    /// 무한 루프 방지를 위해 queue_size 이상 방문하면 중단한다.
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

    /// 처리 완료된 엘리먼트를 게스트에게 돌려준다 (used 링에 추가).
    pub fn push_used(&mut self, id: u32, len: u32) {
        let used_idx = self.used.idx.load(Ordering::Relaxed);
        let ring_idx = (used_idx % self.queue_size) as usize;
        self.used.ring[ring_idx] = VringUsedElem { id, len };
        // WHY: Release 오더링 — 게스트가 used idx를 읽을 때 VringUsedElem 쓰기가 보여야 함
        self.used
            .idx
            .store(used_idx.wrapping_add(1), Ordering::Release);
    }

    /// 대기 중인 가용 디스크립터 수를 반환한다.
    pub fn pending_count(&self) -> u16 {
        let avail_idx = self.avail.idx.load(Ordering::Acquire);
        avail_idx.wrapping_sub(self.last_avail_idx)
    }

    /// 큐를 초기 상태로 리셋한다.
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

use criterion::{criterion_group, criterion_main, Criterion};
use std::hint::black_box;

fn bench_event_ring(c: &mut Criterion) {
    use vmcore::event_ring::*;

    c.bench_function("event_ring_push_pop_1000", |b| {
        b.iter(|| {
            let handle = hcv_event_ring_create(1024);
            let evt = Event {
                event_type: EventType::VmStateChanged,
                vm_handle: 1,
                vcpu_id: 0,
                data: 42,
                timestamp_ns: 0,
            };
            for _ in 0..1000 {
                hcv_event_ring_push(handle, &evt);
            }
            let mut out = Event::default();
            for _ in 0..1000 {
                hcv_event_ring_pop(handle, &mut out);
            }
            hcv_event_ring_destroy(handle);
        });
    });
}

fn bench_vm_create_destroy(c: &mut Criterion) {
    use vmcore::kvm_mgr::*;

    c.bench_function("vm_create_destroy_100", |b| {
        b.iter(|| {
            let mut handles = Vec::new();
            for _ in 0..100 {
                handles.push(hcv_vm_create());
            }
            for h in handles {
                hcv_vm_destroy(black_box(h));
            }
        });
    });
}

fn bench_io_engine(c: &mut Criterion) {
    use std::io::Write;
    use vmcore::io_engine::*;

    c.bench_function("io_engine_write_read_4k", |b| {
        let mut tmp = tempfile::NamedTempFile::new().unwrap();
        tmp.write_all(&vec![0u8; 65536]).unwrap();
        tmp.flush().unwrap();
        let path = std::ffi::CString::new(tmp.path().to_str().unwrap()).unwrap();

        b.iter(|| {
            let handle = hcv_io_engine_create(64);
            let fd = hcv_io_engine_register_file(handle, path.as_ptr(), 0);

            let buf = vec![0x42u8; 4096];
            hcv_io_engine_submit_write(handle, fd as u32, buf.as_ptr(), 4096, 0, 1);

            let mut completions = vec![IoCompletion::default(); 8];
            std::thread::sleep(std::time::Duration::from_millis(1));
            hcv_io_engine_poll(handle, completions.as_mut_ptr(), 8);

            hcv_io_engine_destroy(handle);
        });
    });
}

fn bench_virtio_split_queue(c: &mut Criterion) {
    use vmcore::virtio_split_queue::*;

    c.bench_function("split_queue_push_pop_1000", |b| {
        b.iter(|| {
            let mut q = SplitVirtqueue::new(1024);
            for i in 0..1000u16 {
                q.avail.ring[i as usize] = i;
            }
            q.avail
                .idx
                .store(1000, std::sync::atomic::Ordering::Release);

            for _ in 0..1000 {
                black_box(q.pop_avail());
            }
        });
    });
}

criterion_group!(
    benches,
    bench_event_ring,
    bench_vm_create_destroy,
    bench_io_engine,
    bench_virtio_split_queue
);
criterion_main!(benches);

# Changelog

## [0.1.0] - 2026-03-21

### Added
- Rust vmcore: 63 FFI functions, 13 modules (kvm_mgr, vcpu_mgr, memory_mgr, event_ring, io_engine, kvm_sys, kvm_boot, virtio_split_queue, virtio_blk, virtio_blk_io, virtio_net, panic_barrier)
- Go Controller: REST API (32+ endpoints), gRPC (17 RPCs), /metrics
- 7 services: Compute (Dual VMM), Storage, Network, Peripheral, HA, Backup, Auth
- hcvtui: Ratatui TUI with 6 live screens, VM create form, VM detail view
- hcvctl: CLI with 36 commands, --output json/yaml/table, --tls, completion
- Docker stack: etcd + Prometheus + Grafana + Controller (auto-provisioned dashboards)
- RBAC (admin/operator/viewer) + audit logging
- io_uring async I/O engine for virtio-blk
- KVM mini guest boot (x86 real mode "HCV" output)
- etcd state persistence (PersistentComputeService)
- Prometheus alerting rules (4 rules)
- CI: 8-job GitHub Actions pipeline with coverage + security audit
- 81 automated tests (70 Rust + 11 Go) + 17 Docker smoke tests

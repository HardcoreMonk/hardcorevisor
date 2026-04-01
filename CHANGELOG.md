# Changelog

## [0.2.0](https://github.com/HardcoreMonk/hardcorevisor/compare/v0.1.0...v0.2.0) (2026-04-01)


### Features

* 실제 VM 부팅 지원 — QEMU Real 모드 + 디스크/ISO/VNC 통합 ([a85f37f](https://github.com/HardcoreMonk/hardcorevisor/commit/a85f37f6f101a7dd4e24dcbda79fb5a71a3ea1ef))

## [0.2.0] - 2026-03-23

### Added (Phase 12–17)
- JWT authentication: bcrypt password hashing + SQLite user DB + Bearer token lifecycle (login/refresh/logout/revoke)
- Auth API: POST /auth/login, /auth/refresh, /auth/logout, GET/POST/DELETE /auth/users
- Security headers middleware (X-Content-Type-Options, X-Frame-Options, X-XSS-Protection)
- hcvctl login command with token storage (~/.hcvctl/token) and auto Bearer header
- ZFS snapshot rollback (`zfs rollback`), clone (`zfs clone`), delete (`zfs destroy`)
- VM snapshot ↔ storage integration (Create calls storage.CreateSnapshot, Restore calls RollbackSnapshot)
- Snapshot metadata etcd persistence (PersistentSnapshotService, LoadFromStore)
- QEMU snapshot via QMP (savevm/loadvm) for both Emulated and Real modes
- Linux Bridge management (CreateBridge/DeleteBridge/AddPort/SetBridgeIP via `ip link`)
- veth pair management (CreateVethPair/DeleteVethPair for container networking)
- DHCP/IP pool manager (AllocateIP/ReleaseIP/ListLeases/GenerateDnsmasqConfig)
- VXLAN overlay networking (ip link add type vxlan, bridge binding)
- Zone CRUD API (POST/DELETE /api/v1/network/zones)
- QEMU Real mode hardening (exponential backoff retry, process health monitoring, QueryStatus)
- VM migration phase 2 (pre-checks, MigrationStatus, progress tracking via WebSocket)
- etcd leader election (concurrency.Election, single-node fallback)
- Distributed locking (concurrency.Mutex, in-memory fallback)
- Node failure detection (FailureDetector, 3× heartbeat threshold, NodeDown callbacks)
- VM auto-restart on node failure (FailoverManager, RestartPolicy: always/on-failure/never)
- HA API: GET /cluster/leader, POST /cluster/promote
- LXC container runtime: full VMMBackend implementation (handle range 20000+, Emulated/Real modes)
- LXC config generator (lxc.conf format, cgroup v2 limits, veth networking)
- Triple VMM backend selector: rustvmm (1-9999) + qemu (10000+) + lxc (20000+)
- LXC template management (ubuntu/alpine/debian/centos, lxc-create -t download)
- LXC cgroup v2 resource limits (cpu.max, memory.max, io.max, pids)
- LXC ZFS storage integration (rootfs on ZFS, snapshot, clone)
- LXC security namespaces (uid/gid mapping, AppArmor, seccomp, capability dropping)
- LXC CRIU migration (checkpoint/restore for live migration)
- LXC exec/attach (POST /api/v1/vms/{id}/exec with WebSocket support)
- Container stats API (GET /api/v1/vms/{id}/stats)
- Container type support in TUI (TYPE column, CT/VM color coding, container create form)
- hcvctl container commands (list/create/start/stop/delete/exec)
- vm list --type filter (vm/container)
- 189 automated tests (82 Rust + 107 Go) — +84 tests from v0.1.0

## [0.1.0] - 2026-03-21

### Added
- Rust vmcore: 73 FFI functions, 15 modules (lib, panic_barrier, kvm_mgr, kvm_sys, kvm_boot, vcpu_mgr, memory_mgr, event_ring, io_engine, virtio_split_queue, virtio_blk, virtio_blk_io, virtio_net, tap_device, kvm_loader)
- Go Controller: REST API (61 endpoints), gRPC (17 RPCs), /metrics, /ws WebSocket
- 7 services: Compute (Dual VMM), Storage, Network, Peripheral, HA, Backup, Auth
- 5 additional services: Template, Snapshot, Image, Config, Logging
- hcvtui: Ratatui TUI with 6 live screens, VM create form, VM detail view
- hcvctl: 15 top-level CLI commands (vm/node/storage/network/device/cluster/backup/template/image/snapshot/shell/status/completion/version/login/container), --output json/yaml/table, --tls, --user
- Docker stack: 7 services (etcd + Prometheus + Grafana + Controller + AlertManager + Loki + Promtail), auto-provisioned Grafana 9-panel dashboard
- RBAC (admin/operator/viewer) + audit logging (structured JSON)
- io_uring async I/O engine for virtio-blk (Linux 6.x)
- KVM mini guest boot (x86 real mode "HCV" output) + Linux bzImage loader
- TAP device driver (/dev/net/tun, TUNSETIFF)
- etcd state persistence (PersistentComputeService) with in-memory fallback
- Prometheus alerting rules (4 rules) + AlertManager integration
- Pluggable driver architecture: ZFS/Ceph (storage), nftables (network), sysfs (peripheral), etcd (HA)
- API middleware: RequestID, Audit, Logging, Metrics, RBAC, CORS, Recovery, RateLimit, Pagination, Versioning, SecurityHeaders
- OpenAPI 3.0.3 spec (docs/openapi.yaml)
- CI: 8-job GitHub Actions pipeline with coverage + security audit
- 105 automated tests (82 Rust + 3 Go API + 20 Go E2E) + 17 Docker smoke tests

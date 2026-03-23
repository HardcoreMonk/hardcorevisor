package compute

import (
	"strings"
	"testing"
)

// ── LXC Backend Tests ────────────────────────────────

func TestLXC_CreateDestroy(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	// Create
	vm, err := b.CreateVM("test-ct", 2, 1024)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if vm.Name != "test-ct" {
		t.Errorf("expected name test-ct, got %s", vm.Name)
	}
	if vm.Backend != "lxc" {
		t.Errorf("expected backend lxc, got %s", vm.Backend)
	}
	if vm.Type != "container" {
		t.Errorf("expected type container, got %s", vm.Type)
	}
	if vm.State != "configured" {
		t.Errorf("expected state configured, got %s", vm.State)
	}
	if vm.ID < 20000 {
		t.Errorf("expected handle >= 20000, got %d", vm.ID)
	}
	if vm.RestartPolicy != "always" {
		t.Errorf("expected restart_policy always, got %s", vm.RestartPolicy)
	}
	if vm.IPAddress == "" {
		t.Error("expected IP address to be assigned in emulated mode")
	}

	// Destroy
	if err := b.DestroyVM(vm.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// Verify destroyed
	if _, err := b.GetVM(vm.ID); err == nil {
		t.Error("expected error after destroy")
	}
}

func TestLXC_Lifecycle(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	// Create
	vm, err := b.CreateVM("lifecycle-ct", 1, 512)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	handle := vm.ID

	// Start (configured → running)
	if err := b.StartVM(handle); err != nil {
		t.Fatalf("start: %v", err)
	}
	vm, _ = b.GetVM(handle)
	if vm.State != "running" {
		t.Errorf("expected running, got %s", vm.State)
	}

	// Pause (running → paused)
	if err := b.PauseVM(handle); err != nil {
		t.Fatalf("pause: %v", err)
	}
	vm, _ = b.GetVM(handle)
	if vm.State != "paused" {
		t.Errorf("expected paused, got %s", vm.State)
	}

	// Resume (paused → running)
	if err := b.ResumeVM(handle); err != nil {
		t.Fatalf("resume: %v", err)
	}
	vm, _ = b.GetVM(handle)
	if vm.State != "running" {
		t.Errorf("expected running, got %s", vm.State)
	}

	// Stop (running → stopped)
	if err := b.StopVM(handle); err != nil {
		t.Fatalf("stop: %v", err)
	}
	vm, _ = b.GetVM(handle)
	if vm.State != "stopped" {
		t.Errorf("expected stopped, got %s", vm.State)
	}

	// Destroy
	if err := b.DestroyVM(handle); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func TestLXC_EmulatedMode(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	// Create multiple containers
	vm1, err := b.CreateVM("ct-1", 1, 256)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	vm2, err := b.CreateVM("ct-2", 2, 512)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}

	// List
	vms := b.ListVMs()
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(vms))
	}

	// IDs should be unique and >= 20000
	if vm1.ID == vm2.ID {
		t.Error("handles should be unique")
	}
	if vm1.ID < 20000 || vm2.ID < 20000 {
		t.Error("handles should be >= 20000")
	}

	// Verify emulated IP assignment
	if vm1.IPAddress == "" || vm2.IPAddress == "" {
		t.Error("expected IP addresses in emulated mode")
	}
	if vm1.IPAddress == vm2.IPAddress {
		t.Error("expected different IPs for different containers")
	}
}

func TestLXC_InvalidStateTransition(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, _ := b.CreateVM("invalid-ct", 1, 256)
	handle := vm.ID

	// Pause from configured (invalid: configured → paused not allowed)
	if err := b.PauseVM(handle); err == nil {
		t.Error("expected error for pause from configured")
	}

	// Stop from configured (valid: configured → stopped)
	if err := b.StopVM(handle); err != nil {
		t.Fatalf("stop from configured should be valid: %v", err)
	}

	// Pause from stopped (invalid: stopped → paused not allowed)
	if err := b.PauseVM(handle); err == nil {
		t.Error("expected error for pause from stopped")
	}

	// Resume from stopped (invalid: stopped has no transitions)
	if err := b.ResumeVM(handle); err == nil {
		t.Error("expected error for resume from stopped")
	}
}

func TestLXC_NotFound(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	if _, err := b.GetVM(99999); err == nil {
		t.Error("expected error for non-existent VM")
	}
	if err := b.DestroyVM(99999); err == nil {
		t.Error("expected error for non-existent VM")
	}
	if err := b.StartVM(99999); err == nil {
		t.Error("expected error for non-existent VM")
	}
}

func TestLXC_AvailableTemplates(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})
	templates := b.AvailableTemplates()
	if len(templates) < 4 {
		t.Fatalf("expected at least 4 templates, got %d", len(templates))
	}
	found := make(map[string]bool)
	for _, tmpl := range templates {
		found[tmpl] = true
	}
	for _, expected := range []string{"ubuntu", "alpine", "debian", "centos"} {
		if !found[expected] {
			t.Errorf("expected template %q not found", expected)
		}
	}
}

func TestLXC_CreateWithTemplate(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVMWithTemplate("alpine-ct", 1, 256, "alpine")
	if err != nil {
		t.Fatalf("create with template: %v", err)
	}
	if vm.Template != "alpine" {
		t.Errorf("expected template alpine, got %s", vm.Template)
	}
	if vm.Type != "container" {
		t.Errorf("expected type container, got %s", vm.Type)
	}
}

func TestLXC_ContainerStats(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, _ := b.CreateVM("stats-ct", 2, 1024)

	stats, err := b.GetContainerStats(vm.ID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.MemoryLimitBytes != 1024*1024*1024 {
		t.Errorf("expected memory limit %d, got %d", 1024*1024*1024, stats.MemoryLimitBytes)
	}
	if stats.PIDCount <= 0 {
		t.Error("expected positive PID count")
	}
	if stats.CPUUsageNs <= 0 {
		t.Error("expected positive CPU usage")
	}
}

func TestLXC_ContainerStats_NotFound(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	_, err := b.GetContainerStats(99999)
	if err == nil {
		t.Error("expected error for non-existent container")
	}
}

func TestLXC_NetworkSetup(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true, BridgeName: "testbr0"})

	vm, err := b.CreateVM("net-ct", 1, 256)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Verify IP assigned
	if vm.IPAddress == "" {
		t.Error("expected IP address to be assigned")
	}

	// Verify IP is in 10.0.0.x range
	if !strings.HasPrefix(vm.IPAddress, "10.0.0.") {
		t.Errorf("expected IP in 10.0.0.x range, got %s", vm.IPAddress)
	}

	// Verify config has bridge
	b.mu.RLock()
	cfg := b.configs[vm.ID]
	b.mu.RUnlock()
	if cfg.BridgeName != "testbr0" {
		t.Errorf("expected bridge testbr0, got %s", cfg.BridgeName)
	}
}

// ── LXC Config Tests ────────────────────────────────

func TestLXCConfig_Defaults(t *testing.T) {
	cfg := DefaultLXCConfig("test-ct")
	if cfg.Name != "test-ct" {
		t.Errorf("expected name test-ct, got %s", cfg.Name)
	}
	if cfg.Arch != "amd64" {
		t.Errorf("expected arch amd64, got %s", cfg.Arch)
	}
	if cfg.Distribution != "ubuntu" {
		t.Errorf("expected dist ubuntu, got %s", cfg.Distribution)
	}
	if cfg.NetworkType != "veth" {
		t.Errorf("expected network type veth, got %s", cfg.NetworkType)
	}
	if cfg.BridgeName != "hcvbr0" {
		t.Errorf("expected bridge hcvbr0, got %s", cfg.BridgeName)
	}
	if cfg.Privileged {
		t.Error("expected non-privileged by default")
	}
	if cfg.RootFS != "/var/lib/lxc/test-ct/rootfs" {
		t.Errorf("unexpected rootfs: %s", cfg.RootFS)
	}
}

func TestLXCConfig_Generate(t *testing.T) {
	cfg := DefaultLXCConfig("my-ct")
	cfg.VCPUs = 2
	cfg.MemoryMB = 1024
	cfg.IPAddress = "10.0.0.5"
	cfg.Gateway = "10.0.0.1"

	config := cfg.GenerateConfig()

	// Verify required lines
	expectedLines := []string{
		"lxc.rootfs.path = dir:/var/lib/lxc/my-ct/rootfs",
		"lxc.uts.name = my-ct",
		"lxc.net.0.type = veth",
		"lxc.net.0.link = hcvbr0",
		"lxc.net.0.flags = up",
		"lxc.net.0.ipv4.address = 10.0.0.5/24",
		"lxc.net.0.ipv4.gateway = 10.0.0.1",
		"lxc.cgroup2.memory.max = 1073741824",
		"lxc.cgroup2.cpu.max = 200000 100000",
		"lxc.arch = amd64",
	}
	for _, line := range expectedLines {
		if !strings.Contains(config, line) {
			t.Errorf("expected line %q in config, got:\n%s", line, config)
		}
	}
}

func TestLXCConfig_CgroupLimits(t *testing.T) {
	cfg := DefaultLXCConfig("cg-ct")
	cfg.VCPUs = 4
	cfg.MemoryMB = 2048

	limits := cfg.GenerateCgroupLimits()

	// Memory: 2048 MB = 2147483648 bytes
	if limits["lxc.cgroup2.memory.max"] != "2147483648" {
		t.Errorf("expected memory.max 2147483648, got %s", limits["lxc.cgroup2.memory.max"])
	}

	// CPU: 4 * 100000 = 400000 per 100000
	if limits["lxc.cgroup2.cpu.max"] != "400000 100000" {
		t.Errorf("expected cpu.max '400000 100000', got %s", limits["lxc.cgroup2.cpu.max"])
	}
}

func TestLXCConfig_GenerateNoIP(t *testing.T) {
	cfg := DefaultLXCConfig("noip-ct")
	cfg.IPAddress = ""

	config := cfg.GenerateConfig()
	if strings.Contains(config, "lxc.net.0.ipv4.address") {
		t.Error("expected no IP address line when IPAddress is empty")
	}
}

func TestLXCConfig_Mounts(t *testing.T) {
	cfg := DefaultLXCConfig("mount-ct")
	cfg.Mounts = []string{"/data /mnt/data none bind 0 0"}

	config := cfg.GenerateConfig()
	if !strings.Contains(config, "lxc.mount.entry = /data /mnt/data none bind 0 0") {
		t.Error("expected mount entry in config")
	}
}

func TestLXC_Name(t *testing.T) {
	b := NewLXCBackend(nil)
	if b.Name() != "lxc" {
		t.Errorf("expected name lxc, got %s", b.Name())
	}
}

func TestLXC_DefaultConfig(t *testing.T) {
	b := NewLXCBackend(nil)
	if !b.emulated {
		t.Error("expected emulated mode by default")
	}
	if b.lxcPath != "/var/lib/lxc" {
		t.Errorf("expected default lxcPath, got %s", b.lxcPath)
	}
}

// ── Phase 17 Tests ────────────────────────────────

// ── 17.1: ZFS Storage Integration ──

func TestLXC_ZFSRootfs(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	// Without storage driver, should still work
	vm, err := b.CreateVM("zfs-ct", 2, 1024)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if vm.RootFS == "" {
		t.Error("expected rootfs path")
	}

	// Set storage driver (nil is acceptable for emulated mode)
	b.SetStorageDriver(nil)

	// Destroy should work without storage driver
	if err := b.DestroyVM(vm.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func TestLXC_SnapshotContainer(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVM("snap-ct", 1, 256)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Create snapshot
	if err := b.SnapshotContainer(vm.ID, "snap-1"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Verify snapshot recorded
	b.mu.RLock()
	snaps := b.snapshots[vm.ID]
	b.mu.RUnlock()
	if len(snaps) != 1 || snaps[0] != "snap-1" {
		t.Errorf("expected snapshot snap-1, got %v", snaps)
	}

	// Create second snapshot
	if err := b.SnapshotContainer(vm.ID, "snap-2"); err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}

	b.mu.RLock()
	snaps = b.snapshots[vm.ID]
	b.mu.RUnlock()
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}

	// Non-existent container
	if err := b.SnapshotContainer(99999, "snap"); err == nil {
		t.Error("expected error for non-existent container")
	}
}

func TestLXC_CloneContainer(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVM("clone-src", 2, 512)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Clone without snapshot should fail
	_, err = b.CloneContainer(vm.ID, "clone-dst")
	if err == nil {
		t.Fatal("expected error when no snapshots exist")
	}

	// Create snapshot first
	if err := b.SnapshotContainer(vm.ID, "before-clone"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Clone
	cloneHandle, err := b.CloneContainer(vm.ID, "clone-dst")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// Verify clone
	clone, err := b.GetVM(cloneHandle)
	if err != nil {
		t.Fatalf("get clone: %v", err)
	}
	if clone.Name != "clone-dst" {
		t.Errorf("expected name clone-dst, got %s", clone.Name)
	}
	if clone.VCPUs != vm.VCPUs {
		t.Errorf("expected %d vcpus, got %d", vm.VCPUs, clone.VCPUs)
	}
	if clone.MemoryMB != vm.MemoryMB {
		t.Errorf("expected %d memory, got %d", vm.MemoryMB, clone.MemoryMB)
	}
	if clone.Type != "container" {
		t.Errorf("expected type container, got %s", clone.Type)
	}
	if clone.State != "configured" {
		t.Errorf("expected state configured, got %s", clone.State)
	}
	if cloneHandle < 20000 {
		t.Errorf("expected handle >= 20000, got %d", cloneHandle)
	}

	// Non-existent source
	_, err = b.CloneContainer(99999, "bad-clone")
	if err == nil {
		t.Error("expected error for non-existent container")
	}
}

// ── 17.2: Security Namespaces ──

func TestLXCConfig_SecurityDefaults(t *testing.T) {
	sec := DefaultSecurityConfig()
	if sec.Privileged {
		t.Error("expected non-privileged by default")
	}
	if sec.UIDMapStart != 100000 {
		t.Errorf("expected UID map start 100000, got %d", sec.UIDMapStart)
	}
	if sec.UIDMapSize != 65536 {
		t.Errorf("expected UID map size 65536, got %d", sec.UIDMapSize)
	}
	if sec.GIDMapStart != 100000 {
		t.Errorf("expected GID map start 100000, got %d", sec.GIDMapStart)
	}
	if sec.GIDMapSize != 65536 {
		t.Errorf("expected GID map size 65536, got %d", sec.GIDMapSize)
	}
	if sec.SeccompProfile != "/usr/share/lxc/config/common.seccomp" {
		t.Errorf("unexpected seccomp: %s", sec.SeccompProfile)
	}
	if sec.AppArmorProfile != "lxc-container-default-cgns" {
		t.Errorf("unexpected apparmor: %s", sec.AppArmorProfile)
	}

	// Verify defaults are applied to LXCConfig
	cfg := DefaultLXCConfig("sec-ct")
	if cfg.UIDMapStart != 100000 || cfg.UIDMapSize != 65536 {
		t.Error("expected security defaults in LXCConfig")
	}
}

func TestLXCConfig_Privileged(t *testing.T) {
	cfg := DefaultLXCConfig("priv-ct")
	cfg.Privileged = true

	config := cfg.GenerateConfig()
	if strings.Contains(config, "lxc.idmap") {
		t.Error("privileged container should not have idmap lines")
	}
}

func TestLXCConfig_Nesting(t *testing.T) {
	cfg := DefaultLXCConfig("nest-ct")
	cfg.Nesting = true

	config := cfg.GenerateConfig()
	if !strings.Contains(config, "lxc.mount.auto = cgroup:rw proc:mixed sys:mixed") {
		t.Error("expected nesting mount auto line")
	}
}

func TestLXCConfig_DropCaps(t *testing.T) {
	cfg := DefaultLXCConfig("cap-ct")
	cfg.DropCapabilities = []string{"sys_admin", "net_raw"}

	config := cfg.GenerateConfig()
	if !strings.Contains(config, "lxc.cap.drop = sys_admin") {
		t.Error("expected drop cap sys_admin")
	}
	if !strings.Contains(config, "lxc.cap.drop = net_raw") {
		t.Error("expected drop cap net_raw")
	}
}

func TestLXCConfig_SecurityInGenerateConfig(t *testing.T) {
	cfg := DefaultLXCConfig("full-sec-ct")

	config := cfg.GenerateConfig()

	// Should have idmap (unprivileged by default)
	if !strings.Contains(config, "lxc.idmap = u 0 100000 65536") {
		t.Error("expected UID idmap line")
	}
	if !strings.Contains(config, "lxc.idmap = g 0 100000 65536") {
		t.Error("expected GID idmap line")
	}
	// Should have seccomp
	if !strings.Contains(config, "lxc.seccomp.profile = /usr/share/lxc/config/common.seccomp") {
		t.Error("expected seccomp profile line")
	}
	// Should have apparmor
	if !strings.Contains(config, "lxc.apparmor.profile = lxc-container-default-cgns") {
		t.Error("expected apparmor profile line")
	}
	// Should NOT have nesting by default
	if strings.Contains(config, "lxc.mount.auto") {
		t.Error("expected no nesting mount auto by default")
	}
}

// ── 17.3: Migration (CRIU) ──

func TestLXC_Migration(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVM("migrate-ct", 1, 256)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Migration requires running state
	if err := b.MigrateContainer(vm.ID, "node-02"); err == nil {
		t.Error("expected error: container not running")
	}

	// Start container
	if err := b.StartVM(vm.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Same node should fail
	if err := b.MigrateContainer(vm.ID, "local"); err == nil {
		t.Error("expected error: already on same node")
	}

	// Migrate to different node
	if err := b.MigrateContainer(vm.ID, "node-02"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify node changed
	vm, _ = b.GetVM(vm.ID)
	if vm.Node != "node-02" {
		t.Errorf("expected node node-02, got %s", vm.Node)
	}

	// Non-existent container
	if err := b.MigrateContainer(99999, "node-02"); err == nil {
		t.Error("expected error for non-existent container")
	}
}

func TestLXC_Checkpoint(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVM("ckpt-ct", 1, 256)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Checkpoint (emulated is no-op)
	if err := b.CheckpointContainer(vm.ID, "/tmp/test-checkpoint"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// Restore
	if err := b.RestoreContainer(vm.ID, "/tmp/test-checkpoint"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	vm, _ = b.GetVM(vm.ID)
	if vm.State != "running" {
		t.Errorf("expected state running after restore, got %s", vm.State)
	}

	// Non-existent container
	if err := b.CheckpointContainer(99999, "/tmp/bad"); err == nil {
		t.Error("expected error for non-existent container")
	}
	if err := b.RestoreContainer(99999, "/tmp/bad"); err == nil {
		t.Error("expected error for non-existent container")
	}
}

// ── 17.4: Container Exec ──

func TestLXC_Exec(t *testing.T) {
	b := NewLXCBackend(&LXCBackendConfig{Emulated: true})

	vm, err := b.CreateVM("exec-ct", 1, 256)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Exec requires running state
	_, err = b.ExecContainer(vm.ID, []string{"ls", "-la"})
	if err == nil {
		t.Error("expected error: container not running")
	}

	// Start
	if err := b.StartVM(vm.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Exec
	output, err := b.ExecContainer(vm.ID, []string{"ls", "-la"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if output != "exec: ls -la" {
		t.Errorf("expected 'exec: ls -la', got %q", output)
	}

	// Exec with single command
	output, err = b.ExecContainer(vm.ID, []string{"hostname"})
	if err != nil {
		t.Fatalf("exec hostname: %v", err)
	}
	if output != "exec: hostname" {
		t.Errorf("expected 'exec: hostname', got %q", output)
	}

	// Non-existent container
	_, err = b.ExecContainer(99999, []string{"ls"})
	if err == nil {
		t.Error("expected error for non-existent container")
	}
}

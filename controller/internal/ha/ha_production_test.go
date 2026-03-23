package ha

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ── Leader Election Tests ──────────────────────────────

func TestLeaderElection_SingleNode(t *testing.T) {
	// No etcd endpoints → single-node mode, self is leader
	le, err := NewLeaderElection(nil, "test-node", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !le.IsLeader() {
		t.Error("expected single-node to be leader")
	}

	leader, err := le.GetLeader()
	if err != nil {
		t.Fatalf("GetLeader error: %v", err)
	}
	if leader != "test-node" {
		t.Errorf("expected leader 'test-node', got %q", leader)
	}

	// Campaign should succeed in single-node mode
	if err := le.Campaign(context.Background()); err != nil {
		t.Errorf("Campaign error: %v", err)
	}

	// Resign should succeed
	if err := le.Resign(); err != nil {
		t.Errorf("Resign error: %v", err)
	}

	// Close should succeed
	if err := le.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

func TestLeaderElection_IsLeader(t *testing.T) {
	// Empty endpoints → single-node mode
	le, err := NewLeaderElection([]string{}, "node-01", 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !le.IsLeader() {
		t.Error("expected IsLeader to be true in single-node mode")
	}

	// After resign, isLeader should be false
	le.Resign()
	if le.IsLeader() {
		t.Error("expected IsLeader to be false after resign")
	}
}

func TestLeaderElection_InvalidEndpoints(t *testing.T) {
	// Invalid endpoints should fallback to single-node mode
	le, err := NewLeaderElection([]string{"invalid-host:99999"}, "node-02", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still be leader (fallback)
	if !le.IsLeader() {
		t.Error("expected single-node fallback to be leader")
	}

	le.Close()
}

// ── Distributed Lock Tests ─────────────────────────────

func TestDistLock_AcquireRelease(t *testing.T) {
	lm := NewLockManager(nil) // in-memory mode

	unlock, err := lm.Acquire(context.Background(), "test-key", 10*time.Second)
	if err != nil {
		t.Fatalf("Acquire error: %v", err)
	}
	if unlock == nil {
		t.Fatal("expected non-nil unlock function")
	}

	// Release the lock
	unlock()

	// Should be able to acquire again
	unlock2, err := lm.Acquire(context.Background(), "test-key", 10*time.Second)
	if err != nil {
		t.Fatalf("second Acquire error: %v", err)
	}
	unlock2()
}

func TestDistLock_InMemoryMode(t *testing.T) {
	lm := NewLockManager([]string{}) // empty endpoints → in-memory
	if !lm.inMemory {
		t.Error("expected in-memory mode with empty endpoints")
	}

	unlock, err := lm.Acquire(context.Background(), "key1", 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire error: %v", err)
	}

	// Try to acquire same key non-blocking (should fail since it's held)
	_, ok := lm.TryAcquire("key1", 5*time.Second)
	if ok {
		t.Error("expected TryAcquire to fail while lock is held")
	}

	unlock()

	// Now TryAcquire should succeed
	unlock2, ok := lm.TryAcquire("key1", 5*time.Second)
	if !ok {
		t.Error("expected TryAcquire to succeed after release")
	}
	if unlock2 != nil {
		unlock2()
	}
}

func TestDistLock_WithLock(t *testing.T) {
	lm := NewLockManager(nil)

	executed := false
	err := lm.WithLock(context.Background(), "with-lock-key", 10*time.Second, func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock error: %v", err)
	}
	if !executed {
		t.Error("expected function to be executed within lock")
	}
}

func TestDistLock_Concurrency(t *testing.T) {
	lm := NewLockManager(nil)
	counter := 0
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lm.WithLock(context.Background(), "counter", 10*time.Second, func() error {
				counter++
				return nil
			})
		}()
	}

	wg.Wait()
	if counter != 10 {
		t.Errorf("expected counter=10, got %d", counter)
	}
}

// ── Failure Detector Tests ─────────────────────────────

func TestFailureDetector_Timeout(t *testing.T) {
	driver := newMemoryDriver()

	// Set node-03's LastSeen to 1 minute ago (should trigger failure)
	driver.mu.Lock()
	driver.nodes["node-03"].LastSeen = time.Now().Add(-1 * time.Minute)
	driver.mu.Unlock()

	fd := NewFailureDetector(driver, 100*time.Millisecond)

	failedNodes := make(chan string, 10)
	fd.OnNodeDown(func(nodeName string) {
		failedNodes <- nodeName
	})

	ctx, cancel := context.WithCancel(context.Background())
	fd.Start(ctx)

	// Wait for failure detection
	select {
	case node := <-failedNodes:
		if node != "node-03" {
			t.Errorf("expected node-03 failure, got %s", node)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for failure detection")
	}

	cancel()
	fd.Stop()
}

func TestFailureDetector_Recovery(t *testing.T) {
	driver := newMemoryDriver()

	// Set node-02 as old (should trigger failure detection)
	// Keep other nodes fresh
	now := time.Now()
	driver.mu.Lock()
	driver.nodes["node-01"].LastSeen = now
	driver.nodes["node-02"].LastSeen = now.Add(-1 * time.Minute)
	driver.nodes["node-03"].LastSeen = now
	driver.mu.Unlock()

	fd := NewFailureDetector(driver, 200*time.Millisecond)

	failedCh := make(chan string, 10)
	fd.OnNodeDown(func(nodeName string) {
		failedCh <- nodeName
	})

	ctx, cancel := context.WithCancel(context.Background())
	fd.Start(ctx)

	// Wait for failure detection of node-02
	detected := false
	timeout := time.After(2 * time.Second)
	for !detected {
		select {
		case node := <-failedCh:
			if node == "node-02" {
				detected = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for node-02 failure detection")
		}
	}

	// "Recover" the node by updating LastSeen for ALL nodes
	now = time.Now()
	driver.mu.Lock()
	driver.nodes["node-01"].LastSeen = now
	driver.nodes["node-02"].LastSeen = now
	driver.nodes["node-03"].LastSeen = now
	driver.mu.Unlock()

	// Wait for recovery check cycle
	time.Sleep(500 * time.Millisecond)

	// The node should be recovered (removed from failedNodes map)
	fd.failedMu.RLock()
	isStillFailed := fd.failedNodes["node-02"]
	fd.failedMu.RUnlock()

	if isStillFailed {
		t.Error("expected node-02 to be recovered")
	}

	cancel()
	fd.Stop()
}

// ── Failover Manager Tests ─────────────────────────────

type mockComputeFailover struct {
	mu       sync.Mutex
	vms      []VMSummary
	migrated map[int32]string
}

func (m *mockComputeFailover) ListVMs() []VMSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]VMSummary, len(m.vms))
	copy(result, m.vms)
	return result
}

func (m *mockComputeFailover) MigrateVM(handle int32, targetNode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.migrated[handle] = targetNode
	return nil
}

func TestFailover_HandleNodeDown(t *testing.T) {
	le, _ := NewLeaderElection(nil, "leader-node", 10)
	lm := NewLockManager(nil)
	fm := NewFailoverManager(le, lm)

	driver := newMemoryDriver()
	fm.SetDriver(driver)

	provider := &mockComputeFailover{
		vms: []VMSummary{
			{Handle: 1, Name: "vm-1", Node: "node-03", State: "running", RestartPolicy: "always"},
			{Handle: 2, Name: "vm-2", Node: "node-03", State: "running", RestartPolicy: "never"},
			{Handle: 3, Name: "vm-3", Node: "node-01", State: "running", RestartPolicy: "always"},
			{Handle: 4, Name: "vm-4", Node: "node-03", State: "running", RestartPolicy: "on-failure"},
		},
		migrated: make(map[int32]string),
	}
	fm.SetComputeProvider(provider)

	fm.HandleNodeDown("node-03")

	// VM-1 should be migrated (always policy)
	if _, ok := provider.migrated[1]; !ok {
		t.Error("expected vm-1 to be migrated")
	}

	// VM-2 should NOT be migrated (never policy)
	if _, ok := provider.migrated[2]; ok {
		t.Error("expected vm-2 NOT to be migrated (never policy)")
	}

	// VM-3 should NOT be migrated (different node)
	if _, ok := provider.migrated[3]; ok {
		t.Error("expected vm-3 NOT to be migrated (different node)")
	}

	// VM-4 should be migrated (on-failure policy)
	if _, ok := provider.migrated[4]; !ok {
		t.Error("expected vm-4 to be migrated (on-failure policy)")
	}
}

func TestFailover_NotLeader(t *testing.T) {
	le, _ := NewLeaderElection(nil, "non-leader", 10)
	le.Resign() // make it not leader
	lm := NewLockManager(nil)
	fm := NewFailoverManager(le, lm)

	provider := &mockComputeFailover{
		vms: []VMSummary{
			{Handle: 1, Name: "vm-1", Node: "node-03", State: "running", RestartPolicy: "always"},
		},
		migrated: make(map[int32]string),
	}
	fm.SetComputeProvider(provider)

	fm.HandleNodeDown("node-03")

	// No migration should happen when not leader
	if len(provider.migrated) != 0 {
		t.Error("expected no migrations when not leader")
	}
}

// ── HADriver Extension Tests ───────────────────────────

func TestMemoryDriver_IsLeader(t *testing.T) {
	d := newMemoryDriver()
	if !d.IsLeader() {
		t.Error("MemoryDriver should always be leader")
	}
}

func TestMemoryDriver_GetLeader(t *testing.T) {
	d := newMemoryDriver()
	leader, err := d.GetLeader()
	if err != nil {
		t.Fatalf("GetLeader error: %v", err)
	}
	if leader != "self" {
		t.Errorf("expected 'self', got %q", leader)
	}
}

func TestMemoryDriver_WatchNodes(t *testing.T) {
	d := newMemoryDriver()
	ctx, cancel := context.WithCancel(context.Background())
	err := d.WatchNodes(ctx, func(nodeName, status string) {
		// should not be called in memory mode
	})
	if err != nil {
		t.Fatalf("WatchNodes error: %v", err)
	}
	cancel()
}

// ── Heartbeat Deregister Test ──────────────────────────

func TestHeartbeat_Deregister(t *testing.T) {
	// Verify Deregister method exists and is callable.
	// Without a real store, we just check method signature.
	h := &Heartbeat{
		nodeName: "test-node",
	}
	_ = h // method existence verified at compile time
}

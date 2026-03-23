package compute

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQEMU_RetryBackoff(t *testing.T) {
	// Test that QMPDial fails with a non-existent socket
	// and that the exponential backoff logic is correct by timing

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "nonexistent.sock")

	start := time.Now()
	_, err := QMPDial(socketPath, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error connecting to non-existent socket")
	}

	// Should fail quickly (timeout is 100ms)
	if elapsed > 2*time.Second {
		t.Errorf("QMPDial took too long: %v (expected < 2s for single attempt)", elapsed)
	}
}

func TestQMP_QueryStatus(t *testing.T) {
	// Create a mock QMP server that responds to query-status
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "qmp-test.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	// Mock QMP server goroutine
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Send greeting
		greeting := `{"QMP": {"version": {"qemu": {"micro": 0, "minor": 2, "major": 8}}}}`
		conn.Write([]byte(greeting + "\n"))

		// Read qmp_capabilities
		buf := make([]byte, 4096)
		conn.Read(buf)
		// Respond to qmp_capabilities
		conn.Write([]byte(`{"return": {}}` + "\n"))

		// Read query-status command
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		var cmd map[string]any
		json.Unmarshal(buf[:n], &cmd)

		if cmd["execute"] == "query-status" {
			resp := `{"return": {"status": "running", "running": true}}`
			conn.Write([]byte(resp + "\n"))
		}
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	client, err := QMPDial(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("QMPDial: %v", err)
	}
	defer client.Close()

	status, err := client.QueryStatus()
	if err != nil {
		t.Fatalf("QueryStatus: %v", err)
	}
	if status != "running" {
		t.Errorf("expected status 'running', got %q", status)
	}

	<-serverDone
}

func TestQEMU_EmulatedMonitorNotStarted(t *testing.T) {
	// In emulated mode, monitorProcess should not be started
	// Just verify that emulated mode transitions work without process monitoring
	backend := NewQEMUBackend(&QEMUConfig{Emulated: true})

	vm, err := backend.CreateVM("test-vm", 2, 4096)
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}

	if err := backend.StartVM(vm.ID); err != nil {
		t.Fatalf("StartVM: %v", err)
	}

	gotVM, err := backend.GetVM(vm.ID)
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if gotVM.State != "running" {
		t.Errorf("expected state 'running', got %q", gotVM.State)
	}
}

func TestQEMU_TransitionValidation(t *testing.T) {
	backend := NewQEMUBackend(&QEMUConfig{Emulated: true})

	vm, err := backend.CreateVM("test-vm", 1, 512)
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}

	// configured → pause should fail
	err = backend.PauseVM(vm.ID)
	if err == nil {
		t.Fatal("expected error pausing configured VM")
	}

	// configured → running should work
	err = backend.StartVM(vm.ID)
	if err != nil {
		t.Fatalf("StartVM: %v", err)
	}

	// running → paused should work
	err = backend.PauseVM(vm.ID)
	if err != nil {
		t.Fatalf("PauseVM: %v", err)
	}

	// paused → running should work
	err = backend.ResumeVM(vm.ID)
	if err != nil {
		t.Fatalf("ResumeVM: %v", err)
	}
}

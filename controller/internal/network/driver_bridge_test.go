package network

import (
	"fmt"
	"strings"
	"testing"
)

// MockCommandRunner 는 실제 명령 실행 없이 명령 구성을 기록하는 테스트용 CommandRunner이다.
type MockCommandRunner struct {
	Commands []string // 실행된 명령어 기록 (형식: "name arg1 arg2 ...")
	FailOn   string   // 이 문자열을 포함하는 명령이면 에러 반환
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	cmd := name + " " + strings.Join(args, " ")
	m.Commands = append(m.Commands, cmd)
	if m.FailOn != "" && strings.Contains(cmd, m.FailOn) {
		return fmt.Errorf("mock error: %s", cmd)
	}
	return nil
}

func TestBridge_CreateCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.CreateBridge("br0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(mock.Commands))
	}

	expected0 := "ip link add name br0 type bridge"
	if mock.Commands[0] != expected0 {
		t.Errorf("command[0]: expected %q, got %q", expected0, mock.Commands[0])
	}

	expected1 := "ip link set br0 up"
	if mock.Commands[1] != expected1 {
		t.Errorf("command[1]: expected %q, got %q", expected1, mock.Commands[1])
	}
}

func TestBridge_DeleteCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.DeleteBridge("br0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.Commands))
	}

	expected := "ip link delete br0"
	if mock.Commands[0] != expected {
		t.Errorf("command[0]: expected %q, got %q", expected, mock.Commands[0])
	}
}

func TestBridge_AddPortCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.AddPort("br0", "eth0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.Commands))
	}

	expected := "ip link set eth0 master br0"
	if mock.Commands[0] != expected {
		t.Errorf("command[0]: expected %q, got %q", expected, mock.Commands[0])
	}
}

func TestBridge_SetBridgeIPCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.SetBridgeIP("br0", "10.0.0.1/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.Commands))
	}

	expected := "ip addr add 10.0.0.1/24 dev br0"
	if mock.Commands[0] != expected {
		t.Errorf("command[0]: expected %q, got %q", expected, mock.Commands[0])
	}
}

func TestVethPair_CreateCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	hostEnd, containerEnd, err := mgr.CreateVethPair("vm1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hostEnd != "vm1-h" {
		t.Errorf("hostEnd: expected %q, got %q", "vm1-h", hostEnd)
	}
	if containerEnd != "vm1-c" {
		t.Errorf("containerEnd: expected %q, got %q", "vm1-c", containerEnd)
	}

	if len(mock.Commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(mock.Commands))
	}

	expected0 := "ip link add vm1-h type veth peer name vm1-c"
	if mock.Commands[0] != expected0 {
		t.Errorf("command[0]: expected %q, got %q", expected0, mock.Commands[0])
	}

	expected1 := "ip link set vm1-h up"
	if mock.Commands[1] != expected1 {
		t.Errorf("command[1]: expected %q, got %q", expected1, mock.Commands[1])
	}

	expected2 := "ip link set vm1-c up"
	if mock.Commands[2] != expected2 {
		t.Errorf("command[2]: expected %q, got %q", expected2, mock.Commands[2])
	}
}

func TestVethPair_DeleteCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.DeleteVethPair("vm1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.Commands))
	}

	expected := "ip link delete vm1-h"
	if mock.Commands[0] != expected {
		t.Errorf("command[0]: expected %q, got %q", expected, mock.Commands[0])
	}
}

func TestBridge_CreateFailure(t *testing.T) {
	mock := &MockCommandRunner{FailOn: "add name"}
	mgr := NewBridgeManagerWithRunner(mock)

	err := mgr.CreateBridge("br0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create bridge br0") {
		t.Errorf("unexpected error message: %v", err)
	}
}

package network

import (
	"strings"
	"testing"
)

func TestVXLAN_CreateZoneCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	bridgeMgr := NewBridgeManagerWithRunner(mock)
	driver := NewNftablesDriverWithBridge(bridgeMgr)

	zone := &Zone{
		Name:     "overlay-1",
		ZoneType: "vxlan",
		MTU:      100, // Used as VNI
		Bridge:   "br-overlay",
	}

	err := driver.CreateZone(zone)
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	// Verify commands executed
	if len(mock.Commands) == 0 {
		t.Fatal("expected commands to be executed")
	}

	// Should contain VXLAN interface creation
	foundVxlan := false
	foundBridgeCreate := false
	foundMaster := false
	foundUp := false

	for _, cmd := range mock.Commands {
		if strings.Contains(cmd, "type vxlan") && strings.Contains(cmd, "id 100") && strings.Contains(cmd, "dport 4789") {
			foundVxlan = true
		}
		if strings.Contains(cmd, "add name br-overlay type bridge") {
			foundBridgeCreate = true
		}
		if strings.Contains(cmd, "master br-overlay") {
			foundMaster = true
		}
		if strings.Contains(cmd, "vxlan-overlay-1 up") {
			foundUp = true
		}
	}

	if !foundVxlan {
		t.Error("expected VXLAN interface creation command")
	}
	if !foundBridgeCreate {
		t.Error("expected bridge creation command")
	}
	if !foundMaster {
		t.Error("expected master assignment command")
	}
	if !foundUp {
		t.Error("expected VXLAN interface up command")
	}

	// Verify zone is in memory
	zones, _ := driver.ListZones()
	found := false
	for _, z := range zones {
		if z.Name == "overlay-1" && z.Status == "active" {
			found = true
		}
	}
	if !found {
		t.Error("zone should be stored in memory with active status")
	}
}

func TestVXLAN_SimpleZoneCmd(t *testing.T) {
	mock := &MockCommandRunner{}
	bridgeMgr := NewBridgeManagerWithRunner(mock)
	driver := NewNftablesDriverWithBridge(bridgeMgr)

	zone := &Zone{
		Name:     "simple-test",
		ZoneType: "simple",
		Bridge:   "br-simple",
	}

	err := driver.CreateZone(zone)
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	// Should only create bridge (no VXLAN commands)
	for _, cmd := range mock.Commands {
		if strings.Contains(cmd, "vxlan") {
			t.Error("simple zone should not create VXLAN interfaces")
		}
	}

	// Should have bridge creation
	foundBridge := false
	for _, cmd := range mock.Commands {
		if strings.Contains(cmd, "add name br-simple type bridge") {
			foundBridge = true
		}
	}
	if !foundBridge {
		t.Error("expected bridge creation for simple zone")
	}
}

func TestVXLAN_DeleteZone(t *testing.T) {
	mock := &MockCommandRunner{}
	bridgeMgr := NewBridgeManagerWithRunner(mock)
	driver := NewNftablesDriverWithBridge(bridgeMgr)

	zone := &Zone{
		Name:     "del-test",
		ZoneType: "simple",
		Bridge:   "br-del",
	}

	err := driver.CreateZone(zone)
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	// Clear command log to isolate delete commands
	mock.Commands = nil

	err = driver.DeleteZone("del-test")
	if err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}

	// Should have interface deletion command
	if len(mock.Commands) == 0 {
		t.Error("expected interface deletion commands")
	}

	// Verify zone is removed from memory
	zones, _ := driver.ListZones()
	for _, z := range zones {
		if z.Name == "del-test" {
			t.Error("zone should be deleted from memory")
		}
	}
}

func TestVXLAN_DeleteNonexistentZone(t *testing.T) {
	mock := &MockCommandRunner{}
	bridgeMgr := NewBridgeManagerWithRunner(mock)
	driver := NewNftablesDriverWithBridge(bridgeMgr)

	err := driver.DeleteZone("nonexistent")
	if err == nil {
		t.Fatal("expected error deleting nonexistent zone")
	}
}

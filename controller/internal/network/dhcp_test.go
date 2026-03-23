package network

import (
	"net"
	"strings"
	"testing"
)

func TestDHCP_AllocateRelease(t *testing.T) {
	mgr := NewDHCPManager()

	err := mgr.AddPool("test-pool", "10.0.1.0/24", "10.0.1.1",
		net.ParseIP("10.0.1.10"), net.ParseIP("10.0.1.20"))
	if err != nil {
		t.Fatalf("AddPool: %v", err)
	}

	// Allocate first IP
	ip1, err := mgr.AllocateIP("test-pool")
	if err != nil {
		t.Fatalf("AllocateIP: %v", err)
	}
	if ip1.String() != "10.0.1.10" {
		t.Errorf("expected 10.0.1.10, got %s", ip1.String())
	}

	// Allocate second IP
	ip2, err := mgr.AllocateIP("test-pool")
	if err != nil {
		t.Fatalf("AllocateIP: %v", err)
	}
	if ip2.String() != "10.0.1.11" {
		t.Errorf("expected 10.0.1.11, got %s", ip2.String())
	}

	// Verify leases
	leases := mgr.ListLeases("test-pool")
	if len(leases) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(leases))
	}

	// Release first IP
	err = mgr.ReleaseIP("test-pool", ip1)
	if err != nil {
		t.Fatalf("ReleaseIP: %v", err)
	}

	// Verify lease count decreased
	leases = mgr.ListLeases("test-pool")
	if len(leases) != 1 {
		t.Fatalf("expected 1 lease after release, got %d", len(leases))
	}

	// Re-allocate should return the released IP
	ip3, err := mgr.AllocateIP("test-pool")
	if err != nil {
		t.Fatalf("AllocateIP after release: %v", err)
	}
	if ip3.String() != "10.0.1.10" {
		t.Errorf("expected re-allocated 10.0.1.10, got %s", ip3.String())
	}
}

func TestDHCP_PoolExhaustion(t *testing.T) {
	mgr := NewDHCPManager()

	// Pool with only 3 IPs
	err := mgr.AddPool("small-pool", "10.0.2.0/24", "10.0.2.1",
		net.ParseIP("10.0.2.10"), net.ParseIP("10.0.2.12"))
	if err != nil {
		t.Fatalf("AddPool: %v", err)
	}

	// Allocate all 3 IPs
	for i := 0; i < 3; i++ {
		_, err := mgr.AllocateIP("small-pool")
		if err != nil {
			t.Fatalf("AllocateIP %d: %v", i, err)
		}
	}

	// 4th allocation should fail
	_, err = mgr.AllocateIP("small-pool")
	if err == nil {
		t.Fatal("expected pool exhaustion error, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDHCP_DnsmasqConfig(t *testing.T) {
	mgr := NewDHCPManager()

	err := mgr.AddPool("prod-pool", "10.0.1.0/24", "10.0.1.1",
		net.ParseIP("10.0.1.10"), net.ParseIP("10.0.1.200"))
	if err != nil {
		t.Fatalf("AddPool: %v", err)
	}

	config := mgr.GenerateDnsmasqConfig("prod-pool", "br0")

	if !strings.Contains(config, "interface=br0") {
		t.Error("config should contain interface=br0")
	}
	if !strings.Contains(config, "dhcp-range=10.0.1.10,10.0.1.200,24h") {
		t.Error("config should contain dhcp-range")
	}
	if !strings.Contains(config, "dhcp-option=option:router,10.0.1.1") {
		t.Error("config should contain dhcp-option router")
	}
	if !strings.Contains(config, "bind-interfaces") {
		t.Error("config should contain bind-interfaces")
	}
}

func TestDHCP_DuplicatePool(t *testing.T) {
	mgr := NewDHCPManager()

	err := mgr.AddPool("dup", "10.0.0.0/24", "10.0.0.1",
		net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.20"))
	if err != nil {
		t.Fatalf("AddPool: %v", err)
	}

	err = mgr.AddPool("dup", "10.0.0.0/24", "10.0.0.1",
		net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.20"))
	if err == nil {
		t.Fatal("expected duplicate pool error, got nil")
	}
}

func TestDHCP_ReleaseNonAllocated(t *testing.T) {
	mgr := NewDHCPManager()

	err := mgr.AddPool("pool", "10.0.0.0/24", "10.0.0.1",
		net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.20"))
	if err != nil {
		t.Fatalf("AddPool: %v", err)
	}

	err = mgr.ReleaseIP("pool", net.ParseIP("10.0.0.15"))
	if err == nil {
		t.Fatal("expected error releasing non-allocated IP, got nil")
	}
}

func TestDHCP_NonexistentPool(t *testing.T) {
	mgr := NewDHCPManager()

	_, err := mgr.AllocateIP("nonexistent")
	if err == nil {
		t.Fatal("expected pool not found error, got nil")
	}

	config := mgr.GenerateDnsmasqConfig("nonexistent", "br0")
	if config != "" {
		t.Errorf("expected empty config for nonexistent pool, got %q", config)
	}
}

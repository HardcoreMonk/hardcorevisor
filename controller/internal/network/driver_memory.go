package network

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// MemoryDriver is an in-memory network driver for dev/test.
type MemoryDriver struct {
	mu         sync.RWMutex
	zones      map[string]*Zone
	vnets      map[string]*VNet
	rules      map[string]*FirewallRule
	nextVNetID atomic.Int32
	nextRuleID atomic.Int32
}

// newMemoryDriver creates a MemoryDriver with default zones and vnets.
func newMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		zones: make(map[string]*Zone),
		vnets: make(map[string]*VNet),
		rules: make(map[string]*FirewallRule),
	}
	d.nextVNetID.Store(1)
	d.nextRuleID.Store(1)

	// Default zones
	d.zones["vxlan-zone"] = &Zone{
		Name: "vxlan-zone", ZoneType: "vxlan", MTU: 1450,
		Bridge: "vmbr1", Status: "active",
	}
	d.zones["simple-zone"] = &Zone{
		Name: "simple-zone", ZoneType: "simple", MTU: 1500,
		Bridge: "vmbr0", Status: "active",
	}

	// Default VNets
	d.vnets["vnet-1"] = &VNet{
		ID: "vnet-1", Zone: "vxlan-zone", Name: "prod-network",
		Tag: 100, Subnet: "10.0.1.0/24", Status: "active",
	}
	d.vnets["vnet-2"] = &VNet{
		ID: "vnet-2", Zone: "simple-zone", Name: "mgmt-network",
		Tag: 1, Subnet: "192.168.1.0/24", Status: "active",
	}

	return d
}

func (d *MemoryDriver) Name() string { return "memory" }

func (d *MemoryDriver) ListZones() ([]*Zone, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Zone, 0, len(d.zones))
	for _, z := range d.zones {
		result = append(result, z)
	}
	return result, nil
}

func (d *MemoryDriver) ListVNets(zone string) ([]*VNet, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*VNet, 0)
	for _, v := range d.vnets {
		if zone == "" || v.Zone == zone {
			result = append(result, v)
		}
	}
	return result, nil
}

func (d *MemoryDriver) CreateVNet(zone, name, subnet string, tag int) (*VNet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.zones[zone]; !ok {
		return nil, fmt.Errorf("zone not found: %s", zone)
	}
	id := fmt.Sprintf("vnet-%d", d.nextVNetID.Add(1)-1)
	vnet := &VNet{
		ID: id, Zone: zone, Name: name,
		Tag: tag, Subnet: subnet, Status: "active",
	}
	d.vnets[id] = vnet
	return vnet, nil
}

func (d *MemoryDriver) DeleteVNet(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.vnets[id]; !ok {
		return fmt.Errorf("vnet not found: %s", id)
	}
	delete(d.vnets, id)
	return nil
}

func (d *MemoryDriver) ListFirewallRules() ([]*FirewallRule, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*FirewallRule, 0, len(d.rules))
	for _, r := range d.rules {
		result = append(result, r)
	}
	return result, nil
}

func (d *MemoryDriver) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := fmt.Sprintf("rule-%d", d.nextRuleID.Add(1)-1)
	rule := &FirewallRule{
		ID: id, Direction: direction, Action: action,
		Protocol: protocol, Source: source, Dest: dest,
		DPort: dport, Comment: comment, Enabled: true,
	}
	d.rules[id] = rule
	return rule, nil
}

func (d *MemoryDriver) DeleteFirewallRule(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.rules[id]; !ok {
		return fmt.Errorf("rule not found: %s", id)
	}
	delete(d.rules, id)
	return nil
}

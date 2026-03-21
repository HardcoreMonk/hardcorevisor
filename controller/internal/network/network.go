// Package network — SDN Controller for VXLAN/EVPN/firewall management
//
// In-memory implementation for dev/test. Manages SDN zones,
// virtual networks, and firewall rules.
package network

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// ── Types ────────────────────────────────────────────

// Zone represents an SDN zone (VXLAN, VLAN, Simple, etc.)
type Zone struct {
	Name     string `json:"name"`
	ZoneType string `json:"type"`   // vxlan, vlan, simple, evpn
	MTU      int    `json:"mtu"`
	Bridge   string `json:"bridge"`
	Status   string `json:"status"` // active, pending, error
}

// VNet represents a virtual network within a zone
type VNet struct {
	ID     string `json:"id"`
	Zone   string `json:"zone"`
	Name   string `json:"name"`
	Tag    int    `json:"tag"` // VLAN tag or VXLAN VNI
	Subnet string `json:"subnet"`
	Status string `json:"status"`
}

// FirewallRule represents an nftables-style firewall rule
type FirewallRule struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // in, out
	Action    string `json:"action"`    // accept, drop, reject
	Protocol  string `json:"protocol"`  // tcp, udp, icmp
	Source    string `json:"source"`
	Dest     string `json:"dest"`
	DPort    string `json:"dport"`
	Comment  string `json:"comment"`
	Enabled  bool   `json:"enabled"`
}

// ── Service ──────────────────────────────────────────

// Service manages SDN zones, virtual networks, and firewall rules.
type Service struct {
	mu        sync.RWMutex
	zones     map[string]*Zone
	vnets     map[string]*VNet
	rules     map[string]*FirewallRule
	nextVNetID atomic.Int32
	nextRuleID atomic.Int32
}

// NewService creates a network service with default zones.
func NewService() *Service {
	s := &Service{
		zones: make(map[string]*Zone),
		vnets: make(map[string]*VNet),
		rules: make(map[string]*FirewallRule),
	}
	s.nextVNetID.Store(1)
	s.nextRuleID.Store(1)

	// Default zones
	s.zones["vxlan-zone"] = &Zone{
		Name: "vxlan-zone", ZoneType: "vxlan", MTU: 1450,
		Bridge: "vmbr1", Status: "active",
	}
	s.zones["simple-zone"] = &Zone{
		Name: "simple-zone", ZoneType: "simple", MTU: 1500,
		Bridge: "vmbr0", Status: "active",
	}

	// Default VNets
	s.vnets["vnet-1"] = &VNet{
		ID: "vnet-1", Zone: "vxlan-zone", Name: "prod-network",
		Tag: 100, Subnet: "10.0.1.0/24", Status: "active",
	}
	s.vnets["vnet-2"] = &VNet{
		ID: "vnet-2", Zone: "simple-zone", Name: "mgmt-network",
		Tag: 1, Subnet: "192.168.1.0/24", Status: "active",
	}

	return s
}

// ListZones returns all SDN zones.
func (s *Service) ListZones() []*Zone {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Zone, 0, len(s.zones))
	for _, z := range s.zones {
		result = append(result, z)
	}
	return result
}

// ListVNets returns all virtual networks, optionally filtered by zone.
func (s *Service) ListVNets(zone string) []*VNet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*VNet, 0)
	for _, v := range s.vnets {
		if zone == "" || v.Zone == zone {
			result = append(result, v)
		}
	}
	return result
}

// CreateVNet creates a virtual network in the specified zone.
func (s *Service) CreateVNet(zone, name, subnet string, tag int) (*VNet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.zones[zone]; !ok {
		return nil, fmt.Errorf("zone not found: %s", zone)
	}
	id := fmt.Sprintf("vnet-%d", s.nextVNetID.Add(1)-1)
	vnet := &VNet{
		ID: id, Zone: zone, Name: name,
		Tag: tag, Subnet: subnet, Status: "active",
	}
	s.vnets[id] = vnet
	return vnet, nil
}

// DeleteVNet removes a virtual network by ID.
func (s *Service) DeleteVNet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vnets[id]; !ok {
		return fmt.Errorf("vnet not found: %s", id)
	}
	delete(s.vnets, id)
	return nil
}

// ListFirewallRules returns all firewall rules.
func (s *Service) ListFirewallRules() []*FirewallRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*FirewallRule, 0, len(s.rules))
	for _, r := range s.rules {
		result = append(result, r)
	}
	return result
}

// CreateFirewallRule adds a new firewall rule.
func (s *Service) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := fmt.Sprintf("rule-%d", s.nextRuleID.Add(1)-1)
	rule := &FirewallRule{
		ID: id, Direction: direction, Action: action,
		Protocol: protocol, Source: source, Dest: dest,
		DPort: dport, Comment: comment, Enabled: true,
	}
	s.rules[id] = rule
	return rule, nil
}

// DeleteFirewallRule removes a firewall rule by ID.
func (s *Service) DeleteFirewallRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("rule not found: %s", id)
	}
	delete(s.rules, id)
	return nil
}

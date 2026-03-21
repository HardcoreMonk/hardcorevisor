// Package network — SDN Controller for VXLAN/EVPN/firewall management
//
// Pluggable driver architecture: MemoryDriver (dev/test) and
// NftablesDriver (nft CLI integration for firewall rules).
package network

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
	driver NetworkDriver
}

// NewService creates a network service with the default in-memory driver.
func NewService() *Service {
	return NewServiceWithDriver(newMemoryDriver())
}

// NewServiceWithDriver creates a network service with the specified driver.
func NewServiceWithDriver(driver NetworkDriver) *Service {
	return &Service{driver: driver}
}

// ListZones returns all SDN zones.
func (s *Service) ListZones() []*Zone {
	zones, _ := s.driver.ListZones()
	return zones
}

// ListVNets returns all virtual networks, optionally filtered by zone.
func (s *Service) ListVNets(zone string) []*VNet {
	vnets, _ := s.driver.ListVNets(zone)
	return vnets
}

// CreateVNet creates a virtual network in the specified zone.
func (s *Service) CreateVNet(zone, name, subnet string, tag int) (*VNet, error) {
	return s.driver.CreateVNet(zone, name, subnet, tag)
}

// DeleteVNet removes a virtual network by ID.
func (s *Service) DeleteVNet(id string) error {
	return s.driver.DeleteVNet(id)
}

// ListFirewallRules returns all firewall rules.
func (s *Service) ListFirewallRules() []*FirewallRule {
	rules, _ := s.driver.ListFirewallRules()
	return rules
}

// CreateFirewallRule adds a new firewall rule.
func (s *Service) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	return s.driver.CreateFirewallRule(direction, action, protocol, source, dest, dport, comment)
}

// DeleteFirewallRule removes a firewall rule by ID.
func (s *Service) DeleteFirewallRule(id string) error {
	return s.driver.DeleteFirewallRule(id)
}

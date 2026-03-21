// Package network — pluggable network backend driver interface
package network

// NetworkDriver is the interface for pluggable network backends.
type NetworkDriver interface {
	Name() string
	ListZones() ([]*Zone, error)
	ListVNets(zone string) ([]*VNet, error)
	CreateVNet(zone, name, subnet string, tag int) (*VNet, error)
	DeleteVNet(id string) error
	ListFirewallRules() ([]*FirewallRule, error)
	CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error)
	DeleteFirewallRule(id string) error
}

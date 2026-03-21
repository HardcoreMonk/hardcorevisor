package network

import (
	"fmt"
	"os/exec"
	"strings"
)

// NftablesDriver manages firewall rules via nft CLI.
// Zones and VNets remain in-memory (nftables only handles firewall rules).
type NftablesDriver struct {
	MemoryDriver // embed for zones/vnets
	tableName    string
	chainName    string
}

// NewNftablesDriver creates an NftablesDriver with default table/chain names.
func NewNftablesDriver() *NftablesDriver {
	d := &NftablesDriver{
		tableName: "hcv_filter",
		chainName: "hcv_forward",
	}
	d.MemoryDriver = *newMemoryDriver()
	return d
}

func (d *NftablesDriver) Name() string { return "nftables" }

// ensureTable creates the nftables table and chain if they don't exist.
func (d *NftablesDriver) ensureTable() error {
	cmds := []string{
		fmt.Sprintf("add table inet %s", d.tableName),
		fmt.Sprintf("add chain inet %s %s { type filter hook forward priority 0 ; policy accept ; }", d.tableName, d.chainName),
	}
	for _, cmd := range cmds {
		exec.Command("nft", strings.Fields(cmd)...).Run() // ignore errors (may already exist)
	}
	return nil
}

// CreateFirewallRule creates a firewall rule in memory and applies it via nft.
func (d *NftablesDriver) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	// Create in memory first
	rule, err := d.MemoryDriver.CreateFirewallRule(direction, action, protocol, source, dest, dport, comment)
	if err != nil {
		return nil, err
	}

	// Apply via nft (best effort -- may fail without permissions)
	nftRule := buildNftRule(direction, action, protocol, source, dest, dport, comment)
	if err := d.ensureTable(); err != nil {
		// Log warning but don't fail
		return rule, nil
	}
	cmd := fmt.Sprintf("add rule inet %s %s %s", d.tableName, d.chainName, nftRule)
	if out, err := exec.Command("nft", strings.Fields(cmd)...).CombinedOutput(); err != nil {
		// Log but don't fail -- rule exists in memory even if nft fails
		rule.Enabled = false // mark as not applied
		_ = out
	}

	return rule, nil
}

// buildNftRule constructs an nftables rule string from components.
func buildNftRule(direction, action, protocol, source, dest, dport, comment string) string {
	var parts []string
	if protocol != "" {
		parts = append(parts, protocol)
	}
	if source != "" {
		parts = append(parts, "ip saddr", source)
	}
	if dest != "" {
		parts = append(parts, "ip daddr", dest)
	}
	if dport != "" {
		parts = append(parts, "dport", dport)
	}
	parts = append(parts, action)
	if comment != "" {
		parts = append(parts, fmt.Sprintf("comment \"%s\"", comment))
	}
	return strings.Join(parts, " ")
}

// DeleteFirewallRule removes a firewall rule from memory.
// Note: nftables rule deletion requires handle number which we don't track.
// In production, we'd flush and re-apply all rules.
func (d *NftablesDriver) DeleteFirewallRule(id string) error {
	// Delete from memory
	if err := d.MemoryDriver.DeleteFirewallRule(id); err != nil {
		return err
	}
	return nil
}

// Package main — hcvctl: HardCoreVisor CLI management tool
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	version       = "dev"
	commit        = "none"
	buildDate     = "unknown"
	apiAddr       = "http://localhost:8080"
	outputFormat  = "table"
	tlsSkip       bool
	authUser      string
	authPass      string
	httpClient    *http.Client
)

func initHTTPClient() {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsSkip {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	httpClient = &http.Client{Transport: transport}
}

func main() {
	root := &cobra.Command{
		Use:   "hcvctl",
		Short: "HardCoreVisor CLI management tool",
		Long:  "hcvctl controls and monitors HardCoreVisor clusters via REST API.",
	}

	root.PersistentFlags().StringVar(&apiAddr, "api", apiAddr, "Controller API address")
	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml")
	root.PersistentFlags().BoolVar(&tlsSkip, "tls-skip-verify", false, "Skip TLS certificate verification")
	root.PersistentFlags().StringVar(&authUser, "user", "", "Basic auth username")
	root.PersistentFlags().StringVar(&authPass, "password", "", "Basic auth password")

	cobra.OnInitialize(initHTTPClient)

	// ── vm subcommand ──
	vmCmd := &cobra.Command{Use: "vm", Short: "Manage virtual machines"}

	vmCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all VMs",
		RunE:  vmList,
	})
	vmCmd.AddCommand(&cobra.Command{
		Use:   "create [name]",
		Short: "Create a new VM",
		Args:  cobra.ExactArgs(1),
		RunE:  vmCreate,
	})
	vmCmd.AddCommand(&cobra.Command{
		Use:   "start [id]",
		Short: "Start a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vmAction(args[0], "start")
		},
	})
	vmCmd.AddCommand(&cobra.Command{
		Use:   "stop [id]",
		Short: "Stop a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vmAction(args[0], "stop")
		},
	})

	// ── node subcommand ──
	nodeCmd := &cobra.Command{Use: "node", Short: "Manage cluster nodes"}
	nodeCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List cluster nodes",
		RunE:  nodeList,
	})

	// ── version subcommand ──
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Show version info",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("hcvctl %s (commit: %s, built: %s)\n", version, commit, buildDate)
		},
	}

	// ── storage subcommand ──
	storageCmd := &cobra.Command{Use: "storage", Short: "Manage storage pools and volumes"}

	storagePoolCmd := &cobra.Command{Use: "pool", Short: "Manage storage pools"}
	storagePoolCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List storage pools",
		RunE:  storagePoolList,
	})

	storageVolumeCmd := &cobra.Command{Use: "volume", Short: "Manage storage volumes"}
	storageVolumeCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List storage volumes",
		RunE:  storageVolumeList,
	})

	volCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a storage volume",
		RunE:  storageVolumeCreate,
	}
	volCreateCmd.Flags().String("pool", "", "Storage pool name")
	volCreateCmd.Flags().String("name", "", "Volume name")
	volCreateCmd.Flags().Uint64("size", 0, "Volume size in bytes")
	volCreateCmd.Flags().String("format", "qcow2", "Volume format (qcow2, raw, zvol)")
	storageVolumeCmd.AddCommand(volCreateCmd)

	storageCmd.AddCommand(storagePoolCmd, storageVolumeCmd)

	// ── network subcommand ──
	networkCmd := &cobra.Command{Use: "network", Short: "Manage SDN zones and virtual networks"}

	networkZoneCmd := &cobra.Command{Use: "zone", Short: "Manage SDN zones"}
	networkZoneCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List SDN zones",
		RunE:  networkZoneList,
	})

	networkVnetCmd := &cobra.Command{Use: "vnet", Short: "Manage virtual networks"}
	networkVnetCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List virtual networks",
		RunE:  networkVnetList,
	})

	networkFirewallCmd := &cobra.Command{Use: "firewall", Short: "Manage firewall rules"}
	networkFirewallCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List firewall rules",
		RunE:  networkFirewallList,
	})

	networkCmd.AddCommand(networkZoneCmd, networkVnetCmd, networkFirewallCmd)

	// ── device subcommand ──
	deviceCmd := &cobra.Command{Use: "device", Short: "Manage peripheral devices"}

	deviceListCmd := &cobra.Command{
		Use:   "list",
		Short: "List devices",
		RunE:  deviceList,
	}
	deviceListCmd.Flags().String("type", "", "Filter by device type (gpu, nic, usb, disk)")
	deviceCmd.AddCommand(deviceListCmd)

	deviceAttachCmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach a device to a VM",
		RunE:  deviceAttach,
	}
	deviceAttachCmd.Flags().String("id", "", "Device ID")
	deviceAttachCmd.Flags().Int32("vm", 0, "VM handle to attach to")
	deviceCmd.AddCommand(deviceAttachCmd)

	deviceDetachCmd := &cobra.Command{
		Use:   "detach",
		Short: "Detach a device from a VM",
		RunE:  deviceDetach,
	}
	deviceDetachCmd.Flags().String("id", "", "Device ID")
	deviceCmd.AddCommand(deviceDetachCmd)

	// ── cluster subcommand ──
	clusterCmd := &cobra.Command{Use: "cluster", Short: "Manage HA cluster"}

	clusterCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE:  clusterStatus,
	})

	clusterNodeCmd := &cobra.Command{Use: "node", Short: "Manage cluster nodes"}
	clusterNodeCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List cluster nodes",
		RunE:  clusterNodeList,
	})
	clusterCmd.AddCommand(clusterNodeCmd)

	clusterFenceCmd := &cobra.Command{
		Use:   "fence",
		Short: "Fence a cluster node",
		RunE:  clusterFence,
	}
	clusterFenceCmd.Flags().String("node", "", "Node name to fence")
	clusterFenceCmd.Flags().String("reason", "", "Reason for fencing")
	clusterFenceCmd.Flags().String("action", "reboot", "Fence action (reboot, off, on)")
	clusterCmd.AddCommand(clusterFenceCmd)

	root.AddCommand(vmCmd, nodeCmd, versionCmd, storageCmd, networkCmd, deviceCmd, clusterCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── API helpers ──────────────────────────────────────────

func newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, apiAddr+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authUser != "" {
		req.SetBasicAuth(authUser, authPass)
	}
	return req, nil
}

func apiGet(path string) (*http.Response, error) {
	req, err := newRequest("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	return resp, nil
}

func apiPost(path string, body interface{}) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal error: %w", err)
		}
		r = bytes.NewReader(data)
	}
	req, err := newRequest("POST", path, r)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	return resp, nil
}

func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// printOutput handles --output flag for list commands.
// data is the raw API response for json/yaml, headers+rows for table.
func printOutput(data any, headers []string, rows [][]string) {
	switch outputFormat {
	case "json":
		out, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(out))
	case "yaml":
		out, _ := yaml.Marshal(data)
		fmt.Print(string(out))
	default: // table
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.Join(headers, "\t"))
		for _, row := range rows {
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		tw.Flush()
	}
}

// ── VM handlers ──────────────────────────────────────────

func vmList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/vms")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var vms []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		State    string `json:"state"`
		VCPUs    int    `json:"vcpus"`
		MemoryMB int    `json:"memory_mb"`
		Node     string `json:"node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "NAME", "STATE", "VCPUS", "MEMORY", "NODE"}
	var rows [][]string
	for _, vm := range vms {
		rows = append(rows, []string{
			fmt.Sprintf("%d", vm.ID), vm.Name, vm.State,
			fmt.Sprintf("%d", vm.VCPUs), fmt.Sprintf("%dMB", vm.MemoryMB), vm.Node,
		})
	}
	printOutput(vms, headers, rows)
	return nil
}

func vmCreate(cmd *cobra.Command, args []string) error {
	body := fmt.Sprintf(`{"name":"%s","vcpus":2,"memory_mb":4096}`, args[0])
	resp, err := http.Post(apiAddr+"/api/v1/vms", "application/json",
		io.NopCloser(io.Reader(nil)))
	_ = body
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()
	fmt.Printf("VM '%s' created.\n", args[0])
	return nil
}

func vmAction(id, action string) error {
	resp, err := http.Post(fmt.Sprintf("%s/api/v1/vms/%s/%s", apiAddr, id, action),
		"application/json", nil)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()
	fmt.Printf("VM %s: %s OK\n", id, action)
	return nil
}

// ── Node handlers ────────────────────────────────────────

func nodeList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/nodes")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var nodes []struct {
		Name          string  `json:"name"`
		Status        string  `json:"status"`
		CPUPercent    float64 `json:"cpu_percent"`
		MemoryPercent float64 `json:"memory_percent"`
		VMCount       int     `json:"vm_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"NAME", "STATUS", "CPU%", "MEM%", "VMs"}
	var rows [][]string
	for _, n := range nodes {
		rows = append(rows, []string{
			n.Name, n.Status,
			fmt.Sprintf("%.1f%%", n.CPUPercent),
			fmt.Sprintf("%.1f%%", n.MemoryPercent),
			fmt.Sprintf("%d", n.VMCount),
		})
	}
	printOutput(nodes, headers, rows)
	return nil
}

// ── Storage handlers ─────────────────────────────────────

func storagePoolList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/storage/pools")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var pools []struct {
		Name       string `json:"name"`
		PoolType   string `json:"pool_type"`
		TotalBytes uint64 `json:"total_bytes"`
		UsedBytes  uint64 `json:"used_bytes"`
		Health     string `json:"health"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pools); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"NAME", "TYPE", "TOTAL", "USED", "HEALTH"}
	var rows [][]string
	for _, p := range pools {
		rows = append(rows, []string{
			p.Name, p.PoolType, formatBytes(p.TotalBytes), formatBytes(p.UsedBytes), p.Health,
		})
	}
	printOutput(pools, headers, rows)
	return nil
}

func storageVolumeList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/storage/volumes")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var volumes []struct {
		ID        string `json:"id"`
		Pool      string `json:"pool"`
		Name      string `json:"name"`
		SizeBytes uint64 `json:"size_bytes"`
		Format    string `json:"format"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&volumes); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "POOL", "NAME", "SIZE", "FORMAT", "PATH"}
	var rows [][]string
	for _, v := range volumes {
		rows = append(rows, []string{
			v.ID, v.Pool, v.Name, formatBytes(v.SizeBytes), v.Format, v.Path,
		})
	}
	printOutput(volumes, headers, rows)
	return nil
}

func storageVolumeCreate(cmd *cobra.Command, args []string) error {
	pool, _ := cmd.Flags().GetString("pool")
	name, _ := cmd.Flags().GetString("name")
	size, _ := cmd.Flags().GetUint64("size")
	format, _ := cmd.Flags().GetString("format")

	body := map[string]interface{}{
		"pool":       pool,
		"name":       name,
		"size_bytes": size,
		"format":     format,
	}
	resp, err := apiPost("/api/v1/storage/volumes", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Volume '%s' created in pool '%s'.\n", name, pool)
	return nil
}

// ── Network handlers ─────────────────────────────────────

func networkZoneList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/network/zones")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var zones []struct {
		Name     string `json:"name"`
		ZoneType string `json:"type"`
		MTU      int    `json:"mtu"`
		Bridge   string `json:"bridge"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"NAME", "TYPE", "MTU", "BRIDGE", "STATUS"}
	var rows [][]string
	for _, z := range zones {
		rows = append(rows, []string{
			z.Name, z.ZoneType, fmt.Sprintf("%d", z.MTU), z.Bridge, z.Status,
		})
	}
	printOutput(zones, headers, rows)
	return nil
}

func networkVnetList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/network/vnets")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var vnets []struct {
		ID     string `json:"id"`
		Zone   string `json:"zone"`
		Name   string `json:"name"`
		Tag    int    `json:"tag"`
		Subnet string `json:"subnet"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&vnets); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "ZONE", "NAME", "TAG", "SUBNET", "STATUS"}
	var rows [][]string
	for _, v := range vnets {
		rows = append(rows, []string{
			v.ID, v.Zone, v.Name, fmt.Sprintf("%d", v.Tag), v.Subnet, v.Status,
		})
	}
	printOutput(vnets, headers, rows)
	return nil
}

func networkFirewallList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/network/firewall")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var rules []struct {
		ID        string `json:"id"`
		Direction string `json:"direction"`
		Action    string `json:"action"`
		Protocol  string `json:"protocol"`
		Source    string `json:"source"`
		Dest     string `json:"dest"`
		DPort    string `json:"dport"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rules); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "DIR", "ACTION", "PROTO", "SOURCE", "DEST", "DPORT", "ENABLED"}
	var rows [][]string
	for _, r := range rules {
		rows = append(rows, []string{
			r.ID, r.Direction, r.Action, r.Protocol, r.Source, r.Dest, r.DPort, fmt.Sprintf("%v", r.Enabled),
		})
	}
	printOutput(rules, headers, rows)
	return nil
}

// ── Device handlers ──────────────────────────────────────

func deviceList(cmd *cobra.Command, args []string) error {
	typeFilter, _ := cmd.Flags().GetString("type")
	path := "/api/v1/devices"
	if typeFilter != "" {
		path += "?type=" + typeFilter
	}
	resp, err := apiGet(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var devices []struct {
		ID          string `json:"id"`
		DeviceType  string `json:"device_type"`
		Description string `json:"description"`
		PCIAddress  string `json:"pci_address"`
		AttachedVM  int32  `json:"attached_vm"`
		IOMMU       string `json:"iommu_group"`
		Driver      string `json:"driver"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "TYPE", "DESCRIPTION", "PCI", "ATTACHED_VM", "IOMMU", "DRIVER"}
	var rows [][]string
	for _, d := range devices {
		vmStr := "-"
		if d.AttachedVM != 0 {
			vmStr = fmt.Sprintf("%d", d.AttachedVM)
		}
		rows = append(rows, []string{
			d.ID, d.DeviceType, d.Description, d.PCIAddress, vmStr, d.IOMMU, d.Driver,
		})
	}
	printOutput(devices, headers, rows)
	return nil
}

func deviceAttach(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	vmHandle, _ := cmd.Flags().GetInt32("vm")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	body := map[string]interface{}{
		"vm_handle": vmHandle,
	}
	resp, err := apiPost(fmt.Sprintf("/api/v1/devices/%s/attach", id), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Device '%s' attached to VM %d.\n", id, vmHandle)
	return nil
}

func deviceDetach(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiPost(fmt.Sprintf("/api/v1/devices/%s/detach", id), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Device '%s' detached.\n", id)
	return nil
}

// ── Cluster handlers ─────────────────────────────────────

func clusterStatus(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/cluster/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var status struct {
		Quorum      bool   `json:"quorum"`
		NodeCount   int    `json:"node_count"`
		OnlineCount int    `json:"online_count"`
		Leader      string `json:"leader"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"FIELD", "VALUE"}
	rows := [][]string{
		{"STATUS", status.Status},
		{"QUORUM", fmt.Sprintf("%v", status.Quorum)},
		{"NODES", fmt.Sprintf("%d", status.NodeCount)},
		{"ONLINE", fmt.Sprintf("%d", status.OnlineCount)},
		{"LEADER", status.Leader},
	}
	printOutput(status, headers, rows)
	return nil
}

func clusterNodeList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/cluster/nodes")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var nodes []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		IsLeader   bool   `json:"is_leader"`
		VMCount    int    `json:"vm_count"`
		FenceAgent string `json:"fence_agent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"NAME", "STATUS", "LEADER", "VMs", "FENCE_AGENT"}
	var rows [][]string
	for _, n := range nodes {
		leader := ""
		if n.IsLeader {
			leader = "*"
		}
		rows = append(rows, []string{
			n.Name, n.Status, leader, fmt.Sprintf("%d", n.VMCount), n.FenceAgent,
		})
	}
	printOutput(nodes, headers, rows)
	return nil
}

func clusterFence(cmd *cobra.Command, args []string) error {
	node, _ := cmd.Flags().GetString("node")
	reason, _ := cmd.Flags().GetString("reason")
	action, _ := cmd.Flags().GetString("action")
	if node == "" {
		return fmt.Errorf("--node is required")
	}

	body := map[string]interface{}{
		"reason": reason,
		"action": action,
	}
	resp, err := apiPost(fmt.Sprintf("/api/v1/cluster/fence/%s", node), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Node '%s' fenced (action: %s, reason: %s).\n", node, action, reason)
	return nil
}

// ── Utilities ────────────────────────────────────────────

func formatBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1fTB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

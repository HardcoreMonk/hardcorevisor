// Package main — hcvctl: HardCoreVisor CLI management tool
package main

import (
	"bufio"
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
	vmCreateCmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new VM",
		Args:  cobra.ExactArgs(1),
		RunE:  vmCreate,
	}
	vmCreateCmd.Flags().String("disk", "", "Disk image path (qcow2)")
	vmCreateCmd.Flags().String("backend", "", "VMM backend: qemu or rustvmm (default: auto)")
	vmCreateCmd.Flags().Uint32("vcpus", 2, "Number of vCPUs")
	vmCreateCmd.Flags().Uint64("memory", 4096, "Memory in MB")
	vmCmd.AddCommand(vmCreateCmd)
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

	vmMigrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Live migrate a VM to another node",
		RunE:  vmMigrate,
	}
	vmMigrateCmd.Flags().String("id", "", "VM ID to migrate")
	vmMigrateCmd.Flags().String("target", "", "Target node name")
	vmCmd.AddCommand(vmMigrateCmd)

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

	// ── completion subcommand ──
	completionCmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for hcvctl.

To load bash completions:
  source <(hcvctl completion bash)

To load zsh completions:
  source <(hcvctl completion zsh)

To load fish completions:
  hcvctl completion fish | source`,
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args:      cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}

	// ── backup subcommand ──
	backupCmd := &cobra.Command{Use: "backup", Short: "Manage VM backups"}

	backupCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all backups",
		RunE:  backupList,
	})

	backupCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a VM backup",
		RunE:  backupCreate,
	}
	backupCreateCmd.Flags().Int32("vm-id", 0, "VM ID to backup")
	backupCreateCmd.Flags().String("vm-name", "", "VM name")
	backupCreateCmd.Flags().String("pool", "", "Storage pool for backup")
	backupCmd.AddCommand(backupCreateCmd)

	backupDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a backup",
		RunE:  backupDelete,
	}
	backupDeleteCmd.Flags().String("id", "", "Backup ID to delete")
	backupCmd.AddCommand(backupDeleteCmd)

	// ── status subcommand ──
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show system status and statistics",
		RunE:  systemStatus,
	}

	// ── shell subcommand ──
	shellCmd := &cobra.Command{
		Use:   "shell",
		Short: "Interactive shell mode",
		Long:  "Start an interactive REPL for HardCoreVisor management.",
		RunE:  runShell,
	}

	// ── template subcommand ──
	templateCmd := &cobra.Command{Use: "template", Short: "Manage VM templates"}

	templateCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all templates",
		RunE:  templateList,
	})

	templateCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new template",
		RunE:  templateCreate,
	}
	templateCreateCmd.Flags().String("name", "", "Template name")
	templateCreateCmd.Flags().String("description", "", "Template description")
	templateCreateCmd.Flags().Uint32("vcpus", 2, "Number of vCPUs")
	templateCreateCmd.Flags().Uint64("memory", 4096, "Memory in MB")
	templateCreateCmd.Flags().Uint64("disk", 50, "Disk size in GB")
	templateCreateCmd.Flags().String("backend", "rustvmm", "VMM backend: qemu or rustvmm")
	templateCreateCmd.Flags().String("os", "linux", "OS type: linux or windows")
	templateCmd.AddCommand(templateCreateCmd)

	templateDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a template",
		RunE:  templateDelete,
	}
	templateDeleteCmd.Flags().String("id", "", "Template ID to delete")
	templateCmd.AddCommand(templateDeleteCmd)

	templateDeployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a VM from a template",
		RunE:  templateDeploy,
	}
	templateDeployCmd.Flags().String("id", "", "Template ID to deploy")
	templateDeployCmd.Flags().String("name", "", "Name for the new VM")
	templateCmd.AddCommand(templateDeployCmd)

	root.AddCommand(vmCmd, nodeCmd, versionCmd, storageCmd, networkCmd, deviceCmd, clusterCmd, completionCmd, backupCmd, statusCmd, shellCmd, templateCmd)

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

func apiDelete(path string) (*http.Response, error) {
	req, err := newRequest("DELETE", path, nil)
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
	vcpus, _ := cmd.Flags().GetUint32("vcpus")
	memory, _ := cmd.Flags().GetUint64("memory")
	disk, _ := cmd.Flags().GetString("disk")
	backend, _ := cmd.Flags().GetString("backend")

	body := map[string]interface{}{
		"name":      args[0],
		"vcpus":     vcpus,
		"memory_mb": memory,
	}
	if backend != "" {
		body["backend"] = backend
	}
	if disk != "" {
		body["disk"] = disk
	}

	resp, err := apiPost("/api/v1/vms", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
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

func vmMigrate(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	target, _ := cmd.Flags().GetString("target")
	if id == "" {
		return fmt.Errorf("--id is required")
	}
	if target == "" {
		return fmt.Errorf("--target is required")
	}

	body := map[string]interface{}{
		"target_node": target,
	}
	resp, err := apiPost(fmt.Sprintf("/api/v1/vms/%s/migrate", id), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("VM %s migrated to node '%s'.\n", id, target)
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

// ── Backup handlers ──────────────────────────────────────

func backupList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/backups")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var backups []struct {
		ID          string `json:"id"`
		VMID        int32  `json:"vm_id"`
		VMName      string `json:"vm_name"`
		SnapshotID  string `json:"snapshot_id"`
		Status      string `json:"status"`
		CreatedAt   int64  `json:"created_at"`
		SizeBytes   uint64 `json:"size_bytes"`
		StoragePool string `json:"storage_pool"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&backups); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "VM_ID", "VM_NAME", "SNAPSHOT", "STATUS", "SIZE", "POOL"}
	var rows [][]string
	for _, b := range backups {
		rows = append(rows, []string{
			b.ID, fmt.Sprintf("%d", b.VMID), b.VMName, b.SnapshotID,
			b.Status, formatBytes(b.SizeBytes), b.StoragePool,
		})
	}
	printOutput(backups, headers, rows)
	return nil
}

func backupCreate(cmd *cobra.Command, args []string) error {
	vmID, _ := cmd.Flags().GetInt32("vm-id")
	vmName, _ := cmd.Flags().GetString("vm-name")
	pool, _ := cmd.Flags().GetString("pool")

	body := map[string]interface{}{
		"vm_id":   vmID,
		"vm_name": vmName,
		"pool":    pool,
	}
	resp, err := apiPost("/api/v1/backups", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Backup created for VM '%s' (ID %d) in pool '%s'.\n", vmName, vmID, pool)
	return nil
}

func backupDelete(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiDelete(fmt.Sprintf("/api/v1/backups/%s", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Backup '%s' deleted.\n", id)
	return nil
}

// ── Status handler ────────────────────────────────────────

func systemStatus(cmd *cobra.Command, args []string) error {
	return handleShellStatus()
}

// ── Interactive Shell ────────────────────────────────────

func runShell(cmd *cobra.Command, args []string) error {
	fmt.Println("HardCoreVisor Interactive Shell")
	fmt.Println("Type 'help' for commands, 'exit' to quit")
	fmt.Printf("Connected to: %s\n\n", apiAddr)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("hcv> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		if err := executeShellCommand(line); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
	return nil
}

func executeShellCommand(line string) error {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "help":
		printShellHelp()
	case "vm":
		return handleShellVM(parts[1:])
	case "storage":
		return handleShellStorage(parts[1:])
	case "network":
		return handleShellNetwork(parts[1:])
	case "device":
		return handleShellDevice(parts[1:])
	case "cluster":
		return handleShellCluster(parts[1:])
	case "backup":
		return handleShellBackup(parts[1:])
	case "version":
		return handleShellVersion()
	case "status":
		return handleShellStatus()
	default:
		fmt.Printf("Unknown command: %s. Type 'help' for usage.\n", parts[0])
	}
	return nil
}

func printShellHelp() {
	fmt.Println(`Commands:
  vm list                 List all VMs
  vm create <name>        Create a VM
  vm start <id>           Start a VM
  vm stop <id>            Stop a VM
  vm delete <id>          Delete a VM
  vm migrate <id> <node>  Migrate VM
  storage pool list       List storage pools
  storage volume list     List storage volumes
  network zone list       List SDN zones
  network vnet list       List virtual networks
  network firewall list   List firewall rules
  device list             List devices
  cluster status          Cluster status
  cluster node list       List cluster nodes
  backup list             List backups
  version                 Show version
  status                  System status
  help                    Show this help
  exit                    Exit shell`)
}

func handleShellVM(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vm list|create|start|stop|delete|migrate")
	}
	switch args[0] {
	case "list":
		return vmList(nil, nil)
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: vm create <name>")
		}
		body := map[string]any{"name": args[1], "vcpus": 2, "memory_mb": 4096}
		resp, err := apiPost("/api/v1/vms", body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkResponse(resp); err != nil {
			return err
		}
		fmt.Printf("VM '%s' created.\n", args[1])
		return nil
	case "start":
		if len(args) < 2 {
			return fmt.Errorf("usage: vm start <id>")
		}
		return vmAction(args[1], "start")
	case "stop":
		if len(args) < 2 {
			return fmt.Errorf("usage: vm stop <id>")
		}
		return vmAction(args[1], "stop")
	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: vm delete <id>")
		}
		resp, err := apiDelete(fmt.Sprintf("/api/v1/vms/%s", args[1]))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkResponse(resp); err != nil {
			return err
		}
		fmt.Printf("VM '%s' deleted.\n", args[1])
		return nil
	case "migrate":
		if len(args) < 3 {
			return fmt.Errorf("usage: vm migrate <id> <node>")
		}
		body := map[string]any{"target_node": args[2]}
		resp, err := apiPost(fmt.Sprintf("/api/v1/vms/%s/migrate", args[1]), body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkResponse(resp); err != nil {
			return err
		}
		fmt.Printf("VM %s migrated to node '%s'.\n", args[1], args[2])
		return nil
	default:
		return fmt.Errorf("unknown vm command: %s", args[0])
	}
}

func handleShellStorage(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: storage pool list | storage volume list")
	}
	switch args[0] {
	case "pool":
		if args[1] == "list" {
			return storagePoolList(nil, nil)
		}
		return fmt.Errorf("unknown storage pool command: %s", args[1])
	case "volume":
		if args[1] == "list" {
			return storageVolumeList(nil, nil)
		}
		return fmt.Errorf("unknown storage volume command: %s", args[1])
	default:
		return fmt.Errorf("unknown storage command: %s", args[0])
	}
}

func handleShellNetwork(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: network zone list | network vnet list | network firewall list")
	}
	switch args[0] {
	case "zone":
		if args[1] == "list" {
			return networkZoneList(nil, nil)
		}
		return fmt.Errorf("unknown network zone command: %s", args[1])
	case "vnet":
		if args[1] == "list" {
			return networkVnetList(nil, nil)
		}
		return fmt.Errorf("unknown network vnet command: %s", args[1])
	case "firewall":
		if args[1] == "list" {
			return networkFirewallList(nil, nil)
		}
		return fmt.Errorf("unknown network firewall command: %s", args[1])
	default:
		return fmt.Errorf("unknown network command: %s", args[0])
	}
}

func handleShellDevice(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: device list")
	}
	switch args[0] {
	case "list":
		return shellDeviceList()
	default:
		return fmt.Errorf("unknown device command: %s", args[0])
	}
}

func shellDeviceList() error {
	resp, err := apiGet("/api/v1/devices")
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

func handleShellCluster(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cluster status | cluster node list")
	}
	switch args[0] {
	case "status":
		return clusterStatus(nil, nil)
	case "node":
		if len(args) < 2 || args[1] != "list" {
			return fmt.Errorf("usage: cluster node list")
		}
		return clusterNodeList(nil, nil)
	default:
		return fmt.Errorf("unknown cluster command: %s", args[0])
	}
}

func handleShellBackup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: backup list")
	}
	switch args[0] {
	case "list":
		return backupList(nil, nil)
	default:
		return fmt.Errorf("unknown backup command: %s", args[0])
	}
}

func handleShellVersion() error {
	fmt.Printf("hcvctl %s (commit: %s, built: %s)\n", version, commit, buildDate)
	return nil
}

func handleShellStatus() error {
	resp, err := apiGet("/api/v1/system/stats")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var stats map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	switch outputFormat {
	case "json":
		out, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(out))
	case "yaml":
		out, _ := yaml.Marshal(stats)
		fmt.Print(string(out))
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SECTION\tMETRIC\tVALUE")

		if uptime, ok := stats["uptime_seconds"].(float64); ok {
			fmt.Fprintf(tw, "System\tUptime\t%.0fs\n", uptime)
		}

		if vms, ok := stats["vms"].(map[string]any); ok {
			if total, ok := vms["total"].(float64); ok {
				fmt.Fprintf(tw, "VMs\tTotal\t%.0f\n", total)
			}
			if byState, ok := vms["by_state"].(map[string]any); ok {
				for state, count := range byState {
					if c, ok := count.(float64); ok {
						fmt.Fprintf(tw, "VMs\t%s\t%.0f\n", state, c)
					}
				}
			}
		}

		if stor, ok := stats["storage"].(map[string]any); ok {
			if pools, ok := stor["pools"].(float64); ok {
				fmt.Fprintf(tw, "Storage\tPools\t%.0f\n", pools)
			}
			if volumes, ok := stor["volumes"].(float64); ok {
				fmt.Fprintf(tw, "Storage\tVolumes\t%.0f\n", volumes)
			}
		}

		if net, ok := stats["network"].(map[string]any); ok {
			if zones, ok := net["zones"].(float64); ok {
				fmt.Fprintf(tw, "Network\tZones\t%.0f\n", zones)
			}
			if vnets, ok := net["vnets"].(float64); ok {
				fmt.Fprintf(tw, "Network\tVNets\t%.0f\n", vnets)
			}
			if rules, ok := net["firewall_rules"].(float64); ok {
				fmt.Fprintf(tw, "Network\tFirewall Rules\t%.0f\n", rules)
			}
		}

		if devices, ok := stats["devices"].(float64); ok {
			fmt.Fprintf(tw, "Devices\tTotal\t%.0f\n", devices)
		}
		if backups, ok := stats["backups"].(float64); ok {
			fmt.Fprintf(tw, "Backups\tTotal\t%.0f\n", backups)
		}

		tw.Flush()
	}
	return nil
}

// ── Template handlers ────────────────────────────────────

func templateList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/templates")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var templates []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		VCPUs       uint32 `json:"vcpus"`
		MemoryMB    uint64 `json:"memory_mb"`
		DiskSizeGB  uint64 `json:"disk_size_gb"`
		Backend     string `json:"backend"`
		OSType      string `json:"os_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "NAME", "VCPUS", "MEMORY", "DISK", "BACKEND", "OS", "DESCRIPTION"}
	var rows [][]string
	for _, t := range templates {
		rows = append(rows, []string{
			t.ID, t.Name,
			fmt.Sprintf("%d", t.VCPUs),
			fmt.Sprintf("%dMB", t.MemoryMB),
			fmt.Sprintf("%dGB", t.DiskSizeGB),
			t.Backend, t.OSType, t.Description,
		})
	}
	printOutput(templates, headers, rows)
	return nil
}

func templateCreate(cmd *cobra.Command, args []string) error {
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	vcpus, _ := cmd.Flags().GetUint32("vcpus")
	memory, _ := cmd.Flags().GetUint64("memory")
	disk, _ := cmd.Flags().GetUint64("disk")
	backend, _ := cmd.Flags().GetString("backend")
	osType, _ := cmd.Flags().GetString("os")

	if name == "" {
		return fmt.Errorf("--name is required")
	}

	body := map[string]interface{}{
		"name":         name,
		"description":  description,
		"vcpus":        vcpus,
		"memory_mb":    memory,
		"disk_size_gb": disk,
		"backend":      backend,
		"os_type":      osType,
	}
	resp, err := apiPost("/api/v1/templates", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Template '%s' created.\n", name)
	return nil
}

func templateDelete(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiDelete(fmt.Sprintf("/api/v1/templates/%s", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Template '%s' deleted.\n", id)
	return nil
}

func templateDeploy(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	name, _ := cmd.Flags().GetString("name")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	body := map[string]interface{}{
		"name": name,
	}
	resp, err := apiPost(fmt.Sprintf("/api/v1/templates/%s/deploy", id), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("VM deployed from template '%s'.\n", id)
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

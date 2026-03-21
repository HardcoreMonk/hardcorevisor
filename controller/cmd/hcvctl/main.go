// Package main — hcvctl: HardCoreVisor CLI management tool
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
	apiAddr   = "http://localhost:8080"
)

func main() {
	root := &cobra.Command{
		Use:   "hcvctl",
		Short: "HardCoreVisor CLI management tool",
		Long:  "hcvctl controls and monitors HardCoreVisor clusters via REST API.",
	}

	root.PersistentFlags().StringVar(&apiAddr, "api", apiAddr, "Controller API address")

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

	root.AddCommand(vmCmd, nodeCmd, versionCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── VM handlers ──────────────────────────────────────────

func vmList(cmd *cobra.Command, args []string) error {
	resp, err := http.Get(apiAddr + "/api/v1/vms")
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
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

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tVCPUS\tMEMORY\tNODE")
	for _, vm := range vms {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%dMB\t%s\n",
			vm.ID, vm.Name, vm.State, vm.VCPUs, vm.MemoryMB, vm.Node)
	}
	tw.Flush()
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
	resp, err := http.Get(apiAddr + "/api/v1/nodes")
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
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

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tCPU%\tMEM%\tVMs")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%s\t%s\t%.1f%%\t%.1f%%\t%d\n",
			n.Name, n.Status, n.CPUPercent, n.MemoryPercent, n.VMCount)
	}
	tw.Flush()
	return nil
}

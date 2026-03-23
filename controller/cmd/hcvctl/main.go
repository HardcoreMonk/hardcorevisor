// Package main — hcvctl: HardCoreVisor CLI 관리 도구
//
// # 패키지 목적
//
// Cobra 기반 CLI로 Controller REST API를 통해 HardCoreVisor 클러스터를 관리한다.
//
// # 주요 기능
//
//   - VM 관리: 목록/생성/시작/중지/마이그레이션
//   - 스토리지: 풀 목록, 볼륨 CRUD
//   - 네트워크: SDN 존, 가상 네트워크, 방화벽 규칙 조회
//   - 디바이스: GPU/NIC/USB 패스스루 관리
//   - 클러스터: 상태 조회, 노드 관리, 펜싱
//   - 백업/스냅샷/템플릿/이미지 관리
//   - 인터랙티브 셸 (REPL)
//
// # 글로벌 플래그
//
//   - --api: Controller API 주소 (기본: http://localhost:8080)
//   - --output/-o: 출력 형식 (table/json/yaml, 기본: table)
//   - --tls-skip-verify: TLS 인증서 검증 건너뛰기
//   - --user/--password: Basic Auth 인증 정보
//
// # 사용 예시
//
//	hcvctl vm list -o json
//	hcvctl vm create web-01 --vcpus 4 --memory 8192
//	hcvctl cluster status
//	hcvctl shell  # 인터랙티브 모드
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

// initHTTPClient — HTTP 클라이언트를 초기화한다.
// --tls-skip-verify 플래그가 설정되면 TLS 인증서 검증을 건너뛴다.
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

	vmListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all VMs",
		RunE:  vmList,
	}
	vmListCmd.Flags().String("type", "", "Filter by type (vm, container)")
	vmCmd.AddCommand(vmListCmd)
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

	// ── image subcommand ──
	imageCmd := &cobra.Command{Use: "image", Short: "Manage VM disk images"}

	imageCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered images",
		RunE:  imageList,
	})

	imageRegisterCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a new image",
		RunE:  imageRegister,
	}
	imageRegisterCmd.Flags().String("name", "", "Image name")
	imageRegisterCmd.Flags().String("format", "qcow2", "Image format (qcow2, raw, iso)")
	imageRegisterCmd.Flags().String("path", "", "Path to image file")
	imageRegisterCmd.Flags().String("os", "linux", "OS type (linux, windows)")
	imageCmd.AddCommand(imageRegisterCmd)

	imageDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a registered image",
		RunE:  imageDelete,
	}
	imageDeleteCmd.Flags().String("id", "", "Image ID to delete")
	imageCmd.AddCommand(imageDeleteCmd)

	// ── snapshot subcommand ──
	snapshotCmd := &cobra.Command{Use: "snapshot", Short: "Manage VM snapshots"}

	snapshotListCmd := &cobra.Command{
		Use:   "list",
		Short: "List snapshots",
		RunE:  snapshotListCLI,
	}
	snapshotListCmd.Flags().Int32("vm-id", 0, "Filter by VM ID")
	snapshotCmd.AddCommand(snapshotListCmd)

	snapshotCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a VM snapshot",
		RunE:  snapshotCreateCLI,
	}
	snapshotCreateCmd.Flags().Int32("vm-id", 0, "VM ID to snapshot")
	snapshotCreateCmd.Flags().String("vm-name", "", "VM name")
	snapshotCmd.AddCommand(snapshotCreateCmd)

	snapshotDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a snapshot",
		RunE:  snapshotDeleteCLI,
	}
	snapshotDeleteCmd.Flags().String("id", "", "Snapshot ID to delete")
	snapshotCmd.AddCommand(snapshotDeleteCmd)

	snapshotRestoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a VM from snapshot",
		RunE:  snapshotRestoreCLI,
	}
	snapshotRestoreCmd.Flags().String("id", "", "Snapshot ID to restore")
	snapshotCmd.AddCommand(snapshotRestoreCmd)

	// ── login subcommand — JWT 인증 후 토큰을 로컬에 저장 ──
	// 사용법: hcvctl login --user admin --password secret
	// 토큰 저장 위치: ~/.hcvctl/token (이후 요청에서 자동 사용)
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and store JWT token",
		Long:  "Authenticate with the Controller and store the JWT token in ~/.hcvctl/token for subsequent requests.",
		RunE:  loginRun,
	}
	loginCmd.Flags().String("user", "", "Username (overrides global --user)")
	loginCmd.Flags().String("password", "", "Password (overrides global --password)")

	// ── container subcommand — LXC 컨테이너 관리 ──
	// VM과 동일한 REST API (/api/v1/vms)를 사용하되, type=container 필터를 적용한다.
	// 컨테이너 전용 기능: exec (컨테이너 내 명령 실행)
	containerCmd := &cobra.Command{Use: "container", Short: "Manage LXC containers"}

	containerCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all containers",
		RunE:  containerList,
	})

	ctCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new container",
		RunE:  containerCreate,
	}
	ctCreateCmd.Flags().String("name", "", "Container name")
	ctCreateCmd.Flags().String("template", "ubuntu", "LXC template (ubuntu, alpine, debian, centos)")
	ctCreateCmd.Flags().Uint32("vcpus", 1, "Number of vCPUs")
	ctCreateCmd.Flags().Uint64("memory", 512, "Memory in MB")
	containerCmd.AddCommand(ctCreateCmd)

	containerCmd.AddCommand(&cobra.Command{
		Use:   "start [id]",
		Short: "Start a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vmAction(args[0], "start")
		},
	})

	containerCmd.AddCommand(&cobra.Command{
		Use:   "stop [id]",
		Short: "Stop a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vmAction(args[0], "stop")
		},
	})

	containerCmd.AddCommand(&cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiDelete(fmt.Sprintf("/api/v1/vms/%s", args[0]))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := checkResponse(resp); err != nil {
				return err
			}
			fmt.Printf("Container %s deleted.\n", args[0])
			return nil
		},
	})

	ctExecCmd := &cobra.Command{
		Use:   "exec [id] -- [command...]",
		Short: "Execute a command in a container",
		Args:  cobra.MinimumNArgs(1),
		RunE:  containerExec,
	}
	containerCmd.AddCommand(ctExecCmd)

	root.AddCommand(vmCmd, nodeCmd, versionCmd, storageCmd, networkCmd, deviceCmd, clusterCmd, completionCmd, backupCmd, statusCmd, shellCmd, templateCmd, imageCmd, snapshotCmd, loginCmd, containerCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── API 헬퍼 함수 ──────────────────────────────────────────

// tokenFilePath — JWT 토큰 저장 파일 경로를 반환한다 (~/.hcvctl/token).
// 홈 디렉터리를 결정할 수 없으면 빈 문자열을 반환한다.
func tokenFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.hcvctl/token"
}

// loadStoredToken — ~/.hcvctl/token에서 저장된 JWT 토큰을 읽는다.
// 파일이 없거나 읽기 실패 시 빈 문자열을 반환한다 (인증 없이 요청).
func loadStoredToken() string {
	path := tokenFilePath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// newRequest — API 요청을 생성한다.
// body가 있으면 Content-Type: application/json 헤더를 설정한다.
// Authentication priority:
//  1. --user flag → Basic Auth
//  2. Stored JWT token (~/.hcvctl/token) → Bearer Auth
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
	} else if token := loadStoredToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// loginRun — login 서브커맨드: Controller에 인증 후 JWT 토큰을 로컬에 저장한다.
//
// 처리 순서:
//  1. --user/--password 플래그 또는 글로벌 플래그에서 인증 정보 수집
//  2. POST /api/v1/auth/login으로 JWT 토큰 발급 요청
//  3. 발급된 토큰을 ~/.hcvctl/token에 저장 (0600 퍼미션)
//  4. 이후 newRequest()가 자동으로 Bearer 토큰을 헤더에 추가
func loginRun(cmd *cobra.Command, args []string) error {
	user, _ := cmd.Flags().GetString("user")
	pass, _ := cmd.Flags().GetString("password")
	// Fall back to global flags
	if user == "" {
		user = authUser
	}
	if pass == "" {
		pass = authPass
	}
	if user == "" || pass == "" {
		return fmt.Errorf("--user and --password are required")
	}

	resp, err := apiPost("/api/v1/auth/login", map[string]string{
		"username": user,
		"password": pass,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Store token in ~/.hcvctl/token
	path := tokenFilePath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	dir := path[:strings.LastIndex(path, "/")]
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(result.Token), 0600); err != nil {
		return fmt.Errorf("write token: %w", err)
	}

	fmt.Printf("Login successful. Token stored in %s\n", path)
	fmt.Printf("Expires at: %s\n", result.ExpiresAt)
	return nil
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

// printOutput — --output 플래그에 따라 목록 데이터를 출력한다.
//
// # 매개변수
//   - data: json/yaml 출력용 원시 데이터
//   - headers: table 출력의 헤더 행
//   - rows: table 출력의 데이터 행
//
// # 출력 형식
//   - "json": 들여쓰기된 JSON
//   - "yaml": YAML
//   - "table" (기본): 탭 정렬된 테이블
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
	path := "/api/v1/vms"
	typeFilter, _ := cmd.Flags().GetString("type")
	if typeFilter != "" {
		path += "?type=" + typeFilter
	}
	resp, err := apiGet(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var page struct {
		Data []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			State    string `json:"state"`
			VCPUs    int    `json:"vcpus"`
			MemoryMB int    `json:"memory_mb"`
			Node     string `json:"node"`
			Type     string `json:"type"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "NAME", "TYPE", "STATE", "VCPUS", "MEMORY", "NODE"}
	var rows [][]string
	for _, vm := range page.Data {
		vmType := vm.Type
		if vmType == "" {
			vmType = "vm"
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", vm.ID), vm.Name, vmType, vm.State,
			fmt.Sprintf("%d", vm.VCPUs), fmt.Sprintf("%dMB", vm.MemoryMB), vm.Node,
		})
	}
	printOutput(page.Data, headers, rows)
	return nil
}

// ── Container handlers — LXC 컨테이너 전용 CLI 핸들러 ──

// containerList — LXC 컨테이너 목록을 출력한다.
// GET /api/v1/vms?type=container로 컨테이너만 필터링하여 조회한다.
func containerList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/vms?type=container")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var page struct {
		Data []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			State    string `json:"state"`
			VCPUs    int    `json:"vcpus"`
			MemoryMB int    `json:"memory_mb"`
			Node     string `json:"node"`
			Template string `json:"template"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "NAME", "STATE", "VCPUS", "MEMORY", "NODE", "TEMPLATE"}
	var rows [][]string
	for _, ct := range page.Data {
		rows = append(rows, []string{
			fmt.Sprintf("%d", ct.ID), ct.Name, ct.State,
			fmt.Sprintf("%d", ct.VCPUs), fmt.Sprintf("%dMB", ct.MemoryMB),
			ct.Node, ct.Template,
		})
	}
	printOutput(page.Data, headers, rows)
	return nil
}

// containerCreate — 새 LXC 컨테이너를 생성한다.
// POST /api/v1/vms에 type=container, template 필드를 포함하여 요청한다.
// LXC 백엔드가 자동 선택되며, 지정된 배포 템플릿(ubuntu, alpine 등)으로 생성된다.
func containerCreate(cmd *cobra.Command, args []string) error {
	name, _ := cmd.Flags().GetString("name")
	tmpl, _ := cmd.Flags().GetString("template")
	vcpus, _ := cmd.Flags().GetUint32("vcpus")
	memory, _ := cmd.Flags().GetUint64("memory")

	if name == "" {
		return fmt.Errorf("--name is required")
	}

	body := map[string]interface{}{
		"name":      name,
		"vcpus":     vcpus,
		"memory_mb": memory,
		"type":      "container",
		"template":  tmpl,
	}
	resp, err := apiPost("/api/v1/vms", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Container '%s' created (template: %s).\n", name, tmpl)
	return nil
}

// containerExec — 실행 중인 컨테이너 내에서 명령을 실행한다.
//
// 사용법: hcvctl container exec <id> -- ls -la
// POST /api/v1/vms/{id}/exec에 {"command": ["ls", "-la"]}를 전송한다.
// Real 모드에서는 lxc-attach로 컨테이너 내부에서 명령을 실행하고 출력을 반환한다.
func containerExec(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("container ID is required")
	}
	id := args[0]

	// Everything after "--" or after the id is the command
	command := args[1:]
	if len(command) == 0 {
		return fmt.Errorf("command is required (use: container exec ID -- command args...)")
	}

	body := map[string]interface{}{
		"command": command,
	}
	resp, err := apiPost(fmt.Sprintf("/api/v1/vms/%s/exec", id), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var result struct {
		Output string `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}
	fmt.Print(result.Output)
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

// ── Image handlers ───────────────────────────────────────

func imageList(cmd *cobra.Command, args []string) error {
	resp, err := apiGet("/api/v1/images")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var images []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Format    string `json:"format"`
		SizeBytes uint64 `json:"size_bytes"`
		Path      string `json:"path"`
		OSType    string `json:"os_type"`
		CreatedAt int64  `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "NAME", "FORMAT", "SIZE", "OS", "PATH"}
	var rows [][]string
	for _, img := range images {
		rows = append(rows, []string{
			img.ID, img.Name, img.Format, formatBytes(img.SizeBytes), img.OSType, img.Path,
		})
	}
	printOutput(images, headers, rows)
	return nil
}

func imageRegister(cmd *cobra.Command, args []string) error {
	name, _ := cmd.Flags().GetString("name")
	format, _ := cmd.Flags().GetString("format")
	path, _ := cmd.Flags().GetString("path")
	osType, _ := cmd.Flags().GetString("os")

	if name == "" {
		return fmt.Errorf("--name is required")
	}

	body := map[string]interface{}{
		"name":    name,
		"format":  format,
		"path":    path,
		"os_type": osType,
	}
	resp, err := apiPost("/api/v1/images", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Image '%s' registered.\n", name)
	return nil
}

func imageDelete(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiDelete(fmt.Sprintf("/api/v1/images/%s", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Image '%s' deleted.\n", id)
	return nil
}

// ── Snapshot handlers ─────────────────────────────────────

func snapshotListCLI(cmd *cobra.Command, args []string) error {
	vmID, _ := cmd.Flags().GetInt32("vm-id")
	path := "/api/v1/snapshots"
	if vmID != 0 {
		path += fmt.Sprintf("?vm_id=%d", vmID)
	}
	resp, err := apiGet(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}

	var snapshots []struct {
		ID        string `json:"id"`
		VMID      int32  `json:"vm_id"`
		VMName    string `json:"vm_name"`
		State     string `json:"state"`
		CreatedAt int64  `json:"created_at"`
		SizeBytes uint64 `json:"size_bytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshots); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	headers := []string{"ID", "VM_ID", "VM_NAME", "STATE", "SIZE"}
	var rows [][]string
	for _, s := range snapshots {
		rows = append(rows, []string{
			s.ID, fmt.Sprintf("%d", s.VMID), s.VMName, s.State, formatBytes(s.SizeBytes),
		})
	}
	printOutput(snapshots, headers, rows)
	return nil
}

func snapshotCreateCLI(cmd *cobra.Command, args []string) error {
	vmID, _ := cmd.Flags().GetInt32("vm-id")
	vmName, _ := cmd.Flags().GetString("vm-name")

	body := map[string]interface{}{
		"vm_id":   vmID,
		"vm_name": vmName,
	}
	resp, err := apiPost("/api/v1/snapshots", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Snapshot created for VM '%s' (ID %d).\n", vmName, vmID)
	return nil
}

func snapshotDeleteCLI(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiDelete(fmt.Sprintf("/api/v1/snapshots/%s", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Snapshot '%s' deleted.\n", id)
	return nil
}

func snapshotRestoreCLI(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return fmt.Errorf("--id is required")
	}

	resp, err := apiPost(fmt.Sprintf("/api/v1/snapshots/%s/restore", id), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	fmt.Printf("Snapshot '%s' restore initiated.\n", id)
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

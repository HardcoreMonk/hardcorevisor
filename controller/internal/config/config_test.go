// config 패키지 유닛 테스트
//
// 테스트 대상: DefaultConfig(), Load() (환경변수 오버라이드, 파일 미존재)
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultConfig — DefaultConfig()가 올바른 기본값을 반환하는지 검증
func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig()가 nil을 반환함")
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"API 주소", cfg.API.Addr, ":18080"},
		{"gRPC 주소", cfg.GRPC.Addr, ":19090"},
		{"로그 레벨", cfg.Log.Level, "info"},
		{"로그 형식", cfg.Log.Format, "text"},
		{"DB 경로", cfg.Auth.DBPath, "hcv.db"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// 빈 문자열 기본값 확인
	if cfg.Etcd.Endpoints != "" {
		t.Errorf("etcd endpoints: got %q, want empty", cfg.Etcd.Endpoints)
	}
	if cfg.TLS.CertFile != "" {
		t.Errorf("TLS cert: got %q, want empty", cfg.TLS.CertFile)
	}
}

// TestLoadMissingFile — 존재하지 않는 YAML 파일 경로로 Load() 호출 시 기본값 반환
func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	cfg, err := Load("/tmp/nonexistent-hcv-config-test.yaml")
	if err != nil {
		t.Fatalf("Load() 에러: %v (파일 미존재 시 에러가 아니어야 함)", err)
	}
	if cfg.API.Addr != ":18080" {
		t.Errorf("API 주소: got %q, want %q", cfg.API.Addr, ":18080")
	}
	if cfg.Log.Level != "info" {
		t.Errorf("로그 레벨: got %q, want %q", cfg.Log.Level, "info")
	}
}

// TestLoadEnvOverride — 환경변수가 YAML과 기본값을 오버라이드하는지 검증
func TestLoadEnvOverride(t *testing.T) {
	// 환경변수 설정 (병렬 불가 — 글로벌 상태 변경)
	envVars := map[string]string{
		"HCV_API_ADDR":          ":9999",
		"HCV_GRPC_ADDR":         ":8888",
		"HCV_ETCD_ENDPOINTS":    "localhost:2379,localhost:2380",
		"HCV_TLS_CERT":          "/etc/tls/cert.pem",
		"HCV_TLS_KEY":           "/etc/tls/key.pem",
		"HCV_RBAC_USERS":        "admin:secret:admin",
		"HCV_LOG_LEVEL":         "debug",
		"HCV_LOG_FORMAT":        "json",
		"HCV_STORAGE_DRIVER":    "zfs",
		"HCV_PERIPHERAL_DRIVER": "sysfs",
		"HCV_CEPH_POOL":         "mypool",
		"HCV_JWT_SECRET":        "supersecret",
		"HCV_DB_PATH":           "/var/lib/hcv.db",
		"HCV_OTEL_ENDPOINT":     "http://localhost:4318",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}

	cfg, err := Load("/tmp/nonexistent-hcv-config-test.yaml")
	if err != nil {
		t.Fatalf("Load() 에러: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"API 주소", cfg.API.Addr, ":9999"},
		{"gRPC 주소", cfg.GRPC.Addr, ":8888"},
		{"etcd 엔드포인트", cfg.Etcd.Endpoints, "localhost:2379,localhost:2380"},
		{"TLS 인증서", cfg.TLS.CertFile, "/etc/tls/cert.pem"},
		{"TLS 키", cfg.TLS.KeyFile, "/etc/tls/key.pem"},
		{"RBAC 사용자", cfg.Auth.Users, "admin:secret:admin"},
		{"로그 레벨", cfg.Log.Level, "debug"},
		{"로그 형식", cfg.Log.Format, "json"},
		{"스토리지 드라이버", cfg.Storage.Driver, "zfs"},
		{"주변기기 드라이버", cfg.Peripheral.Driver, "sysfs"},
		{"Ceph 풀", cfg.Storage.CephPool, "mypool"},
		{"JWT 시크릿", cfg.Auth.JWTSecret, "supersecret"},
		{"DB 경로", cfg.Auth.DBPath, "/var/lib/hcv.db"},
		{"OTEL 엔드포인트", cfg.Otel.Endpoint, "http://localhost:4318"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestLoadYAMLFile — 유효한 YAML 파일에서 설정을 읽는지 검증
func TestLoadYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hcv.yaml")
	yamlContent := `
api:
  addr: ":7777"
grpc:
  addr: ":6666"
log:
  level: "warn"
  format: "json"
`
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("YAML 파일 작성 실패: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 에러: %v", err)
	}
	if cfg.API.Addr != ":7777" {
		t.Errorf("API 주소: got %q, want %q", cfg.API.Addr, ":7777")
	}
	if cfg.GRPC.Addr != ":6666" {
		t.Errorf("gRPC 주소: got %q, want %q", cfg.GRPC.Addr, ":6666")
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("로그 레벨: got %q, want %q", cfg.Log.Level, "warn")
	}
}

// TestGetEndpoints — EtcdConfig.GetEndpoints() 파싱 검증
func TestGetEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		endpoints string
		wantLen   int
	}{
		{"빈 문자열", "", 0},
		{"단일", "localhost:2379", 1},
		{"다중", "host1:2379,host2:2379,host3:2379", 3},
		{"공백 포함", " host1:2379 , host2:2379 ", 2},
		{"빈 항목", "host1:2379,,host2:2379", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := EtcdConfig{Endpoints: tc.endpoints}
			got := cfg.GetEndpoints()
			if tc.wantLen == 0 {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
			} else if len(got) != tc.wantLen {
				t.Errorf("got %d endpoints, want %d", len(got), tc.wantLen)
			}
		})
	}
}

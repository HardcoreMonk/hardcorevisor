// Package config — Controller YAML 설정 파일 + 환경변수 오버라이드
//
// 설정 우선순위: 환경변수 > YAML 파일 > 기본값
//
// 지원 환경변수 (YAML보다 항상 우선):
//   - HCV_API_ADDR: REST API 주소 (기본값: ":8080")
//   - HCV_GRPC_ADDR: gRPC 서버 주소 (기본값: ":9090")
//   - HCV_ETCD_ENDPOINTS: etcd 엔드포인트 (쉼표 구분, 미설정 시 인메모리)
//   - HCV_TLS_CERT: TLS 인증서 파일 경로
//   - HCV_TLS_KEY: TLS 키 파일 경로
//   - HCV_RBAC_USERS: RBAC 사용자 정의 ("user:pass:role,...")
//   - HCV_LOG_LEVEL: 로그 레벨 ("debug", "info", "warn", "error"). 기본값: "info"
//   - HCV_LOG_FORMAT: 로그 형식 ("text", "json"). 기본값: "text"
//   - HCV_STORAGE_DRIVER: 스토리지 드라이버 ("memory", "zfs", "ceph")
//   - HCV_PERIPHERAL_DRIVER: 주변기기 드라이버 ("memory", "sysfs")
//   - HCV_CEPH_POOL: Ceph 풀 이름 (기본값: "rbd")
//
// YAML 설정 파일 예제: hcv.example.yaml
package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 는 HardCoreVisor Controller의 최상위 설정 구조체이다.
type Config struct {
	API        APIConfig        `yaml:"api"`
	GRPC       GRPCConfig       `yaml:"grpc"`
	Etcd       EtcdConfig       `yaml:"etcd"`
	TLS        TLSConfig        `yaml:"tls"`
	Auth       AuthConfig       `yaml:"auth"`
	OAuth2     OAuth2Config     `yaml:"oauth2"`
	Log        LogConfig        `yaml:"log"`
	Storage    StorageConfig    `yaml:"storage"`
	Peripheral PeripheralConfig `yaml:"peripheral"`
}

// OAuth2Config 는 OAuth2/OIDC 프로바이더 설정을 보관한다.
// ProviderURL이 빈 문자열이면 OAuth2 기능이 비활성화된다.
type OAuth2Config struct {
	ProviderURL  string `yaml:"provider_url"`  // OIDC 프로바이더 URL
	ClientID     string `yaml:"client_id"`     // OAuth2 클라이언트 ID
	ClientSecret string `yaml:"client_secret"` // OAuth2 클라이언트 시크릿
	RedirectURL  string `yaml:"redirect_url"`  // 콜백 URL (기본: http://localhost:8080/api/v1/auth/oauth2/callback)
}

// StorageConfig 는 스토리지 백엔드 설정을 보관한다.
type StorageConfig struct {
	Driver   string `yaml:"driver"`    // "memory" (default), "zfs", "ceph"
	CephPool string `yaml:"ceph_pool"` // Ceph pool name (default: "rbd")
}

// PeripheralConfig 는 주변기기 백엔드 설정을 보관한다.
type PeripheralConfig struct {
	Driver string `yaml:"driver"` // "memory" (default), "sysfs"
}

// APIConfig 는 REST API 서버 설정을 보관한다.
type APIConfig struct {
	Addr      string `yaml:"addr"`       // default ":8080"
	RateLimit int    `yaml:"rate_limit"` // requests per second, 0 = no limit
}

// GRPCConfig 는 gRPC 서버 설정을 보관한다.
type GRPCConfig struct {
	Addr string `yaml:"addr"` // default ":9090"
}

// EtcdConfig 는 etcd 연결 설정을 보관한다.
type EtcdConfig struct {
	Endpoints string `yaml:"endpoints"` // comma-separated
}

// GetEndpoints 는 쉼표로 구분된 엔드포인트 문자열을 슬라이스로 반환한다.
// 빈 문자열이면 nil 슬라이스를 반환한다.
func (c EtcdConfig) GetEndpoints() []string {
	if c.Endpoints == "" {
		return nil
	}
	parts := strings.Split(c.Endpoints, ",")
	endpoints := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			endpoints = append(endpoints, p)
		}
	}
	if len(endpoints) == 0 {
		return nil
	}
	return endpoints
}

// TLSConfig 는 TLS 인증서 경로를 보관한다.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig 는 RBAC 사용자 정의와 JWT 설정을 보관한다.
type AuthConfig struct {
	Users     string `yaml:"users"`      // user:pass:role,... (legacy)
	JWTSecret string `yaml:"jwt_secret"` // JWT signing secret (auto-generated if empty)
	DBPath    string `yaml:"db_path"`    // SQLite user database path (default: "hcv.db")
}

// LogConfig 는 로깅 설정을 보관한다.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // text, json
}

// DefaultConfig 는 합리적인 기본값으로 Config를 반환한다.
// 기본값: API ":8080", gRPC ":9090", 로그 레벨 "info", 로그 형식 "text"
func DefaultConfig() *Config {
	return &Config{
		API:  APIConfig{Addr: ":8080"},
		GRPC: GRPCConfig{Addr: ":9090"},
		Auth: AuthConfig{DBPath: "hcv.db"},
		Log:  LogConfig{Level: "info", Format: "text"},
	}
}

// Load 는 YAML 설정 파일을 읽고 환경변수를 오버레이한다.
//
// 처리 순서:
//  1. DefaultConfig()로 기본값 생성
//  2. YAML 파일 읽기 (파일 미존재 시 기본값 유지, 에러 아님)
//  3. 환경변수 오버레이 (항상 YAML보다 우선)
//
// 호출 시점: Controller 시작 시 main.go에서 1회 호출
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// File not found — use defaults, then overlay env vars.
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	// Overlay environment variables (env vars take precedence).
	if v := os.Getenv("HCV_API_ADDR"); v != "" {
		cfg.API.Addr = v
	}
	if v := os.Getenv("HCV_GRPC_ADDR"); v != "" {
		cfg.GRPC.Addr = v
	}
	if v := os.Getenv("HCV_ETCD_ENDPOINTS"); v != "" {
		cfg.Etcd.Endpoints = v
	}
	if v := os.Getenv("HCV_TLS_CERT"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("HCV_TLS_KEY"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("HCV_RBAC_USERS"); v != "" {
		cfg.Auth.Users = v
	}
	if v := os.Getenv("HCV_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("HCV_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("HCV_STORAGE_DRIVER"); v != "" {
		cfg.Storage.Driver = v
	}
	if v := os.Getenv("HCV_PERIPHERAL_DRIVER"); v != "" {
		cfg.Peripheral.Driver = v
	}
	if v := os.Getenv("HCV_CEPH_POOL"); v != "" {
		cfg.Storage.CephPool = v
	}
	if v := os.Getenv("HCV_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("HCV_DB_PATH"); v != "" {
		cfg.Auth.DBPath = v
	}
	// OAuth2/OIDC 환경변수 오버라이드
	if v := os.Getenv("HCV_OAUTH2_PROVIDER"); v != "" {
		cfg.OAuth2.ProviderURL = v
	}
	if v := os.Getenv("HCV_OAUTH2_CLIENT_ID"); v != "" {
		cfg.OAuth2.ClientID = v
	}
	if v := os.Getenv("HCV_OAUTH2_CLIENT_SECRET"); v != "" {
		cfg.OAuth2.ClientSecret = v
	}

	return cfg, nil
}

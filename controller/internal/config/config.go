// Package config provides YAML + environment variable configuration for the controller.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the HardCoreVisor controller.
type Config struct {
	API        APIConfig        `yaml:"api"`
	GRPC       GRPCConfig       `yaml:"grpc"`
	Etcd       EtcdConfig       `yaml:"etcd"`
	TLS        TLSConfig        `yaml:"tls"`
	Auth       AuthConfig       `yaml:"auth"`
	Log        LogConfig        `yaml:"log"`
	Storage    StorageConfig    `yaml:"storage"`
	Peripheral PeripheralConfig `yaml:"peripheral"`
}

// StorageConfig holds storage backend settings.
type StorageConfig struct {
	Driver string `yaml:"driver"` // "memory" (default), "zfs"
}

// PeripheralConfig holds peripheral backend settings.
type PeripheralConfig struct {
	Driver string `yaml:"driver"` // "memory" (default), "sysfs"
}

// APIConfig holds REST API settings.
type APIConfig struct {
	Addr      string `yaml:"addr"`       // default ":8080"
	RateLimit int    `yaml:"rate_limit"` // requests per second, 0 = no limit
}

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	Addr string `yaml:"addr"` // default ":9090"
}

// EtcdConfig holds etcd connection settings.
type EtcdConfig struct {
	Endpoints string `yaml:"endpoints"` // comma-separated
}

// TLSConfig holds TLS certificate paths.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig holds RBAC user definitions.
type AuthConfig struct {
	Users string `yaml:"users"` // user:pass:role,...
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // text, json
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		API:  APIConfig{Addr: ":8080"},
		GRPC: GRPCConfig{Addr: ":9090"},
		Log:  LogConfig{Level: "info", Format: "text"},
	}
}

// Load reads a YAML config file from path, then overlays environment variables.
// Environment variables always take precedence over file values.
// If the file does not exist, defaults are used without error.
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

	return cfg, nil
}

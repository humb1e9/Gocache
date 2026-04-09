package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const defaultConfigPath = "config/config.json"

// Config describes the runtime settings for a cache node.
type Config struct {
	NodeID        string   `json:"node_id"`
	GRPCAddr      string   `json:"grpc_addr"`
	HTTPAddr      string   `json:"http_addr"`
	Group         string   `json:"group"`
	Policy        string   `json:"policy"`
	CacheBytes    int64    `json:"cache_bytes"`
	EtcdEndpoints []string `json:"etcd_endpoints"`
	ServicePrefix string   `json:"service_prefix"`
	LeaseTTL      int64    `json:"lease_ttl"`
}

// Load reads the node config from disk.
func Load() (Config, error) {
	path := os.Getenv("GOCACHE_CONFIG")
	if path == "" {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Group == "" {
		cfg.Group = "scores"
	}
	if cfg.Policy == "" {
		cfg.Policy = "lru"
	}
	if cfg.CacheBytes == 0 {
		cfg.CacheBytes = 64 << 20
	}
	if len(cfg.EtcdEndpoints) == 0 {
		cfg.EtcdEndpoints = []string{"127.0.0.1:2379"}
	}
	if cfg.ServicePrefix == "" {
		cfg.ServicePrefix = "/services/gocache/nodes"
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 10
	}
	if cfg.NodeID == "" && cfg.GRPCAddr != "" {
		cfg.NodeID = strings.ReplaceAll(cfg.GRPCAddr, ":", "_")
	}
}

func validate(cfg Config) error {
	if cfg.GRPCAddr == "" {
		return fmt.Errorf("config grpc_addr is required")
	}
	if cfg.HTTPAddr == "" {
		return fmt.Errorf("config http_addr is required")
	}
	return nil
}

// Package config loads settings from environment variables and YAML files.
// See docs/DESIGN.md, section 8.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Env returns the environment variable key, or def if it is unset or empty.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt returns key as an int, or def if unset or invalid.
func EnvInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return n
}

// EnvFloat returns key as a float64, or def if unset or invalid.
func EnvFloat(key string, def float64) float64 {
	f, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil {
		return def
	}
	return f
}

// EnvBool returns key as a bool, or def if unset or invalid.
func EnvBool(key string, def bool) bool {
	b, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return b
}

// Hostname returns the machine (container) hostname.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// BackendConfig is one entry in configs/backends.yaml.
type BackendConfig struct {
	ID  string `yaml:"id"`
	URL string `yaml:"url"`
}

// Quota is one application's token bucket settings from configs/quotas.yaml.
type Quota struct {
	Capacity     float64 `yaml:"capacity"`
	RefillPerSec float64 `yaml:"refill_per_sec"`
}

// LoadBackends reads the backend list.
func LoadBackends(path string) ([]BackendConfig, error) {
	var f struct {
		Backends []BackendConfig `yaml:"backends"`
	}
	if err := loadYAML(path, &f); err != nil {
		return nil, err
	}
	if len(f.Backends) == 0 {
		return nil, fmt.Errorf("%s: no backends defined", path)
	}
	for _, b := range f.Backends {
		if b.ID == "" || b.URL == "" {
			return nil, fmt.Errorf("%s: every backend needs an id and a url", path)
		}
	}
	return f.Backends, nil
}

// LoadQuotas reads the per-application quotas.
func LoadQuotas(path string) (map[string]Quota, error) {
	var f struct {
		Apps map[string]Quota `yaml:"apps"`
	}
	if err := loadYAML(path, &f); err != nil {
		return nil, err
	}
	if len(f.Apps) == 0 {
		return nil, fmt.Errorf("%s: no apps defined", path)
	}
	for app, q := range f.Apps {
		if q.Capacity <= 0 || q.RefillPerSec < 0 {
			return nil, fmt.Errorf("%s: app %q needs capacity > 0 and refill_per_sec >= 0", path, app)
		}
	}
	return f.Apps, nil
}

func loadYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Gateway holds all gateway settings.
type Gateway struct {
	ID           string
	Port         string
	Policy       string
	Limiter      string
	FailMode     string
	RedisAddr    string
	GatewayCount int
	ClockSource  string
	ClockSkew    time.Duration
	BackendsFile string
	QuotasFile   string
	HealthChecks bool
	MaxRetries   int
}

// LoadGateway reads gateway settings from environment variables.
func LoadGateway() Gateway {
	count := EnvInt("GATEWAY_COUNT", 1)
	if count < 1 {
		count = 1
	}
	retries := EnvInt("MAX_RETRIES", 1)
	if retries < 0 {
		retries = 0
	}
	return Gateway{
		ID:           Env("GATEWAY_ID", Hostname()),
		Port:         Env("PORT", "8000"),
		Policy:       Env("POLICY", "least_tokens"),
		Limiter:      Env("LIMITER", "redis"),
		FailMode:     Env("FAIL_MODE", "fail_closed"),
		RedisAddr:    Env("REDIS_ADDR", "redis:6379"),
		GatewayCount: count,
		ClockSource:  Env("CLOCK_SOURCE", "redis"),
		ClockSkew:    time.Duration(EnvInt("CLOCK_SKEW_MS", 0)) * time.Millisecond,
		BackendsFile: Env("BACKENDS_FILE", "configs/backends.yaml"),
		QuotasFile:   Env("QUOTAS_FILE", "configs/quotas.yaml"),
		HealthChecks: EnvBool("HEALTH_CHECKS", true),
		MaxRetries:   retries,
	}
}

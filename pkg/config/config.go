package config

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config represents the top-level configuration structure.
type Config struct {
	Global   GlobalConfig    `yaml:"global"   mapstructure:"global"`
	Services []ServiceConfig `yaml:"services" mapstructure:"services"`
}

// GlobalConfig holds global settings.
type GlobalConfig struct {
	LogLevel string `yaml:"log_level" mapstructure:"log_level"`
}

// ServiceConfig defines a virtual service with its backends and health check settings.
type ServiceConfig struct {
	Name        string            `yaml:"name"         mapstructure:"name"`
	Listen      string            `yaml:"listen"       mapstructure:"listen"`
	Protocol    string            `yaml:"protocol"     mapstructure:"protocol"`
	Scheduler   string            `yaml:"scheduler"    mapstructure:"scheduler"`
	HealthCheck HealthCheckConfig `yaml:"health_check" mapstructure:"health_check"`
	Backends    []BackendConfig   `yaml:"backends"     mapstructure:"backends"`
}

// HealthCheckConfig defines per-service health check parameters.
type HealthCheckConfig struct {
	Enabled            *bool  `yaml:"enabled"              mapstructure:"enabled"`
	Type               string `yaml:"type"                 mapstructure:"type"`
	Interval           string `yaml:"interval"             mapstructure:"interval"`
	Timeout            string `yaml:"timeout"              mapstructure:"timeout"`
	FailCount          int    `yaml:"fail_count"           mapstructure:"fail_count"`
	RiseCount          int    `yaml:"rise_count"           mapstructure:"rise_count"`
	HTTPPath           string `yaml:"http_path"            mapstructure:"http_path"`
	HTTPExpectedStatus int    `yaml:"http_expected_status" mapstructure:"http_expected_status"`
}

// IsEnabled returns whether health check is enabled for this service.
// Defaults to true if not explicitly set.
func (h HealthCheckConfig) IsEnabled() bool {
	if h.Enabled == nil {
		return true
	}
	return *h.Enabled
}

// GetInterval parses and returns the health check interval duration.
// Defaults to 5s if not set or invalid.
func (h HealthCheckConfig) GetInterval() time.Duration {
	if h.Interval == "" {
		return 5 * time.Second
	}
	duration, err := time.ParseDuration(h.Interval)
	if err != nil {
		return 5 * time.Second
	}
	return duration
}

// GetTimeout parses and returns the health check timeout duration.
// Defaults to 3s if not set or invalid.
func (h HealthCheckConfig) GetTimeout() time.Duration {
	if h.Timeout == "" {
		return 3 * time.Second
	}
	duration, err := time.ParseDuration(h.Timeout)
	if err != nil {
		return 3 * time.Second
	}
	return duration
}

// GetType returns the health check type.
// Defaults to "tcp" if not set.
func (h HealthCheckConfig) GetType() string {
	if h.Type == "" {
		return "tcp"
	}
	return h.Type
}

// GetHTTPPath returns the HTTP health check request path.
// Defaults to "/" if not set.
func (h HealthCheckConfig) GetHTTPPath() string {
	if h.HTTPPath == "" {
		return "/"
	}
	return h.HTTPPath
}

// GetHTTPExpectedStatus returns the expected HTTP response status code.
// Defaults to 200 if not set.
func (h HealthCheckConfig) GetHTTPExpectedStatus() int {
	if h.HTTPExpectedStatus <= 0 {
		return 200
	}
	return h.HTTPExpectedStatus
}

// GetFailCount returns the consecutive failure threshold.
// Defaults to 3 if not set.
func (h HealthCheckConfig) GetFailCount() int {
	if h.FailCount <= 0 {
		return 3
	}
	return h.FailCount
}

// GetRiseCount returns the consecutive success threshold.
// Defaults to 2 if not set.
func (h HealthCheckConfig) GetRiseCount() int {
	if h.RiseCount <= 0 {
		return 2
	}
	return h.RiseCount
}

// BackendConfig defines a real server (destination).
type BackendConfig struct {
	Address string `yaml:"address" mapstructure:"address"`
	Weight  int    `yaml:"weight"  mapstructure:"weight"`
}

// validSchedulers is the set of supported IPVS scheduling algorithms.
var validSchedulers = map[string]bool{
	"rr":  true,
	"wrr": true,
	"lc":  true,
	"wlc": true,
	"dh":  true,
	"sh":  true,
}

// validProtocols is the set of supported protocols.
var validProtocols = map[string]bool{
	"tcp": true,
	"udp": true,
}

// Manager handles configuration loading, validation, and hot-reload.
type Manager struct {
	viper      *viper.Viper
	configPath string
	current    *Config
	mu         sync.RWMutex
	onChange   chan struct{}
	logger     *zap.Logger
}

// NewManager creates a config Manager, loads and validates the initial configuration.
func NewManager(configPath string, logger *zap.Logger) (*Manager, error) {
	viperInstance := viper.New()
	viperInstance.SetConfigFile(configPath)

	// Set defaults
	viperInstance.SetDefault("global.log_level", "info")

	manager := &Manager{
		viper:      viperInstance,
		configPath: configPath,
		onChange:   make(chan struct{}, 1),
		logger:     logger,
	}

	cfg, err := manager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	manager.current = cfg

	return manager, nil
}

// Load reads the config file, unmarshals it, and validates.
func (m *Manager) Load() (*Config, error) {
	if err := m.viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := m.viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate checks the configuration for correctness.
func Validate(cfg *Config) error {
	if len(cfg.Services) == 0 {
		return fmt.Errorf("at least one service must be defined")
	}

	nameSet := make(map[string]bool)
	listenSet := make(map[string]bool)

	for i, svc := range cfg.Services {
		if svc.Name == "" {
			return fmt.Errorf("service[%d]: name is required", i)
		}
		if nameSet[svc.Name] {
			return fmt.Errorf("service[%d]: duplicate service name %q", i, svc.Name)
		}
		nameSet[svc.Name] = true

		// Validate listen address
		host, port, err := net.SplitHostPort(svc.Listen)
		if err != nil {
			return fmt.Errorf("service %q: invalid listen address %q: %w", svc.Name, svc.Listen, err)
		}
		if net.ParseIP(host) == nil {
			return fmt.Errorf("service %q: invalid listen IP %q", svc.Name, host)
		}
		if port == "" || port == "0" {
			return fmt.Errorf("service %q: listen port must be a positive number", svc.Name)
		}

		// Validate protocol (default to tcp)
		protocol := svc.Protocol
		if protocol == "" {
			cfg.Services[i].Protocol = "tcp"
			protocol = "tcp"
		}
		if !validProtocols[protocol] {
			return fmt.Errorf("service %q: unsupported protocol %q (supported: tcp, udp)", svc.Name, protocol)
		}

		// Deduplicate by listen address + protocol (IPVS allows same IP:Port for different protocols)
		listenKey := svc.Listen + "/" + protocol
		if listenSet[listenKey] {
			return fmt.Errorf("service %q: duplicate listen address %q for protocol %q", svc.Name, svc.Listen, protocol)
		}
		listenSet[listenKey] = true

		// Validate scheduler
		if !validSchedulers[svc.Scheduler] {
			return fmt.Errorf("service %q: unsupported scheduler %q (supported: rr, wrr, lc, wlc, dh, sh)", svc.Name, svc.Scheduler)
		}

		// Validate health check parameters
		if svc.HealthCheck.IsEnabled() {
			if svc.HealthCheck.Interval != "" {
				if _, err := time.ParseDuration(svc.HealthCheck.Interval); err != nil {
					return fmt.Errorf("service %q: invalid health_check.interval %q: %w", svc.Name, svc.HealthCheck.Interval, err)
				}
			}
			if svc.HealthCheck.Timeout != "" {
				if _, err := time.ParseDuration(svc.HealthCheck.Timeout); err != nil {
					return fmt.Errorf("service %q: invalid health_check.timeout %q: %w", svc.Name, svc.HealthCheck.Timeout, err)
				}
			}

			// Validate health check type
			checkType := svc.HealthCheck.GetType()
			if checkType != "tcp" && checkType != "http" {
				return fmt.Errorf("service %q: unsupported health_check.type %q (supported: tcp, http)", svc.Name, checkType)
			}

			// Validate HTTP-specific parameters
			if checkType == "http" {
				if svc.HealthCheck.HTTPPath != "" && svc.HealthCheck.HTTPPath[0] != '/' {
					return fmt.Errorf("service %q: health_check.http_path must start with '/'", svc.Name)
				}
				if svc.HealthCheck.HTTPExpectedStatus != 0 &&
					(svc.HealthCheck.HTTPExpectedStatus < 100 || svc.HealthCheck.HTTPExpectedStatus > 599) {
					return fmt.Errorf("service %q: health_check.http_expected_status must be between 100 and 599", svc.Name)
				}
			}
		}

		// Validate backends
		if len(svc.Backends) == 0 {
			return fmt.Errorf("service %q: at least one backend is required", svc.Name)
		}

		backendSet := make(map[string]bool)
		for j, backend := range svc.Backends {
			if backend.Address == "" {
				return fmt.Errorf("service %q: backend[%d]: address is required", svc.Name, j)
			}
			backendHost, backendPort, err := net.SplitHostPort(backend.Address)
			if err != nil {
				return fmt.Errorf("service %q: backend[%d]: invalid address %q: %w", svc.Name, j, backend.Address, err)
			}
			if net.ParseIP(backendHost) == nil {
				return fmt.Errorf("service %q: backend[%d]: invalid IP %q", svc.Name, j, backendHost)
			}
			if backendPort == "" || backendPort == "0" {
				return fmt.Errorf("service %q: backend[%d]: port must be a positive number", svc.Name, j)
			}
			if backendSet[backend.Address] {
				return fmt.Errorf("service %q: backend[%d]: duplicate address %q", svc.Name, j, backend.Address)
			}
			backendSet[backend.Address] = true

			if backend.Weight <= 0 {
				return fmt.Errorf("service %q: backend[%d]: weight must be a positive integer", svc.Name, j)
			}
		}
	}

	return nil
}

// WatchConfig starts watching the config file for changes.
// On change, it reloads and validates; if valid, updates current config and notifies via onChange channel.
func (m *Manager) WatchConfig() {
	m.viper.OnConfigChange(func(event fsnotify.Event) {
		m.logger.Info("config file changed", zap.String("file", event.Name))

		cfg, err := m.Load()
		if err != nil {
			m.logger.Error("failed to reload config, keeping previous config", zap.Error(err))
			return
		}

		m.mu.Lock()
		m.current = cfg
		m.mu.Unlock()

		m.logger.Info("config reloaded successfully")

		// Non-blocking send to notify listeners
		select {
		case m.onChange <- struct{}{}:
		default:
		}
	})

	m.viper.WatchConfig()
}

// GetConfig returns a snapshot of the current configuration.
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// OnChange returns a read-only channel that signals when config has changed.
func (m *Manager) OnChange() <-chan struct{} {
	return m.onChange
}

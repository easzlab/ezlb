package config

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config represents the top-level configuration structure.
type Config struct {
	Services []ServiceConfig `yaml:"services" mapstructure:"services"`
	Global   GlobalConfig    `yaml:"global"   mapstructure:"global"`
}

// GlobalConfig holds global settings.
type GlobalConfig struct {
	CleanupOnExit  *bool  `yaml:"cleanup_on_exit"  mapstructure:"cleanup_on_exit"`
	MetricsEnabled *bool  `yaml:"metrics_enabled"  mapstructure:"metrics_enabled"`
	AdminAddress   string `yaml:"admin_address"    mapstructure:"admin_address"`
	MetricsPath    string `yaml:"metrics_path"     mapstructure:"metrics_path"`
	// MetricsInterval controls how often the traffic stats collector polls
	// IPVS for Prometheus metrics. The collector runs whenever metrics are
	// enabled (global.metrics_enabled). Minimum 5s, defaults to 15s.
	MetricsInterval string    `yaml:"metrics_interval" mapstructure:"metrics_interval"`
	Log             LogConfig `yaml:"log"              mapstructure:"log"`
}

// LogConfig holds unified logging configuration.
type LogConfig struct {
	Level      string `yaml:"level"       mapstructure:"level"`
	Home       string `yaml:"home"        mapstructure:"home"`
	MaxSize    int    `yaml:"max_size"    mapstructure:"max_size"`
	MaxBackups int    `yaml:"max_backups" mapstructure:"max_backups"`
	MaxAge     int    `yaml:"max_age"     mapstructure:"max_age"`
	Compress   bool   `yaml:"compress"    mapstructure:"compress"`
}

// validLogLevels is the set of supported log levels.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// GetLevel returns the log level. Defaults to "info" if not set.
func (l LogConfig) GetLevel() string {
	if l.Level == "" {
		return "info"
	}
	return l.Level
}

// GetHome returns the log directory. Defaults to "./logs" if not set.
func (l LogConfig) GetHome() string {
	if l.Home == "" {
		return "./logs"
	}
	return l.Home
}

// GetMaxSize returns the max size in MB per log file. Defaults to 50.
func (l LogConfig) GetMaxSize() int {
	if l.MaxSize <= 0 {
		return 50
	}
	return l.MaxSize
}

// GetMaxBackups returns the max number of old log files to retain. Defaults to 3.
func (l LogConfig) GetMaxBackups() int {
	if l.MaxBackups <= 0 {
		return 3
	}
	return l.MaxBackups
}

// GetMaxAge returns the max age in days to retain old log files. Defaults to 0 (no limit).
func (l LogConfig) GetMaxAge() int {
	return l.MaxAge
}

// GetMetricsInterval parses and returns the Prometheus traffic stats
// collection interval. Defaults to 15s; values below 5s are clamped to 5s.
func (g GlobalConfig) GetMetricsInterval() time.Duration {
	if g.MetricsInterval == "" {
		return 15 * time.Second
	}
	duration, err := time.ParseDuration(g.MetricsInterval)
	if err != nil {
		return 15 * time.Second
	}
	if duration < 5*time.Second {
		return 5 * time.Second
	}
	return duration
}

// IsCleanupOnExit returns whether to clean up IPVS and iptables rules on exit.
// Defaults to true if not explicitly set.
func (g GlobalConfig) IsCleanupOnExit() bool {
	if g.CleanupOnExit == nil {
		return true
	}
	return *g.CleanupOnExit
}

// IsMetricsEnabled returns whether metrics are enabled.
// Defaults to true if not explicitly set.
func (g GlobalConfig) IsMetricsEnabled() bool {
	if g.MetricsEnabled == nil {
		return true
	}
	return *g.MetricsEnabled
}

// GetMetricsPath returns the metrics endpoint path.
// Defaults to "/metrics" if not set.
func (g GlobalConfig) GetMetricsPath() string {
	if g.MetricsPath == "" {
		return "/metrics"
	}
	return g.MetricsPath
}

// ServiceConfig defines a virtual service with its backends and health check settings.
type ServiceConfig struct {
	Name        string            `yaml:"name"              mapstructure:"name"`
	Listen      string            `yaml:"listen"            mapstructure:"listen"`
	Protocol    string            `yaml:"protocol"          mapstructure:"protocol"`
	Scheduler   string            `yaml:"scheduler"         mapstructure:"scheduler"`
	ForwardMode string            `yaml:"forward_mode"      mapstructure:"forward_mode"`
	SnatIP      string            `yaml:"snat_ip"           mapstructure:"snat_ip"`
	Backends    []BackendConfig   `yaml:"backends"          mapstructure:"backends"`
	HealthCheck HealthCheckConfig `yaml:"health_check"      mapstructure:"health_check"`
	FullNAT     bool              `yaml:"full_nat"          mapstructure:"full_nat"`
}

// GetForwardMode returns the IPVS forwarding mode.
// Defaults to "nat" if not set.
func (s ServiceConfig) GetForwardMode() string {
	if s.ForwardMode == "" {
		return "nat"
	}
	return s.ForwardMode
}

// HealthCheckConfig defines per-service health check parameters.
type HealthCheckConfig struct {
	Enabled            *bool  `yaml:"enabled"              mapstructure:"enabled"`
	Type               string `yaml:"type"                 mapstructure:"type"`
	Interval           string `yaml:"interval"             mapstructure:"interval"`
	Timeout            string `yaml:"timeout"              mapstructure:"timeout"`
	HTTPPath           string `yaml:"http_path"            mapstructure:"http_path"`
	FailCount          int    `yaml:"fail_count"           mapstructure:"fail_count"`
	RiseCount          int    `yaml:"rise_count"           mapstructure:"rise_count"`
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

// validForwardModes is the set of supported IPVS forwarding modes.
var validForwardModes = map[string]bool{
	"nat": true,
	"dr":  true,
	"tun": true,
}

// Manager handles configuration loading, validation, and hot-reload.
type Manager struct {
	viper      *viper.Viper
	current    *Config
	onChange   chan struct{}
	onReload   func()
	logger     *zap.Logger
	configPath string
	mu         sync.RWMutex
}

// applyViperDefaults sets all default values on the given viper instance.
// This is the single source of truth for config defaults and is shared between
// NewManager and LoadLogConfig to avoid duplication.
func applyViperDefaults(v *viper.Viper) {
	v.SetDefault("global.log.level", "info")
	v.SetDefault("global.log.home", "./logs")
	v.SetDefault("global.log.max_size", 50)
	v.SetDefault("global.log.max_backups", 3)
	v.SetDefault("global.log.max_age", 0)
	v.SetDefault("global.log.compress", false)
	v.SetDefault("global.cleanup_on_exit", true)
	v.SetDefault("global.metrics_enabled", true)
	v.SetDefault("global.metrics_path", "/metrics")
	v.SetDefault("global.metrics_interval", "15s")
}

// LoadLogConfig reads only the global.log section from the config file.
// It is used by the entrypoint to build production loggers before the full
// config Manager is constructed, avoiding a chicken-and-egg dependency on logger.
func LoadLogConfig(path string) (LogConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)
	applyViperDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return LogConfig{}, fmt.Errorf("failed to read config file: %w", err)
	}

	var wrapper struct {
		Global struct {
			Log LogConfig `mapstructure:"log"`
		} `mapstructure:"global"`
	}
	if err := v.Unmarshal(&wrapper); err != nil {
		return LogConfig{}, fmt.Errorf("failed to unmarshal log config: %w", err)
	}
	return wrapper.Global.Log, nil
}

// NewManager creates a config Manager, loads and validates the initial configuration.
func NewManager(configPath string, logger *zap.Logger) (*Manager, error) {
	viperInstance := viper.New()
	viperInstance.SetConfigFile(configPath)
	applyViperDefaults(viperInstance)

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
	// Validate log level
	logLevel := cfg.Global.Log.GetLevel()
	if !validLogLevels[logLevel] {
		return fmt.Errorf("global.log.level: unsupported level %q (supported: debug, info, warn, error)", logLevel)
	}

	// Validate Prometheus metrics collection interval
	if cfg.Global.MetricsInterval != "" {
		interval, err := time.ParseDuration(cfg.Global.MetricsInterval)
		if err != nil {
			return fmt.Errorf("global.metrics_interval: invalid duration %q: %w", cfg.Global.MetricsInterval, err)
		}
		if interval < 5*time.Second {
			return fmt.Errorf("global.metrics_interval: minimum interval is 5s, got %v", interval)
		}
	}

	if len(cfg.Services) == 0 {
		return fmt.Errorf("at least one service must be defined")
	}
	if cfg.Global.AdminAddress != "" {
		if _, _, err := net.SplitHostPort(cfg.Global.AdminAddress); err != nil {
			return fmt.Errorf("global.admin_address: %w", err)
		}
	}
	metricsPath := cfg.Global.GetMetricsPath()
	if !strings.HasPrefix(metricsPath, "/") || metricsPath == "/health" || metricsPath == "/ready" {
		return fmt.Errorf("global.metrics_path: invalid or reserved path %q", metricsPath)
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
		listenIP := net.ParseIP(host)
		if listenIP == nil {
			return fmt.Errorf("service %q: invalid listen IP %q", svc.Name, host)
		}
		listenPort, err := parsePort(port)
		if err != nil {
			return fmt.Errorf("service %q: invalid listen port %q: %w", svc.Name, port, err)
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
		listenKey := net.JoinHostPort(listenIP.String(), strconv.Itoa(int(listenPort))) + "/" + protocol
		if listenSet[listenKey] {
			return fmt.Errorf("service %q: duplicate listen address %q for protocol %q", svc.Name, svc.Listen, protocol)
		}
		listenSet[listenKey] = true

		// Validate scheduler
		if !validSchedulers[svc.Scheduler] {
			return fmt.Errorf("service %q: unsupported scheduler %q (supported: rr, wrr, lc, wlc, dh, sh)", svc.Name, svc.Scheduler)
		}

		// Validate forward mode (default to nat)
		forwardMode := svc.ForwardMode
		if forwardMode == "" {
			cfg.Services[i].ForwardMode = "nat"
			forwardMode = "nat"
		}
		if !validForwardModes[forwardMode] {
			return fmt.Errorf("service %q: unsupported forward_mode %q (supported: nat, dr, tun)", svc.Name, forwardMode)
		}
		// FullNAT requires NAT forward mode (iptables SNAT requires NAT forwarding)
		if svc.FullNAT && forwardMode != "nat" {
			return fmt.Errorf("service %q: full_nat requires forward_mode=nat, got %q", svc.Name, forwardMode)
		}
		if svc.FullNAT && listenIP.To4() == nil {
			return fmt.Errorf("service %q: full_nat currently supports IPv4 only", svc.Name)
		}

		// Validate health check parameters
		if svc.HealthCheck.IsEnabled() {
			if svc.HealthCheck.Interval != "" {
				interval, err := time.ParseDuration(svc.HealthCheck.Interval)
				if err != nil {
					return fmt.Errorf("service %q: invalid health_check.interval %q: %w", svc.Name, svc.HealthCheck.Interval, err)
				}
				if interval <= 0 {
					return fmt.Errorf("service %q: health_check.interval must be positive", svc.Name)
				}
			}
			if svc.HealthCheck.Timeout != "" {
				timeout, err := time.ParseDuration(svc.HealthCheck.Timeout)
				if err != nil {
					return fmt.Errorf("service %q: invalid health_check.timeout %q: %w", svc.Name, svc.HealthCheck.Timeout, err)
				}
				if timeout <= 0 {
					return fmt.Errorf("service %q: health_check.timeout must be positive", svc.Name)
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

		// Validate full_nat and snat_ip
		if svc.SnatIP != "" {
			if !svc.FullNAT {
				return fmt.Errorf("service %q: snat_ip requires full_nat to be enabled", svc.Name)
			}
			if ip := net.ParseIP(svc.SnatIP); ip == nil || ip.To4() == nil {
				return fmt.Errorf("service %q: invalid snat_ip %q", svc.Name, svc.SnatIP)
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
			backendIP := net.ParseIP(backendHost)
			if backendIP == nil {
				return fmt.Errorf("service %q: backend[%d]: invalid IP %q", svc.Name, j, backendHost)
			}
			parsedBackendPort, err := parsePort(backendPort)
			if err != nil {
				return fmt.Errorf("service %q: backend[%d]: invalid port %q: %w", svc.Name, j, backendPort, err)
			}
			if (listenIP.To4() == nil) != (backendIP.To4() == nil) {
				return fmt.Errorf("service %q: backend[%d]: IP family must match listen address", svc.Name, j)
			}
			backendKey := net.JoinHostPort(backendIP.String(), strconv.Itoa(int(parsedBackendPort)))
			if backendSet[backendKey] {
				return fmt.Errorf("service %q: backend[%d]: duplicate address %q", svc.Name, j, backend.Address)
			}
			backendSet[backendKey] = true

			if backend.Weight <= 0 {
				return fmt.Errorf("service %q: backend[%d]: weight must be a positive integer", svc.Name, j)
			}
		}
	}

	return nil
}

func parsePort(value string) (uint16, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, err
	}
	if port == 0 {
		return 0, fmt.Errorf("must be between 1 and 65535")
	}
	return uint16(port), nil
}

// Restart-only settings are rejected during hot reload so the active config
// never claims an admin endpoint or logger state that was not actually applied.
func validateReload(current, next *Config) error {
	if current.Global.AdminAddress != next.Global.AdminAddress ||
		current.Global.GetMetricsPath() != next.Global.GetMetricsPath() ||
		current.Global.IsMetricsEnabled() != next.Global.IsMetricsEnabled() ||
		!reflect.DeepEqual(current.Global.Log, next.Global.Log) {
		return fmt.Errorf("global.admin_address, metrics_path, metrics_enabled, and log settings require a restart")
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
		if err := validateReload(m.current, cfg); err != nil {
			m.mu.Unlock()
			m.logger.Error("rejected restart-only config change, keeping previous config", zap.Error(err))
			return
		}
		m.current = cfg
		m.mu.Unlock()

		m.logger.Info("config reloaded successfully")

		// Increment config reload counter via callback if registered
		if m.onReload != nil {
			m.onReload()
		}

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

// SetOnReloadCallback sets a callback function to be called when config is reloaded.
func (m *Manager) SetOnReloadCallback(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onReload = fn
}

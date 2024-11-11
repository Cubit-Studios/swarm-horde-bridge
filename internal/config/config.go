package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v2"
)

// Load reads and parses the configuration from a file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := loadEnvOverrides(&cfg); err != nil {
		return nil, fmt.Errorf("loading environment overrides: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	setDefaults(&cfg)

	// Set global zerolog level based on config
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel // fallback to info if invalid level
	}
	zerolog.SetGlobalLevel(level)

	return &cfg, nil
}

// loadEnvOverrides applies environment variable overrides to the config
func loadEnvOverrides(cfg *Config) error {
	// Server settings
	if port := os.Getenv("PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("invalid PORT value: %w", err)
		}
		cfg.Server.Port = p
	}

	// Horde settings
	if host := os.Getenv("HORDE_HOST"); host != "" {
		cfg.Horde.Host = host
	}
	if key := os.Getenv("HORDE_API_KEY"); key != "" {
		cfg.Horde.APIKey = key
	}
	if timeout := os.Getenv("HORDE_TIMEOUT"); timeout != "" {
		t, err := strconv.Atoi(timeout)
		if err != nil {
			return fmt.Errorf("invalid HORDE_TIMEOUT value: %w", err)
		}
		cfg.Horde.Timeout = t
	}

	// Swarm settings
	if host := os.Getenv("SWARM_HOST"); host != "" {
		cfg.Swarm.Host = host
	}
	if timeout := os.Getenv("SWARM_TIMEOUT"); timeout != "" {
		t, err := strconv.Atoi(timeout)
		if err != nil {
			return fmt.Errorf("invalid SWARM_TIMEOUT value: %w", err)
		}
		cfg.Swarm.Timeout = t
	}

	// Monitor settings
	if interval := os.Getenv("MONITOR_INTERVAL"); interval != "" {
		i, err := strconv.Atoi(interval)
		if err != nil {
			return fmt.Errorf("invalid MONITOR_INTERVAL value: %w", err)
		}
		cfg.Monitor.Interval = i
	}

	// Timeout settings
	if clientTimeout := os.Getenv("TIMEOUT_HTTP_CLIENT"); clientTimeout != "" {
		t, err := strconv.Atoi(clientTimeout)
		if err != nil {
			return fmt.Errorf("invalid TIMEOUT_HTTP_CLIENT value: %w", err)
		}
		cfg.Timeouts.HTTPClient = t
	}
	if shutdownTimeout := os.Getenv("TIMEOUT_SHUTDOWN"); shutdownTimeout != "" {
		t, err := strconv.Atoi(shutdownTimeout)
		if err != nil {
			return fmt.Errorf("invalid TIMEOUT_SHUTDOWN value: %w", err)
		}
		cfg.Timeouts.Shutdown = t
	}

	// Retry settings
	if attempts := os.Getenv("RETRY_MAX_ATTEMPTS"); attempts != "" {
		a, err := strconv.Atoi(attempts)
		if err != nil {
			return fmt.Errorf("invalid RETRY_MAX_ATTEMPTS value: %w", err)
		}
		cfg.Retry.MaxAttempts = a
	}
	if delay := os.Getenv("RETRY_INITIAL_DELAY"); delay != "" {
		d, err := strconv.Atoi(delay)
		if err != nil {
			return fmt.Errorf("invalid RETRY_INITIAL_DELAY value: %w", err)
		}
		cfg.Retry.InitialDelay = d
	}
	if maxDelay := os.Getenv("RETRY_MAX_DELAY"); maxDelay != "" {
		d, err := strconv.Atoi(maxDelay)
		if err != nil {
			return fmt.Errorf("invalid RETRY_MAX_DELAY value: %w", err)
		}
		cfg.Retry.MaxDelay = d
	}

	// Log level
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		cfg.LogLevel = level
	}

	return nil
}

// validate checks if the configuration is valid
func validate(cfg *Config) error {
	if cfg.Horde.Host == "" {
		return fmt.Errorf("horde host is required")
	}
	if cfg.Horde.APIKey == "" {
		return fmt.Errorf("horde API key is required")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", cfg.Server.Port)
	}
	return nil
}

// setDefaults sets default values for optional configuration fields
func setDefaults(cfg *Config) {
	// Server defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	// Swarm defaults
	if cfg.Swarm.Host == "" {
		cfg.Swarm.Host = "http://localhost"
	}
	if cfg.Swarm.Timeout == 0 {
		cfg.Swarm.Timeout = 30
	}

	// Monitor defaults
	if cfg.Monitor.Interval == 0 {
		cfg.Monitor.Interval = 30
	}

	// Timeout defaults
	if cfg.Timeouts.HTTPClient == 0 {
		cfg.Timeouts.HTTPClient = 30
	}
	if cfg.Timeouts.Shutdown == 0 {
		cfg.Timeouts.Shutdown = 5
	}

	// Retry defaults
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.InitialDelay == 0 {
		cfg.Retry.InitialDelay = 1
	}
	if cfg.Retry.MaxDelay == 0 {
		cfg.Retry.MaxDelay = 5
	}

	// Log level default
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	// Set default clock if none provided
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
}

// GetHTTPClientTimeout returns the HTTP client timeout as a time.Duration
func (c *Config) GetHTTPClientTimeout() time.Duration {
	return time.Duration(c.Timeouts.HTTPClient) * time.Second
}

// GetShutdownTimeout returns the shutdown timeout as a time.Duration
func (c *Config) GetShutdownTimeout() time.Duration {
	return time.Duration(c.Timeouts.Shutdown) * time.Second
}

// GetMonitorInterval returns the monitor interval as a time.Duration
func (c *Config) GetMonitorInterval() time.Duration {
	return time.Duration(c.Monitor.Interval) * time.Second
}

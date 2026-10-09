package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v2"
)

// Load reads the configuration from an optional YAML file, applies
// environment variable overrides and defaults, then validates the result.
//
// If the file does not exist, the configuration is built from the
// environment only (e.g. when running in a container).
func Load(path string) (*Config, error) {
	var cfg Config

	fileFound := false
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("parsing config file: %w", err)
			}
			cfg.ConfigFile = path
			fileFound = true
		case errors.Is(err, fs.ErrNotExist):
			// Optional config file: fall back to environment variables only
		default:
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	if err := loadEnvOverrides(&cfg); err != nil {
		return nil, fmt.Errorf("loading environment overrides: %w", err)
	}

	setDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		if !fileFound && path != "" {
			return nil, fmt.Errorf("validating config (config file %q not found, using environment variables only): %w", path, err)
		}
		return nil, fmt.Errorf("validating config: %w", err)
	}

	// Set global zerolog level based on config (validated above)
	level, _ := zerolog.ParseLevel(cfg.LogLevel)
	zerolog.SetGlobalLevel(level)

	return &cfg, nil
}

// loadEnvOverrides applies environment variable overrides to the config
func loadEnvOverrides(cfg *Config) error {
	envString("HORDE_HOST", &cfg.Horde.Host)
	envString("HORDE_API_KEY", &cfg.Horde.APIKey)
	envString("HORDE_PUBLIC_URL", &cfg.Horde.PublicURL)
	envString("HORDE_TEMPLATE_ID", &cfg.Horde.TemplateId)
	envString("HORDE_STREAM_ID", &cfg.Horde.StreamId)
	envString("LOG_LEVEL", &cfg.LogLevel)
	envString("DATA_DIR", &cfg.DataDir)
	envString("WEBHOOK_TOKEN", &cfg.Server.WebhookToken)
	envString("SWARM_ALLOWED_HOST", &cfg.Swarm.AllowedHost)

	ints := []struct {
		name string
		dst  *int
	}{
		{"PORT", &cfg.Server.Port},
		{"HORDE_TIMEOUT", &cfg.Horde.Timeout},
		{"SWARM_TIMEOUT", &cfg.Swarm.Timeout},
		{"MONITOR_INTERVAL", &cfg.Monitor.Interval},
		{"MAX_JOB_AGE", &cfg.Monitor.MaxJobAge},
		{"TIMEOUT_SHUTDOWN", &cfg.Timeouts.Shutdown},
		{"RETRY_MAX_ATTEMPTS", &cfg.Retry.MaxAttempts},
		{"RETRY_INITIAL_DELAY", &cfg.Retry.InitialDelay},
		{"RETRY_MAX_DELAY", &cfg.Retry.MaxDelay},
	}
	for _, i := range ints {
		if err := envInt(i.name, i.dst); err != nil {
			return err
		}
	}

	return nil
}

// envString overrides dst with the value of the environment variable, if set and non-empty
func envString(name string, dst *string) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		*dst = v
	}
}

// envInt overrides dst with the integer value of the environment variable, if set and non-empty
func envInt(name string, dst *int) error {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid %s value: %w", name, err)
	}
	*dst = i
	return nil
}

// validate checks if the configuration is valid
func validate(cfg *Config) error {
	if cfg.Horde.Host == "" {
		return fmt.Errorf("horde host is required (HORDE_HOST)")
	}
	if err := validateURL(cfg.Horde.Host); err != nil {
		return fmt.Errorf("invalid horde host: %w", err)
	}
	if cfg.Horde.PublicURL != "" {
		if err := validateURL(cfg.Horde.PublicURL); err != nil {
			return fmt.Errorf("invalid horde public URL: %w", err)
		}
	}
	if err := validateAllowedHost(cfg.Swarm.AllowedHost); err != nil {
		return fmt.Errorf("invalid swarm allowed host: %w", err)
	}
	if cfg.Horde.APIKey == "" {
		return fmt.Errorf("horde API key is required (HORDE_API_KEY)")
	}
	if cfg.Horde.TemplateId == "" {
		return fmt.Errorf("horde template id is required (HORDE_TEMPLATE_ID)")
	}
	if cfg.Horde.StreamId == "" {
		return fmt.Errorf("horde stream id is required (HORDE_STREAM_ID)")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", cfg.Server.Port)
	}
	if cfg.Monitor.Interval <= 0 {
		return fmt.Errorf("monitor interval must be positive, got %d", cfg.Monitor.Interval)
	}
	if cfg.Monitor.MaxJobAge <= 0 {
		return fmt.Errorf("max job age must be positive, got %d", cfg.Monitor.MaxJobAge)
	}
	if cfg.Horde.Timeout <= 0 || cfg.Swarm.Timeout <= 0 || cfg.Timeouts.Shutdown <= 0 {
		return fmt.Errorf("timeouts must be positive")
	}
	if cfg.Retry.MaxAttempts <= 0 {
		return fmt.Errorf("retry max attempts must be at least 1, got %d", cfg.Retry.MaxAttempts)
	}
	if cfg.Retry.InitialDelay < 0 || cfg.Retry.MaxDelay < 0 {
		return fmt.Errorf("retry delays must not be negative")
	}
	if _, err := zerolog.ParseLevel(cfg.LogLevel); err != nil {
		return fmt.Errorf("invalid log level %q", cfg.LogLevel)
	}
	if cfg.DataDir == "" {
		return fmt.Errorf("data directory is required (DATA_DIR)")
	}
	return nil
}

// validateURL checks that s is an absolute http(s) URL
func validateURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", s)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", s)
	}
	return nil
}

// setDefaults sets default values for optional configuration fields
func setDefaults(cfg *Config) {
	// Server defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	// Horde defaults
	cfg.Horde.Host = strings.TrimRight(cfg.Horde.Host, "/")
	cfg.Horde.PublicURL = strings.TrimRight(cfg.Horde.PublicURL, "/")
	if cfg.Horde.PublicURL == "" {
		cfg.Horde.PublicURL = cfg.Horde.Host
	}
	if cfg.Horde.Timeout == 0 {
		cfg.Horde.Timeout = 30
	}

	// Swarm defaults
	if cfg.Swarm.Timeout == 0 {
		cfg.Swarm.Timeout = 30
	}

	// Monitor defaults
	if cfg.Monitor.Interval == 0 {
		cfg.Monitor.Interval = 30
	}
	if cfg.Monitor.MaxJobAge == 0 {
		cfg.Monitor.MaxJobAge = 14400
	}

	// Timeout defaults
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

	// Data directory default
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}

	// Set default clock if none provided
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
}

// GetShutdownTimeout returns the shutdown timeout as a time.Duration
func (c *Config) GetShutdownTimeout() time.Duration {
	return time.Duration(c.Timeouts.Shutdown) * time.Second
}

// GetMonitorInterval returns the monitor interval as a time.Duration
func (c *Config) GetMonitorInterval() time.Duration {
	return time.Duration(c.Monitor.Interval) * time.Second
}

// GetHordeTimeout returns the Horde HTTP client timeout as a time.Duration
func (c *Config) GetHordeTimeout() time.Duration {
	return time.Duration(c.Horde.Timeout) * time.Second
}

// GetSwarmTimeout returns the Swarm HTTP client timeout as a time.Duration
func (c *Config) GetSwarmTimeout() time.Duration {
	return time.Duration(c.Swarm.Timeout) * time.Second
}

// GetMaxJobAge returns the maximum time a job is tracked before being reported as failed
func (c *Config) GetMaxJobAge() time.Duration {
	return time.Duration(c.Monitor.MaxJobAge) * time.Second
}

// GetJobCreationBudget returns how long creating a Horde job and reporting the
// initial status to Swarm may take: every Horde attempt may hit its timeout,
// plus the backoff delays between attempts, plus one Swarm update.
func (c *Config) GetJobCreationBudget() time.Duration {
	attempts := c.Retry.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	budget := time.Duration(attempts) * c.GetHordeTimeout()
	budget += time.Duration(attempts-1) * time.Duration(c.Retry.MaxDelay) * time.Second
	budget += c.GetSwarmTimeout()
	return budget
}

// validateAllowedHost checks that s is empty or a bare host with an optional port
func validateAllowedHost(s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, "/?#@ ") {
		return fmt.Errorf("%q must be a host name with an optional port, without scheme or path", s)
	}
	u, err := url.Parse("https://" + s)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%q is not a valid host", s)
	}
	return nil
}

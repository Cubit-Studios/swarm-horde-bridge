package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfigYAML = `
server:
  port: 8080

horde:
  host: "http://horde.example.com/"
  api_key: "test-key"
  timeout: 30
  template_id: "file-template"
  stream_id: "file-stream"

swarm:
  host: "https://swarm.typo.example"
  timeout: 30

monitor:
  interval: 30

timeouts:
  http_client: 30
  shutdown: 5

retry:
  max_attempts: 3
  initial_delay: 1
  max_delay: 5

log_level: "info"
data_dir: "/var/lib/bridge"
`

// requiredEnv is the minimal environment for an env-only configuration
var requiredEnv = map[string]string{
	"HORDE_HOST":        "https://horde.internal:5000",
	"HORDE_API_KEY":     "env-key",
	"HORDE_TEMPLATE_ID": "env-template",
	"HORDE_STREAM_ID":   "env-stream",
}

func TestLoad(t *testing.T) {
	configFile := createTempConfig(t, validConfigYAML)
	missingFile := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	tests := []struct {
		name        string
		configPath  string
		envVars     map[string]string
		wantErr     bool
		validate    func(*testing.T, *Config)
		errContains []string
	}{
		{
			name:       "valid config file",
			configPath: configFile,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 8080, cfg.Server.Port)
				assert.Equal(t, "http://horde.example.com", cfg.Horde.Host, "trailing slash trimmed")
				assert.Equal(t, "http://horde.example.com", cfg.Horde.PublicURL, "public URL defaults to host")
				assert.Equal(t, "test-key", cfg.Horde.APIKey)
				assert.Equal(t, "file-template", cfg.Horde.TemplateId)
				assert.Equal(t, "file-stream", cfg.Horde.StreamId)
				assert.Equal(t, 30, cfg.Monitor.Interval)
				assert.Equal(t, "/var/lib/bridge", cfg.DataDir)
				assert.Equal(t, configFile, cfg.ConfigFile)
			},
		},
		{
			name:        "non-existent file without environment",
			configPath:  missingFile,
			wantErr:     true,
			errContains: []string{"not found", "horde host is required"},
		},
		{
			name:       "non-existent file with environment only",
			configPath: missingFile,
			envVars:    requiredEnv,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "", cfg.ConfigFile)
				assert.Equal(t, "https://horde.internal:5000", cfg.Horde.Host)
				assert.Equal(t, "env-key", cfg.Horde.APIKey)
				assert.Equal(t, "env-template", cfg.Horde.TemplateId)
				assert.Equal(t, "env-stream", cfg.Horde.StreamId)
				// defaults
				assert.Equal(t, 8080, cfg.Server.Port)
				assert.Equal(t, 30, cfg.Horde.Timeout)
				assert.Equal(t, 30, cfg.Swarm.Timeout)
				assert.Equal(t, 30, cfg.Monitor.Interval)
				assert.Equal(t, 14400, cfg.Monitor.MaxJobAge)
				assert.Equal(t, "", cfg.Server.WebhookToken, "webhook token is optional")
				assert.Equal(t, 5, cfg.Timeouts.Shutdown)
				assert.Equal(t, 3, cfg.Retry.MaxAttempts)
				assert.Equal(t, 1, cfg.Retry.InitialDelay)
				assert.Equal(t, 5, cfg.Retry.MaxDelay)
				assert.Equal(t, "info", cfg.LogLevel)
				assert.Equal(t, "data", cfg.DataDir, "relative to the working directory")
				assert.Equal(t, "https://horde.internal:5000", cfg.Horde.PublicURL)
			},
		},
		{
			name:       "empty config path uses environment only",
			configPath: "",
			envVars:    requiredEnv,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "env-template", cfg.Horde.TemplateId)
			},
		},
		{
			name:       "environment variables override file",
			configPath: configFile,
			envVars: map[string]string{
				"PORT":                "9090",
				"HORDE_HOST":          "http://horde:5000",
				"HORDE_API_KEY":       "env-key",
				"HORDE_PUBLIC_URL":    "https://horde.example.com/",
				"HORDE_TEMPLATE_ID":   "env-template",
				"HORDE_STREAM_ID":     "env-stream",
				"HORDE_TIMEOUT":       "10",
				"SWARM_TIMEOUT":       "11",
				"MONITOR_INTERVAL":    "12",
				"MAX_JOB_AGE":         "600",
				"WEBHOOK_TOKEN":       "s3cret",
				"SWARM_ALLOWED_HOST":  "swarm.example.com:8443",
				"TIMEOUT_SHUTDOWN":    "13",
				"RETRY_MAX_ATTEMPTS":  "4",
				"RETRY_INITIAL_DELAY": "2",
				"RETRY_MAX_DELAY":     "20",
				"LOG_LEVEL":           "debug",
				"DATA_DIR":            "/srv/data",
			},
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 9090, cfg.Server.Port)
				assert.Equal(t, "http://horde:5000", cfg.Horde.Host)
				assert.Equal(t, "env-key", cfg.Horde.APIKey)
				assert.Equal(t, "https://horde.example.com", cfg.Horde.PublicURL)
				assert.Equal(t, "env-template", cfg.Horde.TemplateId)
				assert.Equal(t, "env-stream", cfg.Horde.StreamId)
				assert.Equal(t, 10, cfg.Horde.Timeout)
				assert.Equal(t, 11, cfg.Swarm.Timeout)
				assert.Equal(t, 12, cfg.Monitor.Interval)
				assert.Equal(t, 600, cfg.Monitor.MaxJobAge)
				assert.Equal(t, "s3cret", cfg.Server.WebhookToken)
				assert.Equal(t, "swarm.example.com:8443", cfg.Swarm.AllowedHost)
				assert.Equal(t, 13, cfg.Timeouts.Shutdown)
				assert.Equal(t, 4, cfg.Retry.MaxAttempts)
				assert.Equal(t, 2, cfg.Retry.InitialDelay)
				assert.Equal(t, 20, cfg.Retry.MaxDelay)
				assert.Equal(t, "debug", cfg.LogLevel)
				assert.Equal(t, "/srv/data", cfg.DataDir)
			},
		},
		{
			name:        "invalid port in env",
			configPath:  configFile,
			envVars:     map[string]string{"PORT": "invalid"},
			wantErr:     true,
			errContains: []string{"invalid PORT value"},
		},
		{
			name:        "invalid monitor interval in env",
			configPath:  configFile,
			envVars:     map[string]string{"MONITOR_INTERVAL": "soon"},
			wantErr:     true,
			errContains: []string{"invalid MONITOR_INTERVAL value"},
		},
		{
			name:        "invalid max job age in env",
			configPath:  configFile,
			envVars:     map[string]string{"MAX_JOB_AGE": "-5"},
			wantErr:     true,
			errContains: []string{"max job age must be positive"},
		},
		{
			name:        "invalid log level",
			configPath:  configFile,
			envVars:     map[string]string{"LOG_LEVEL": "verbose"},
			wantErr:     true,
			errContains: []string{"invalid log level"},
		},
		{
			name:        "horde host without scheme",
			configPath:  configFile,
			envVars:     map[string]string{"HORDE_HOST": "horde.example.com"},
			wantErr:     true,
			errContains: []string{"invalid horde host"},
		},
		{
			name: "missing required fields",
			configPath: createTempConfig(t, `
server:
  port: 8080
`),
			wantErr:     true,
			errContains: []string{"horde host is required"},
		},
		{
			name: "missing template id",
			configPath: createTempConfig(t, `
horde:
  host: "http://horde"
  api_key: "k"
  stream_id: "s"
`),
			wantErr:     true,
			errContains: []string{"horde template id is required"},
		},
		{
			name:        "invalid yaml",
			configPath:  createTempConfig(t, "server: [unclosed"),
			wantErr:     true,
			errContains: []string{"parsing config file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment variables
			clearEnvVars()

			// Set test environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}
			defer clearEnvVars()

			// Load configuration
			cfg, err := Load(tt.configPath)

			// Check error cases
			if tt.wantErr {
				require.Error(t, err)
				for _, s := range tt.errContains {
					assert.Contains(t, err.Error(), s)
				}
				return
			}

			// Verify successful cases
			require.NoError(t, err)
			require.NotNil(t, cfg)
			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestConfigHelperMethods(t *testing.T) {
	cfg := &Config{
		Horde:    HordeConfig{Timeout: 7},
		Swarm:    SwarmConfig{Timeout: 9},
		Timeouts: TimeoutConfig{Shutdown: 5},
		Monitor:  MonitorConfig{Interval: 15},
	}

	assert.Equal(t, 7*time.Second, cfg.GetHordeTimeout())
	assert.Equal(t, 9*time.Second, cfg.GetSwarmTimeout())
	assert.Equal(t, 5*time.Second, cfg.GetShutdownTimeout())
	assert.Equal(t, 15*time.Second, cfg.GetMonitorInterval())
}

func TestValidate(t *testing.T) {
	valid := func() Config {
		cfg := Config{
			Server: ServerConfig{Port: 8080},
			Horde: HordeConfig{
				Host:       "http://example.com",
				APIKey:     "test-key",
				TemplateId: "template",
				StreamId:   "stream",
			},
		}
		setDefaults(&cfg)
		return cfg
	}

	tests := []struct {
		name        string
		mutate      func(*Config)
		errContains string
	}{
		{name: "valid config", mutate: func(*Config) {}},
		{name: "missing horde host", mutate: func(c *Config) { c.Horde.Host = "" }, errContains: "horde host is required"},
		{name: "missing api key", mutate: func(c *Config) { c.Horde.APIKey = "" }, errContains: "horde API key is required"},
		{name: "missing stream id", mutate: func(c *Config) { c.Horde.StreamId = "" }, errContains: "horde stream id is required"},
		{name: "invalid public url", mutate: func(c *Config) { c.Horde.PublicURL = "ftp://x" }, errContains: "invalid horde public URL"},
		{name: "invalid port", mutate: func(c *Config) { c.Server.Port = 70000 }, errContains: "invalid port number"},
		{name: "negative interval", mutate: func(c *Config) { c.Monitor.Interval = -1 }, errContains: "monitor interval"},
		{name: "zero attempts", mutate: func(c *Config) { c.Retry.MaxAttempts = -1 }, errContains: "retry max attempts"},
		{name: "negative max job age", mutate: func(c *Config) { c.Monitor.MaxJobAge = -1 }, errContains: "max job age"},
		{name: "allowed host with scheme", mutate: func(c *Config) { c.Swarm.AllowedHost = "https://swarm" }, errContains: "invalid swarm allowed host"},
		{name: "allowed host with path", mutate: func(c *Config) { c.Swarm.AllowedHost = "swarm/api" }, errContains: "invalid swarm allowed host"},
		{name: "allowed host with port", mutate: func(c *Config) { c.Swarm.AllowedHost = "swarm.example.com:8443" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.mutate(&cfg)
			err := validate(&cfg)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestSetDefaults(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, 30, cfg.Horde.Timeout)
	assert.Equal(t, 30, cfg.Swarm.Timeout)
	assert.Equal(t, 30, cfg.Monitor.Interval)
	assert.Equal(t, 14400, cfg.Monitor.MaxJobAge)
	assert.Equal(t, 5, cfg.Timeouts.Shutdown)
	assert.Equal(t, 3, cfg.Retry.MaxAttempts)
	assert.Equal(t, 1, cfg.Retry.InitialDelay)
	assert.Equal(t, 5, cfg.Retry.MaxDelay)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "data", cfg.DataDir, "relative to the working directory")
	assert.NotNil(t, cfg.Clock)
}

// Helper functions

func clearEnvVars() {
	envVars := []string{
		"PORT",
		"HORDE_HOST",
		"HORDE_API_KEY",
		"HORDE_PUBLIC_URL",
		"HORDE_TEMPLATE_ID",
		"HORDE_STREAM_ID",
		"HORDE_TIMEOUT",
		"SWARM_TIMEOUT",
		"MONITOR_INTERVAL",
		"MAX_JOB_AGE",
		"WEBHOOK_TOKEN",
		"SWARM_ALLOWED_HOST",
		"TIMEOUT_SHUTDOWN",
		"RETRY_MAX_ATTEMPTS",
		"RETRY_INITIAL_DELAY",
		"RETRY_MAX_DELAY",
		"LOG_LEVEL",
		"DATA_DIR",
	}

	for _, env := range envVars {
		os.Unsetenv(env)
	}
}

func createTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestGetJobCreationBudget(t *testing.T) {
	cfg := &Config{
		Horde: HordeConfig{Timeout: 30},
		Swarm: SwarmConfig{Timeout: 10},
		Retry: RetryConfig{MaxAttempts: 3, MaxDelay: 5},
	}
	// 3 attempts x 30s + 2 delays x 5s + one Swarm update (10s)
	assert.Equal(t, 110*time.Second, cfg.GetJobCreationBudget())
	assert.Equal(t, 0*time.Second, (&Config{}).GetJobCreationBudget())

	cfg.Monitor.MaxJobAge = 60
	assert.Equal(t, time.Minute, cfg.GetMaxJobAge())
}

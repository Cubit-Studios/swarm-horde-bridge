package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	// Create a temporary test config file
	configContent := []byte(`
server:
  port: 8080

horde:
  host: "http://horde.example.com"
  api_key: "test-key"
  timeout: 30

swarm:
  host: "http://localhost"
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
`)

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(configContent)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	tests := []struct {
		name        string
		configPath  string
		envVars     map[string]string
		wantErr     bool
		validate    func(*testing.T, *Config)
		errContains string
	}{
		{
			name:       "valid config file",
			configPath: tmpfile.Name(),
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 8080, cfg.Server.Port)
				assert.Equal(t, "http://horde.example.com", cfg.Horde.Host)
				assert.Equal(t, "test-key", cfg.Horde.APIKey)
				assert.Equal(t, "http://localhost", cfg.Swarm.Host)
				assert.Equal(t, 30, cfg.Monitor.Interval)
			},
		},
		{
			name:        "non-existent file",
			configPath:  "nonexistent.yaml",
			wantErr:     true,
			errContains: "reading config file",
		},
		{
			name:       "environment variables override",
			configPath: tmpfile.Name(),
			envVars: map[string]string{
				"PORT":          "9090",
				"HORDE_API_KEY": "env-key",
				"LOG_LEVEL":     "debug",
			},
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 9090, cfg.Server.Port)
				assert.Equal(t, "env-key", cfg.Horde.APIKey)
				assert.Equal(t, "debug", cfg.LogLevel)
			},
		},
		{
			name:       "invalid port in env",
			configPath: tmpfile.Name(),
			envVars: map[string]string{
				"PORT": "invalid",
			},
			wantErr:     true,
			errContains: "invalid PORT value",
		},
		{
			name:       "invalid timeout in env",
			configPath: tmpfile.Name(),
			envVars: map[string]string{
				"TIMEOUT_HTTP_CLIENT": "invalid",
			},
			wantErr:     true,
			errContains: "invalid TIMEOUT_HTTP_CLIENT value",
		},
		{
			name: "missing required fields",
			configPath: createTempConfig(t, `
server:
  port: 8080
`),
			wantErr:     true,
			errContains: "horde host is required",
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
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
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
		Timeouts: TimeoutConfig{
			HTTPClient: 30,
			Shutdown:   5,
		},
		Monitor: MonitorConfig{
			Interval: 15,
		},
	}

	t.Run("GetHTTPClientTimeout", func(t *testing.T) {
		expected := 30 * time.Second
		assert.Equal(t, expected, cfg.GetHTTPClientTimeout())
	})

	t.Run("GetShutdownTimeout", func(t *testing.T) {
		expected := 5 * time.Second
		assert.Equal(t, expected, cfg.GetShutdownTimeout())
	})

	t.Run("GetMonitorInterval", func(t *testing.T) {
		expected := 15 * time.Second
		assert.Equal(t, expected, cfg.GetMonitorInterval())
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			cfg: Config{
				Server: ServerConfig{Port: 8080},
				Horde: HordeConfig{
					Host:   "http://example.com",
					APIKey: "test-key",
				},
			},
			wantErr: false,
		},
		{
			name: "missing horde host",
			cfg: Config{
				Server: ServerConfig{Port: 8080},
				Horde: HordeConfig{
					APIKey: "test-key",
				},
			},
			wantErr:     true,
			errContains: "horde host is required",
		},
		{
			name: "missing api key",
			cfg: Config{
				Server: ServerConfig{Port: 8080},
				Horde: HordeConfig{
					Host: "http://example.com",
				},
			},
			wantErr:     true,
			errContains: "horde API key is required",
		},
		{
			name: "invalid port",
			cfg: Config{
				Server: ServerConfig{Port: 70000},
				Horde: HordeConfig{
					Host:   "http://example.com",
					APIKey: "test-key",
				},
			},
			wantErr:     true,
			errContains: "invalid port number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(&tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
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
	assert.Equal(t, "http://localhost", cfg.Swarm.Host)
	assert.Equal(t, 30, cfg.Swarm.Timeout)
	assert.Equal(t, 30, cfg.Monitor.Interval)
	assert.Equal(t, 30, cfg.Timeouts.HTTPClient)
	assert.Equal(t, 5, cfg.Timeouts.Shutdown)
	assert.Equal(t, 3, cfg.Retry.MaxAttempts)
	assert.Equal(t, 1, cfg.Retry.InitialDelay)
	assert.Equal(t, 5, cfg.Retry.MaxDelay)
	assert.Equal(t, "info", cfg.LogLevel)
}

// Helper functions

func clearEnvVars() {
	envVars := []string{
		"PORT",
		"HORDE_HOST",
		"HORDE_API_KEY",
		"HORDE_TIMEOUT",
		"SWARM_HOST",
		"SWARM_TIMEOUT",
		"MONITOR_INTERVAL",
		"TIMEOUT_HTTP_CLIENT",
		"TIMEOUT_SHUTDOWN",
		"RETRY_MAX_ATTEMPTS",
		"RETRY_INITIAL_DELAY",
		"RETRY_MAX_DELAY",
		"LOG_LEVEL",
	}

	for _, env := range envVars {
		os.Unsetenv(env)
	}
}

func createTempConfig(t *testing.T, content string) string {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(tmpfile.Name()) })

	_, err = tmpfile.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	return tmpfile.Name()
}

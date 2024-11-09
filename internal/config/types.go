package config

import "time"

// Clock interface for better testing
type Clock interface {
	Now() time.Time
}

// RealClock implements Clock interface with actual time
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

// Config represents the application configuration
type Config struct {
	Server   ServerConfig  `yaml:"server"`
	Horde    HordeConfig   `yaml:"horde"`
	Swarm    SwarmConfig   `yaml:"swarm"`
	Monitor  MonitorConfig `yaml:"monitor"`
	Timeouts TimeoutConfig `yaml:"timeouts"`
	Retry    RetryConfig   `yaml:"retry"`
	LogLevel string        `yaml:"log_level" env:"LOG_LEVEL" default:"info"`
	// Clock for time operations, defaults to RealClock
	Clock Clock
}

// ServerConfig holds the HTTP server configuration
type ServerConfig struct {
	Port int `yaml:"port" env:"PORT" default:"8080"`
}

// HordeConfig holds the Horde API configuration
type HordeConfig struct {
	Host       string `yaml:"host" env:"HORDE_HOST" required:"true"`
	APIKey     string `yaml:"api_key" env:"HORDE_API_KEY" required:"true"`
	Timeout    int    `yaml:"timeout" env:"HORDE_TIMEOUT" default:"30"`
	TemplateId string `yaml:"template_id"`
	StreamId   string `yaml:"stream_id"`
}

// SwarmConfig holds the Swarm API configuration
type SwarmConfig struct {
	Host    string `yaml:"host" env:"SWARM_HOST" default:"http://localhost"`
	Timeout int    `yaml:"timeout" env:"SWARM_TIMEOUT" default:"30"`
}

// MonitorConfig holds the job monitoring configuration
type MonitorConfig struct {
	Interval int `yaml:"interval" env:"MONITOR_INTERVAL" default:"30"`
}

// TimeoutConfig holds various timeout configurations
type TimeoutConfig struct {
	HTTPClient int `yaml:"http_client" env:"TIMEOUT_HTTP_CLIENT" default:"30"`
	Shutdown   int `yaml:"shutdown" env:"TIMEOUT_SHUTDOWN" default:"5"`
}

// RetryConfig holds retry-related configurations
type RetryConfig struct {
	MaxAttempts  int `yaml:"max_attempts" env:"RETRY_MAX_ATTEMPTS" default:"3"`
	InitialDelay int `yaml:"initial_delay" env:"RETRY_INITIAL_DELAY" default:"1"`
	MaxDelay     int `yaml:"max_delay" env:"RETRY_MAX_DELAY" default:"5"`
}

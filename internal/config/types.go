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

// Config represents the application configuration.
//
// Every field can be set from the YAML config file and/or from an environment
// variable (environment variables take precedence). The config file is
// optional: when it does not exist the configuration comes from the
// environment only.
type Config struct {
	Server   ServerConfig  `yaml:"server"`
	Horde    HordeConfig   `yaml:"horde"`
	Swarm    SwarmConfig   `yaml:"swarm"`
	Monitor  MonitorConfig `yaml:"monitor"`
	Timeouts TimeoutConfig `yaml:"timeouts"`
	Retry    RetryConfig   `yaml:"retry"`
	LogLevel string        `yaml:"log_level" env:"LOG_LEVEL" default:"info"`
	// DataDir is where the job store (jobs.json) is persisted. A relative path
	// is resolved against the working directory (the Docker image sets /data).
	DataDir string `yaml:"data_dir" env:"DATA_DIR" default:"data"`

	// ConfigFile is the path of the config file that was loaded, empty when
	// the configuration comes from the environment only.
	ConfigFile string `yaml:"-"`
	// Clock for time operations, defaults to RealClock
	Clock Clock `yaml:"-"`
}

// ServerConfig holds the HTTP server configuration
type ServerConfig struct {
	Port int `yaml:"port" env:"PORT" default:"8080"`
	// WebhookToken, when set, must be sent by Swarm in the X-Webhook-Token header
	WebhookToken string `yaml:"webhook_token" env:"WEBHOOK_TOKEN"`
}

// HordeConfig holds the Horde API configuration
type HordeConfig struct {
	// Host is the base URL used to call the Horde API (may be an internal URL)
	Host   string `yaml:"host" env:"HORDE_HOST" required:"true"`
	APIKey string `yaml:"api_key" env:"HORDE_API_KEY" required:"true"`
	// PublicURL is the base URL used in links posted to Swarm, defaults to Host
	PublicURL  string `yaml:"public_url" env:"HORDE_PUBLIC_URL"`
	Timeout    int    `yaml:"timeout" env:"HORDE_TIMEOUT" default:"30"`
	TemplateId string `yaml:"template_id" env:"HORDE_TEMPLATE_ID" required:"true"`
	StreamId   string `yaml:"stream_id" env:"HORDE_STREAM_ID" required:"true"`
}

// SwarmConfig holds the Swarm configuration.
//
// The bridge never calls Swarm on a configured host: status updates are POSTed
// to the update URL that Swarm supplies in each webhook request. The former
// `swarm.host` setting was never used and has been removed (it is ignored if
// still present in a config file).
type SwarmConfig struct {
	// Timeout is the HTTP timeout (seconds) for status updates sent to Swarm
	Timeout int `yaml:"timeout" env:"SWARM_TIMEOUT" default:"30"`
	// AllowedHost (hostname with optional port) restricts the update URLs the
	// bridge will POST to: https on that host only (http is also allowed for
	// localhost / 127.0.0.1). Empty disables the check.
	AllowedHost string `yaml:"allowed_host" env:"SWARM_ALLOWED_HOST"`
}

// MonitorConfig holds the job monitoring configuration
type MonitorConfig struct {
	Interval int `yaml:"interval" env:"MONITOR_INTERVAL" default:"30"`
	// MaxJobAge (seconds) after which an unfinished job is reported as failed
	MaxJobAge int `yaml:"max_job_age" env:"MAX_JOB_AGE" default:"14400"`
}

// TimeoutConfig holds various timeout configurations
type TimeoutConfig struct {
	Shutdown int `yaml:"shutdown" env:"TIMEOUT_SHUTDOWN" default:"5"`
}

// RetryConfig holds retry-related configurations
type RetryConfig struct {
	MaxAttempts  int `yaml:"max_attempts" env:"RETRY_MAX_ATTEMPTS" default:"3"`
	InitialDelay int `yaml:"initial_delay" env:"RETRY_INITIAL_DELAY" default:"1"`
	MaxDelay     int `yaml:"max_delay" env:"RETRY_MAX_DELAY" default:"5"`
}

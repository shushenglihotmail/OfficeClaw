// Package config handles loading and validating OfficeClaw configuration.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for OfficeClaw.
type Config struct {
	WhatsApp  WhatsAppConfig  `yaml:"whatsapp"`
	LLM       LLMConfig       `yaml:"llm"`
	Tools     ToolsConfig     `yaml:"tools"`
	Tasks     map[string]Task `yaml:"tasks"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// WhatsAppConfig holds WhatsApp integration settings.
type WhatsAppConfig struct {
	// Path to SQLite database for session storage
	DatabasePath string `yaml:"database_path"`
	// Trigger prefix for OfficeClaw agent (e.g., "OC:")
	TriggerPrefix string `yaml:"trigger_prefix"`
	// Trigger prefix for Claude CLI agent (e.g., "OCC:")
	ClaudeTrigger string `yaml:"claude_trigger"`
	// Working folder for Claude CLI agent
	ClaudeWorkingFolder string `yaml:"claude_working_folder"`
	// Keyword to reset Claude CLI session (e.g., send "OCC: reset")
	ClaudeSessionResetKeyword string `yaml:"claude_session_reset_keyword"`
	// Default task when none specified in message
	DefaultTask string `yaml:"default_task"`
}

// LLMConfig holds multi-provider LLM settings.
type LLMConfig struct {
	Provider              string          `yaml:"provider"`
	Anthropic             AnthropicConfig `yaml:"anthropic"`
	Azure                 AzureConfig     `yaml:"azure"`
	OpenAI                OpenAIConfig    `yaml:"openai"`
	Temperature           float64         `yaml:"temperature"`
	RequestTimeoutSeconds int             `yaml:"request_timeout_seconds"`
}

// AnthropicConfig for Claude models via Claude Code CLI.
// Uses the pre-authenticated CLI (SSO auth) - no API key required.
type AnthropicConfig struct {
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
	// Path to Claude CLI executable (auto-detected if empty)
	CLIPath string `yaml:"cli_path"`
}

// AzureConfig for Azure OpenAI / Azure Foundry.
type AzureConfig struct {
	Endpoint   string       `yaml:"endpoint"`
	APIKey     string       `yaml:"api_key"`
	APIVersion string       `yaml:"api_version"`
	Models     []ModelEntry `yaml:"models"`
}

// ModelEntry describes a model deployment (mirrors LLMCrawl LLM_MODELS).
type ModelEntry struct {
	Name            string `yaml:"name"`
	DeploymentName  string `yaml:"deployment_name"`
	ProviderType    string `yaml:"provider_type"`
	MaxOutputTokens int    `yaml:"max_output_tokens"`
}

// OpenAIConfig for direct OpenAI API access.
type OpenAIConfig struct {
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
}

// ToolsConfig holds per-tool settings.
type ToolsConfig struct {
	FileAccess    FileAccessConfig    `yaml:"file_access"`
	Messaging     MessagingConfig     `yaml:"messaging"`
	TaskExecution TaskExecutionConfig `yaml:"task_execution"`
	VPN           VPNConfig           `yaml:"vpn"`
}

// FileAccessConfig configures the local file read tool.
type FileAccessConfig struct {
	Enabled       bool     `yaml:"enabled"`
	AllowedPaths  []string `yaml:"allowed_paths"`
	MaxFileSizeMB int      `yaml:"max_file_size_mb"`
}

// MessagingConfig for WhatsApp reply tool.
type MessagingConfig struct {
	Enabled bool `yaml:"enabled"`
}

// TaskExecutionConfig for the task tool.
type TaskExecutionConfig struct {
	Enabled bool `yaml:"enabled"`
}

// VPNConfig configures the VPN management tool.
type VPNConfig struct {
	Enabled               bool     `yaml:"enabled"`
	VPNNames              []string `yaml:"vpn_names"`
	ConnectTimeoutSeconds int      `yaml:"connect_timeout_seconds"`
	KeepAliveEnabled      bool     `yaml:"keep_alive_enabled"`
	KeepAliveMinutes      int      `yaml:"keep_alive_minutes"`
	VerifyPath            string   `yaml:"verify_path"`
}

// Task defines a runnable task.
type Task struct {
	Description    string `yaml:"description"`
	Command        string `yaml:"command,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	Schedule       string `yaml:"schedule,omitempty"` // cron expression
}

// TelemetryConfig controls observability.
type TelemetryConfig struct {
	Enabled    bool             `yaml:"enabled"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	OTel       OTelConfig       `yaml:"otel"`
}

// PrometheusConfig for metrics endpoint.
type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

// OTelConfig for OpenTelemetry.
type OTelConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ServiceName  string `yaml:"service_name"`
	OTLPEndpoint string `yaml:"otlp_endpoint,omitempty"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
}

// Load reads and parses the config file, applying env var overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyEnvOverrides(cfg)
	applyDefaults(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides lets environment variables override config values.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CLAUDE_CLI_PATH"); v != "" {
		cfg.LLM.Anthropic.CLIPath = v
	}
	if v := os.Getenv("AZURE_OPENAI_ENDPOINT"); v != "" {
		cfg.LLM.Azure.Endpoint = v
	}
	if v := os.Getenv("AZURE_OPENAI_API_KEY"); v != "" {
		cfg.LLM.Azure.APIKey = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.LLM.OpenAI.APIKey = v
	}
	if v := os.Getenv("WHATSAPP_DB_PATH"); v != "" {
		cfg.WhatsApp.DatabasePath = v
	}
}

// applyDefaults fills in zero values with sensible defaults.
func applyDefaults(cfg *Config) {
	// WhatsApp defaults
	if cfg.WhatsApp.DatabasePath == "" {
		cfg.WhatsApp.DatabasePath = "whatsapp.db"
	}
	if cfg.WhatsApp.TriggerPrefix == "" {
		cfg.WhatsApp.TriggerPrefix = "OC:"
	}
	if cfg.WhatsApp.ClaudeTrigger == "" {
		cfg.WhatsApp.ClaudeTrigger = "OCC:"
	}
	if cfg.WhatsApp.ClaudeSessionResetKeyword == "" {
		cfg.WhatsApp.ClaudeSessionResetKeyword = "reset"
	}
	// ClaudeWorkingFolder defaults to current directory if not set
	if cfg.WhatsApp.DefaultTask == "" {
		cfg.WhatsApp.DefaultTask = "assist"
	}

	// LLM defaults
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "anthropic"
	}
	if cfg.LLM.Temperature == 0 {
		cfg.LLM.Temperature = 0.1
	}
	if cfg.LLM.RequestTimeoutSeconds <= 0 {
		cfg.LLM.RequestTimeoutSeconds = 180
	}
	if cfg.LLM.Anthropic.MaxTokens <= 0 {
		cfg.LLM.Anthropic.MaxTokens = 8192
	}
	if cfg.LLM.Anthropic.Model == "" {
		cfg.LLM.Anthropic.Model = "claude-sonnet-4-20250514"
	}

	// Telemetry defaults
	if cfg.Telemetry.Prometheus.Port <= 0 {
		cfg.Telemetry.Prometheus.Port = 9090
	}
	if cfg.Telemetry.Prometheus.Path == "" {
		cfg.Telemetry.Prometheus.Path = "/metrics"
	}
	if cfg.Telemetry.OTel.ServiceName == "" {
		cfg.Telemetry.OTel.ServiceName = "officeclaw"
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.File == "" {
		cfg.Logging.File = "officeclaw.log"
	}
	if cfg.Logging.MaxSizeMB <= 0 {
		cfg.Logging.MaxSizeMB = 50
	}
	if cfg.Logging.MaxBackups <= 0 {
		cfg.Logging.MaxBackups = 3
	}

	// Tool defaults
	if cfg.Tools.FileAccess.MaxFileSizeMB <= 0 {
		cfg.Tools.FileAccess.MaxFileSizeMB = 10
	}

	// VPN defaults
	if cfg.Tools.VPN.ConnectTimeoutSeconds <= 0 {
		cfg.Tools.VPN.ConnectTimeoutSeconds = 30
	}
	if cfg.Tools.VPN.KeepAliveMinutes <= 0 {
		cfg.Tools.VPN.KeepAliveMinutes = 30
	}
	if len(cfg.Tools.VPN.VPNNames) == 0 {
		cfg.Tools.VPN.VPNNames = []string{"MSFT-AzVPN-Manual", "MSFTVPN-Manual"}
	}

}

// Validate checks that required fields are present.
func (c *Config) Validate() error {
	var errs []string

	// Validate LLM provider config
	switch c.LLM.Provider {
	case "anthropic":
		// Uses Claude CLI with SSO auth - no API key required
		// CLI path is auto-detected if not specified
	case "azure":
		if c.LLM.Azure.Endpoint == "" {
			errs = append(errs, "llm.azure.endpoint is required (or set AZURE_OPENAI_ENDPOINT)")
		}
	case "openai":
		if c.LLM.OpenAI.APIKey == "" {
			errs = append(errs, "llm.openai.api_key is required (or set OPENAI_API_KEY)")
		}
	default:
		errs = append(errs, fmt.Sprintf("unsupported llm.provider: %s", c.LLM.Provider))
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

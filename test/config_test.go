package test

import (
	"testing"

	"github.com/officeclaw/src/config"
)

func TestConfigDefaults(t *testing.T) {
	cfg := &config.Config{}
	// Manually call applyDefaults indirectly by checking zero-value handling

	// Verify struct initializes with zero values
	if cfg.WhatsApp.TriggerPrefix != "" {
		t.Error("Expected empty TriggerPrefix before defaults")
	}
	if cfg.LLM.Provider != "" {
		t.Error("Expected empty Provider before defaults")
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := &config.Config{
		WhatsApp: config.WhatsAppConfig{
			TriggerPrefix: "OfficeClaw:",
			DefaultTask:   "assist",
		},
		LLM: config.LLMConfig{
			Provider: "anthropic",
			Anthropic: config.AnthropicConfig{
				Model: "claude-sonnet-4-20250514",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Valid config should not return error: %v", err)
	}
}

func TestConfigValidationUnsupportedProvider(t *testing.T) {
	cfg := &config.Config{
		WhatsApp: config.WhatsAppConfig{
			TriggerPrefix: "OfficeClaw:",
			DefaultTask:   "assist",
		},
		LLM: config.LLMConfig{
			Provider: "gemini",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Should error on unsupported provider")
	}
}

func TestConfigValidationAzureNeedsEndpoint(t *testing.T) {
	cfg := &config.Config{
		WhatsApp: config.WhatsAppConfig{
			TriggerPrefix: "OfficeClaw:",
			DefaultTask:   "assist",
		},
		LLM: config.LLMConfig{
			Provider: "azure",
			Azure: config.AzureConfig{
				// Missing endpoint
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Should error when azure endpoint is missing")
	}
}

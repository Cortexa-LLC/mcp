package config

import (
	"os"
	"strconv"
	"strings"
)

const (
	// EnvMaxFileBytes is the environment variable name for the file size limit.
	EnvMaxFileBytes = "MARKITDOWN_MAX_FILE_BYTES"

	// EnvEnableOpenAI is the environment variable to enable OpenAI image enhancement.
	EnvEnableOpenAI = "MARKITDOWN_ENABLE_OPENAI"

	// EnvOpenAIAPIKey is the environment variable for the OpenAI API key.
	EnvOpenAIAPIKey = "OPENAI_API_KEY"

	// EnvOpenAIModel is the environment variable for the OpenAI model to use.
	EnvOpenAIModel = "MARKITDOWN_OPENAI_MODEL"

	// DefaultMaxFileBytes is the default maximum accepted file size (50 MiB).
	DefaultMaxFileBytes int64 = 50 << 20

	// DefaultOpenAIModel is the default model for image understanding.
	DefaultOpenAIModel = "gpt-4o"
)

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	MaxFileSizeBytes int64
	EnableOpenAI     bool
	OpenAIAPIKey     string
	OpenAIModel      string
}

// MaxFileSizeMB returns the configured limit in whole megabytes.
func (c *Config) MaxFileSizeMB() int64 {
	return c.MaxFileSizeBytes >> 20
}

// HasOpenAI returns true if OpenAI integration is both enabled and has an API key.
func (c *Config) HasOpenAI() bool {
	return c.EnableOpenAI && c.OpenAIAPIKey != ""
}

// Load reads Config from environment variables, falling back to defaults for
// missing or invalid values.
func Load() *Config {
	cfg := &Config{
		MaxFileSizeBytes: DefaultMaxFileBytes,
		OpenAIModel:      DefaultOpenAIModel,
	}

	// File size limit
	if v := os.Getenv(EnvMaxFileBytes); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxFileSizeBytes = n
		}
	}

	// OpenAI configuration
	if v := os.Getenv(EnvEnableOpenAI); v != "" {
		cfg.EnableOpenAI = isTruthy(v)
	}

	if v := os.Getenv(EnvOpenAIAPIKey); v != "" {
		cfg.OpenAIAPIKey = v
	}

	if v := os.Getenv(EnvOpenAIModel); v != "" {
		cfg.OpenAIModel = v
	}

	return cfg
}

// isTruthy returns true for values like "true", "1", "yes", "on" (case-insensitive).
func isTruthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

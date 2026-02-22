package config

import (
	"os"
	"strconv"
)

const (
	// EnvMaxFileBytes is the environment variable name for the file size limit.
	EnvMaxFileBytes = "MARKITDOWN_MAX_FILE_BYTES"

	// DefaultMaxFileBytes is the default maximum accepted file size (50 MiB).
	DefaultMaxFileBytes int64 = 50 << 20
)

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	MaxFileSizeBytes int64
}

// MaxFileSizeMB returns the configured limit in whole megabytes.
func (c *Config) MaxFileSizeMB() int64 {
	return c.MaxFileSizeBytes >> 20
}

// Load reads Config from environment variables, falling back to defaults for
// missing or invalid values.
func Load() *Config {
	cfg := &Config{MaxFileSizeBytes: DefaultMaxFileBytes}
	if v := os.Getenv(EnvMaxFileBytes); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxFileSizeBytes = n
		}
	}
	return cfg
}

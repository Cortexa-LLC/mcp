package config

import (
	"os"
	"strconv"
)

const (
	defaultMaxFileBytes = 50 << 20 // 50 MB
)

// Config holds runtime configuration read from environment variables.
type Config struct {
	// MaxFileSizeBytes is the maximum accepted file size before conversion.
	// Override with MARKITDOWN_MAX_FILE_BYTES.
	MaxFileSizeBytes int64
}

// Load reads Config from environment variables, falling back to defaults.
func Load() *Config {
	cfg := &Config{
		MaxFileSizeBytes: defaultMaxFileBytes,
	}

	if v := os.Getenv("MARKITDOWN_MAX_FILE_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxFileSizeBytes = n
		}
	}

	return cfg
}


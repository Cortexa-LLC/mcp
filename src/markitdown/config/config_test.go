package config

import (
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("MARKITDOWN_MAX_FILE_BYTES", "")

	cfg := Load()

	if cfg.MaxFileSizeBytes != defaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want %d", cfg.MaxFileSizeBytes, defaultMaxFileBytes)
	}
}

func TestLoad_MaxFileBytesFromEnv(t *testing.T) {
	t.Setenv("MARKITDOWN_MAX_FILE_BYTES", "1048576") // 1 MB

	cfg := Load()

	if cfg.MaxFileSizeBytes != 1_048_576 {
		t.Errorf("MaxFileSizeBytes = %d, want 1048576", cfg.MaxFileSizeBytes)
	}
}

func TestLoad_InvalidMaxFileBytesIgnored(t *testing.T) {
	t.Setenv("MARKITDOWN_MAX_FILE_BYTES", "not-a-number")

	cfg := Load()

	// Invalid value should fall back to default.
	if cfg.MaxFileSizeBytes != defaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want default %d", cfg.MaxFileSizeBytes, defaultMaxFileBytes)
	}
}

func TestLoad_ZeroMaxFileBytesIgnored(t *testing.T) {
	t.Setenv("MARKITDOWN_MAX_FILE_BYTES", "0")

	cfg := Load()

	// Zero is not a valid limit; fall back to default.
	if cfg.MaxFileSizeBytes != defaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want default %d", cfg.MaxFileSizeBytes, defaultMaxFileBytes)
	}
}

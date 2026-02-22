package config

import (
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv(EnvMaxFileBytes, "")

	cfg := Load()

	if cfg.MaxFileSizeBytes != DefaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want %d", cfg.MaxFileSizeBytes, DefaultMaxFileBytes)
	}
}

func TestLoad_MaxFileBytesFromEnv(t *testing.T) {
	t.Setenv(EnvMaxFileBytes, "1048576") // 1 MiB

	cfg := Load()

	if cfg.MaxFileSizeBytes != 1_048_576 {
		t.Errorf("MaxFileSizeBytes = %d, want 1048576", cfg.MaxFileSizeBytes)
	}
}

func TestLoad_InvalidMaxFileBytesIgnored(t *testing.T) {
	t.Setenv(EnvMaxFileBytes, "not-a-number")

	cfg := Load()

	if cfg.MaxFileSizeBytes != DefaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want default %d", cfg.MaxFileSizeBytes, DefaultMaxFileBytes)
	}
}

func TestLoad_ZeroMaxFileBytesIgnored(t *testing.T) {
	t.Setenv(EnvMaxFileBytes, "0")

	cfg := Load()

	if cfg.MaxFileSizeBytes != DefaultMaxFileBytes {
		t.Errorf("MaxFileSizeBytes = %d, want default %d", cfg.MaxFileSizeBytes, DefaultMaxFileBytes)
	}
}

func TestMaxFileSizeMB(t *testing.T) {
	cfg := &Config{MaxFileSizeBytes: 10 << 20} // 10 MiB
	if got := cfg.MaxFileSizeMB(); got != 10 {
		t.Errorf("MaxFileSizeMB() = %d, want 10", got)
	}
}

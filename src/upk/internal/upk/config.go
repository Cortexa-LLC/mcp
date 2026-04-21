package upk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectConfig describes a project to federate with
type ProjectConfig struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	KGDatabase string `json:"kg_database"`
}

// Config is the upk configuration
type Config struct {
	DefaultEmbedModel string          `json:"defaultEmbedModel"`
	Projects          []ProjectConfig `json:"projects"`
}

// LoadConfig loads the upk configuration from ~/.upk/config.json
func LoadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	configPath := filepath.Join(home, ".upk", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// SaveConfig saves the upk configuration to ~/.upk/config.json
func SaveConfig(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	configPath := filepath.Join(home, ".upk", "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// GetUserDBPath returns the path to the user's knowledge database
func GetUserDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".upk", "knowledge.db"), nil
}

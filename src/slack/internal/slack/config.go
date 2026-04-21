package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WorkspaceConfig contains credentials for a single Slack workspace
type WorkspaceConfig struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// Config contains all workspace configurations
type Config struct {
	DefaultWorkspace string                     `json:"default_workspace,omitempty"`
	Workspaces       map[string]WorkspaceConfig `json:"workspaces"`
}

// LoadConfig loads the workspace configuration from a file
func LoadConfig(configPath string) (*Config, error) {
	// Expand ~ to home directory
	if configPath[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		configPath = filepath.Join(home, configPath[2:])
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if len(cfg.Workspaces) == 0 {
		return nil, fmt.Errorf("no workspaces configured")
	}

	// Set default workspace if not specified
	if cfg.DefaultWorkspace == "" {
		// Use first workspace as default
		for name := range cfg.Workspaces {
			cfg.DefaultWorkspace = name
			break
		}
	}

	// Validate default workspace exists
	if _, ok := cfg.Workspaces[cfg.DefaultWorkspace]; !ok {
		return nil, fmt.Errorf("default workspace %s not found in workspaces", cfg.DefaultWorkspace)
	}

	return &cfg, nil
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".slack-mcp", "config.json")
}

// GetWorkspace returns the configuration for a specific workspace
func (c *Config) GetWorkspace(name string) (*WorkspaceConfig, error) {
	// Use default if name is empty
	if name == "" {
		name = c.DefaultWorkspace
	}

	ws, ok := c.Workspaces[name]
	if !ok {
		return nil, fmt.Errorf("workspace %s not found", name)
	}

	return &ws, nil
}

// ListWorkspaces returns the names of all configured workspaces
func (c *Config) ListWorkspaces() []string {
	names := make([]string, 0, len(c.Workspaces))
	for name := range c.Workspaces {
		names = append(names, name)
	}
	return names
}

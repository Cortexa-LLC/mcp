package slack

import (
	"fmt"
)

// MultiClient manages multiple Slack workspace clients
type MultiClient struct {
	config  *Config
	clients map[string]*Client
}

// NewMultiClient creates a client manager for multiple workspaces
func NewMultiClient(config *Config) *MultiClient {
	clients := make(map[string]*Client)

	for name, wsCfg := range config.Workspaces {
		clients[name] = NewClient(
			wsCfg.AccessToken,
			wsCfg.RefreshToken,
			wsCfg.ClientID,
			wsCfg.ClientSecret,
		)
	}

	return &MultiClient{
		config:  config,
		clients: clients,
	}
}

// GetClient returns the client for a specific workspace
func (mc *MultiClient) GetClient(workspace string) (*Client, error) {
	// Use default if workspace is empty
	if workspace == "" {
		workspace = mc.config.DefaultWorkspace
	}

	client, ok := mc.clients[workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %s not found", workspace)
	}

	return client, nil
}

// ListWorkspaces returns all workspace names
func (mc *MultiClient) ListWorkspaces() []string {
	return mc.config.ListWorkspaces()
}

// GetDefaultWorkspace returns the default workspace name
func (mc *MultiClient) GetDefaultWorkspace() string {
	return mc.config.DefaultWorkspace
}

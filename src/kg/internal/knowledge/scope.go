package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ScopeConfig defines a knowledge graph scope - what gets indexed into a specific database.
// Scopes enable multi-layered KGs in monorepos (e.g., platform.db + selling.db).
type ScopeConfig struct {
	// Name of this scope (e.g., "platform", "selling")
	Name string `json:"name"`

	// Database filename relative to .ai/ directory (e.g., "platform.db", "selling.db")
	Database string `json:"database"`

	// Layers are other scopes to federate with (read-only). Queries merge results from all layers.
	// Example: ["platform"] means this scope builds on platform knowledge.
	Layers []string `json:"layers,omitempty"`

	// Include patterns (glob-style, relative to project root). Default: ["**/*"]
	Include []string `json:"include,omitempty"`

	// Exclude patterns (glob-style, relative to project root). Applied after Include.
	Exclude []string `json:"exclude,omitempty"`

	// IncludeModules lists specific modules/ subdirectories to include.
	// When set, "modules/**/*" is implicitly excluded, then these are re-included.
	// Example: ["SellingModule", "M2M"] includes only modules/SellingModule/** and modules/M2M/**
	IncludeModules []string `json:"includeModules,omitempty"`
}

// LoadScopeConfig reads a scope config from .ai/scope/<name>.json
func LoadScopeConfig(aiDir, scopeName string) (*ScopeConfig, error) {
	path := filepath.Join(aiDir, "scope", scopeName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scope config %s: %w", path, err)
	}

	var cfg ScopeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse scope config %s: %w", path, err)
	}

	// Validate required fields
	if cfg.Name == "" {
		cfg.Name = scopeName
	}
	if cfg.Database == "" {
		return nil, fmt.Errorf("scope config %s missing required field: database", path)
	}

	// Apply defaults
	if len(cfg.Include) == 0 {
		cfg.Include = []string{"**/*"}
	}

	return &cfg, nil
}

// ListScopeConfigs returns all scope configs found in .ai/scope/
// Returns empty slice (not error) if scope directory doesn't exist.
func ListScopeConfigs(aiDir string) ([]*ScopeConfig, error) {
	scopeDir := filepath.Join(aiDir, "scope")
	entries, err := os.ReadDir(scopeDir)
	if os.IsNotExist(err) {
		return nil, nil // No scopes defined - use legacy single-DB mode
	}
	if err != nil {
		return nil, fmt.Errorf("read scope directory: %w", err)
	}

	var configs []*ScopeConfig
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		scopeName := entry.Name()[:len(entry.Name())-5] // strip .json
		cfg, err := LoadScopeConfig(aiDir, scopeName)
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

// ShouldIncludePath checks if a path (relative to project root) should be indexed
// according to this scope config.
func (sc *ScopeConfig) ShouldIncludePath(relPath string) bool {
	// Check if path is in modules/ and handle IncludeModules logic
	if len(sc.IncludeModules) > 0 {
		if matched, module := matchesModulePath(relPath); matched {
			// Path is in modules/ - only include if module is in IncludeModules
			return contains(sc.IncludeModules, module)
		}
		// Path not in modules/ - fall through to normal Include/Exclude logic
	}

	// Apply exclude patterns first
	for _, pattern := range sc.Exclude {
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return false
		}
		// Also check glob-style ** patterns
		if matchGlob(pattern, relPath) {
			return false
		}
	}

	// Apply include patterns
	for _, pattern := range sc.Include {
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
		if matchGlob(pattern, relPath) {
			return true
		}
	}

	return false
}

// matchesModulePath checks if path is under modules/ and returns (true, moduleName) if so.
// Example: "modules/SellingModule/src/foo.swift" → (true, "SellingModule")
func matchesModulePath(relPath string) (bool, string) {
	// Normalize to forward slashes
	normalized := filepath.ToSlash(relPath)

	// Check if path starts with "modules/"
	if len(normalized) < 8 || normalized[:8] != "modules/" {
		return false, ""
	}

	// Extract the module name (first path segment after "modules/")
	remainder := normalized[8:]
	sepIdx := -1
	for i, c := range remainder {
		if c == '/' {
			sepIdx = i
			break
		}
	}

	if sepIdx > 0 {
		return true, remainder[:sepIdx]
	}

	// Path is exactly "modules/ModuleName" with no trailing slash
	if remainder != "" {
		return true, remainder
	}

	return false, ""
}

// matchGlob implements basic ** glob matching (recursive wildcard)
func matchGlob(pattern, path string) bool {
	// Normalize both to forward slashes for consistent matching
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// "**/*" matches everything
	if pattern == "**/*" {
		return true
	}

	// Handle "prefix/**/*" pattern (e.g., "modules/**/*")
	if len(pattern) > 5 && pattern[len(pattern)-4:] == "/**/*" {
		prefix := pattern[:len(pattern)-5]
		return path == prefix || len(path) > len(prefix) && path[:len(prefix)+1] == prefix+"/"
	}

	// Handle "**" at the start (e.g., "**/foo.txt")
	if len(pattern) > 3 && pattern[:3] == "**/" {
		suffix := pattern[3:]
		// Check if path ends with the suffix or has it as a path component
		if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
			return true
		}
		// Check each directory level
		for p := path; p != "." && p != "/"; p = filepath.Dir(p) {
			if matched, _ := filepath.Match(suffix, p); matched {
				return true
			}
		}
	}

	return false
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// GetDefaultScope reads the default scope from .ai/config.json
// Returns empty string if no default is set.
func GetDefaultScope(aiDir string) (string, error) {
	configPath := filepath.Join(aiDir, "config.json")
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}

	var config struct {
		DefaultScope string `json:"defaultScope"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	return config.DefaultScope, nil
}

// SetDefaultScope sets the default scope in .ai/config.json
func SetDefaultScope(aiDir, scopeName string) error {
	configPath := filepath.Join(aiDir, "config.json")

	// Read existing config or create new
	var config map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse existing config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	} else {
		config = make(map[string]interface{})
	}

	// Update default scope
	if scopeName == "" {
		delete(config, "defaultScope")
	} else {
		config["defaultScope"] = scopeName
	}

	// Write back
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

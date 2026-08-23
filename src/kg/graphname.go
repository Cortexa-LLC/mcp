package main

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// Hub graph naming.
//
// A hub's namespace is shared by every repo that pushes to it, so a graph name
// has to be unique across all of them. It was not: a scoped push used the
// scope's own name, so a `platform` scope in one repo and a `platform` scope in
// another landed on the same graph, last push winning silently.
//
// The rule:
//
//	<repo>            the repo's default scope, or a repo with no scopes
//	<repo>.<scope>    any other named scope
//
// The repo is the unit of identity because within a GitHub organisation repo
// names are unique by construction — the org cannot hold two repos called
// `platform`. Since a hub serves one organisation, an owner segment would be a
// constant, so it is left out; adding it later is a compatible extension if
// hubs ever federate across orgs.
//
// The default scope keeps the bare repo name so that a repo which is one
// project stays `depop` rather than `depop.default`, and so that adding a
// second scope later does not rename the graph the first one was pushing to.
//
// Separator is "." and not "/" because a graph name becomes a single
// filesystem path component on the hub, and graphNameRE rejects "/" precisely
// to keep it one. The design doc's `monorepo/platform` predates that hardening.
const graphNameSeparator = "."

// graphNameUnsafe matches anything the hub's graphNameRE would reject.
var graphNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// sanitizeGraphComponent makes an arbitrary string safe as part of a graph
// name. Remote URLs and directory names are not constrained to the hub's
// alphabet, and a name the hub rejects fails at push time with an error about
// a regex, which is a poor way to learn your repo has a space in its name.
func sanitizeGraphComponent(s string) string {
	s = graphNameUnsafe.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(s, "-.")
	if s == "." || s == ".." {
		return ""
	}
	return s
}

// repoIdentity returns the name identifying this repository on a hub.
//
// Taken from the git remote rather than the directory name: a checkout's
// directory is whatever the person who cloned it chose, which is not identity.
// Falls back to the project root's basename when there is no remote, which
// covers repos that have not been pushed anywhere yet.
func repoIdentity(root string) string {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil {
		if name := repoNameFromRemote(strings.TrimSpace(string(out))); name != "" {
			return name
		}
	}
	return sanitizeGraphComponent(filepath.Base(root))
}

// repoNameFromRemote extracts the repository name from a git remote URL,
// handling both SSH (git@host:owner/repo.git) and HTTPS forms.
func repoNameFromRemote(remote string) string {
	if remote == "" {
		return ""
	}
	remote = strings.TrimSuffix(remote, ".git")
	remote = strings.TrimRight(remote, "/")
	// Works for both forms: the repo is whatever follows the last separator.
	if i := strings.LastIndexAny(remote, "/:"); i >= 0 {
		remote = remote[i+1:]
	}
	return sanitizeGraphComponent(remote)
}

// defaultGraphName returns the hub graph name for a scope, following the rule
// documented at the top of this file. An explicit hubGraph in the scope config
// wins: --graph cannot express per-scope names during --all-scopes, so a
// monorepo pinning its published names needs somewhere to say so.
func defaultGraphName(root, aiDir, scopeName string) string {
	repo := repoIdentity(root)
	if repo == "" {
		repo = "graph"
	}
	if scopeName == "" {
		return repo
	}

	if cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName); err == nil && cfg.HubGraph != "" {
		return sanitizeGraphComponent(cfg.HubGraph)
	}

	if defaultScope, err := knowledge.GetDefaultScope(aiDir); err == nil && defaultScope == scopeName {
		return repo
	}

	scope := sanitizeGraphComponent(scopeName)
	if scope == "" {
		return repo
	}
	return repo + graphNameSeparator + scope
}

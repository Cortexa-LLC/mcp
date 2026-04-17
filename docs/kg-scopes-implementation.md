# Knowledge Graph Scopes — Implementation Summary

## Overview

Multi-scope knowledge graphs enable large monorepos to maintain separate but federated knowledge graphs for platform/shared code and team-specific domains. This prevents knowledge pollution while allowing teams to benefit from shared platform knowledge.

## Problem Statement

Large monorepos face several challenges with a single knowledge graph:

1. **Knowledge Pollution**: Platform code mixed with domain-specific code makes searches noisy
2. **Team Boundaries**: Different teams work on different vertical slices (features) but share horizontal slices (platform)
3. **Scale**: Indexing everything for every team is wasteful and slow
4. **Context**: Teams need platform knowledge but want their domain-specific knowledge prioritized

## Solution Architecture

### Component Overview

```
.ai/
  ├── platform.db              # Shared platform knowledge
  ├── team-a.db                # Team A domain knowledge
  ├── team-b.db                # Team B domain knowledge
  ├── config.json              # {"defaultScope": "team-a"}
  └── scope/
      ├── platform.json        # Platform scope config
      ├── team-a.json          # Team A scope config
      └── team-b.json          # Team B scope config
```

### Data Flow

```
User Command (kg search "auth")
  ↓
Determine Scope (default or --scope flag)
  ↓
Load Scope Config (.ai/scope/team-a.json)
  ↓
Check for Layers (layers: ["platform"])
  ↓
Open FederatedStore
  ├─ platform.db (read-only, priority: 1)
  └─ team-a.db   (primary, priority: 11)
  ↓
Execute Queries on All Layers
  ↓
Merge Results (higher priority wins)
  ↓
Return Unified Results
```

## Implementation Details

### 1. Scope Configuration (`scope.go`)

**Location**: `src/kg/internal/knowledge/scope.go`

```go
type ScopeConfig struct {
    Name           string   // Scope identifier
    Database       string   // Database filename (e.g., "platform.db")
    Layers         []string // Other scopes to federate with
    Include        []string // Glob patterns to include
    Exclude        []string // Glob patterns to exclude
    IncludeModules []string // Specific modules/ to include
}
```

**Key Functions**:
- `LoadScopeConfig(aiDir, scopeName)` - loads scope from `.ai/scope/<name>.json`
- `ListScopeConfigs(aiDir)` - returns all defined scopes
- `GetDefaultScope(aiDir)` - reads default from `.ai/config.json`
- `SetDefaultScope(aiDir, scopeName)` - sets default in config
- `ShouldIncludePath(relPath)` - checks if file matches scope filters

**Path Filtering Logic**:
1. If `IncludeModules` is set:
   - Paths in `modules/` must match a listed module
   - Paths outside `modules/` follow normal include/exclude rules
2. Apply exclude patterns first (short-circuit if matched)
3. Apply include patterns (default: `["**/*"]`)

### 2. Federated Store (`federated.go`)

**Location**: `src/kg/internal/knowledge/federated.go`

```go
type FederatedStore struct {
    layers []*layeredStore
}

type layeredStore struct {
    name     string
    store    *Store
    priority int
}
```

**Key Functions**:
- `OpenFederatedStore(aiDir, scopeConfig, readOnly)` - opens all layer stores
- `HybridSearch(...)` - queries all layers and merges results
- `PrimaryStore()` - returns writable store for mutations
- `Close()` - closes all layer stores

**Merge Strategy**:
- Collect results from all layers
- Deduplicate by entity ID
- Higher priority layer wins for conflicts
- Same priority: combine scores
- Sort by final score descending

### 3. Scope-Aware Indexing (`index.go`)

**Command**:
```bash
kg index                  # Index default scope (or all if no default)
kg index --scope team-a   # Index specific scope
kg index --all            # Index all scopes
```

**Flow**:
1. Determine which scopes to index based on flags and default
2. For each scope:
   - Load scope config
   - Open store for that scope's database
   - Create indexer with scope filter
   - Walk files and index only those matching scope rules

**Indexer Integration**:
```go
indexer.SetScopeFilter(scopeConfig)
// In file walker:
if scopeFilter != nil && !scopeFilter.ShouldIncludePath(relPath) {
    return nil  // Skip file
}
```

### 4. Federated Search (`search.go`)

**Command**:
```bash
kg search "query"              # Search default scope + layers
kg search "query" --scope team-a  # Search specific scope + layers
kg search "query" --all        # Search all scopes independently
```

**Logic**:
- `--all`: Opens each scope independently, merges by entity ID (no priority)
- Single scope with layers: Uses `FederatedStore` for priority-based merging
- Single scope without layers: Uses regular `Store`
- Legacy mode (no scopes): Uses `knowledge.db`

### 5. MCP Server Integration (`mcp_server.go`, `handle_server.go`)

**Server Startup**:
1. Determine default scope from `.ai/config.json`
2. Load scope config if default is set
3. Pass scope config to `RunMCPServer()`

**Query Handling**:
```go
withSearch := func(fn func(KeywordSearcher) (any, error)) {
    if useFederation {
        fs := OpenFederatedStore(aiDir, scopeConfig, true)
        return fn(fs)
    }
    s := OpenStoreReadOnly(dbPath)
    return fn(s)
}
```

**Read vs Write**:
- Read operations: Use `FederatedStore` when scope has layers
- Write operations: Always use primary scope's `Store` directly

### 6. Config Management (`config.go`)

**Commands**:
```bash
kg config list-scopes            # List all scopes with default marker
kg config set-default-scope team-a  # Set default
kg config set-default-scope ""   # Clear default
```

## Configuration Examples

### Platform Scope
`.ai/scope/platform.json`:
```json
{
  "name": "platform",
  "database": "platform.db",
  "include": ["**/*"],
  "exclude": ["modules/**/*"],
  "includeModules": [
    "CoreUI",
    "Networking",
    "Authentication",
    "Telemetry"
  ]
}
```

### Team Scope
`.ai/scope/team-a.json`:
```json
{
  "name": "team-a",
  "database": "team-a.db",
  "layers": ["platform"],
  "includeModules": [
    "FeatureA",
    "FeatureASupport"
  ]
}
```

### Default Scope
`.ai/config.json`:
```json
{
  "defaultScope": "team-a"
}
```

## Workflows

### Platform Team Workflow
```bash
# Initial setup
cd monorepo
mkdir -p .ai/scope
# Create platform.json (see above)

# Index platform
kg index --scope platform

# Platform team's default
kg config set-default-scope platform

# Work as normal
kg search "caching"
kg add observation <id> "[DECISION] ..."
```

### Domain Team Workflow
```bash
# Initial setup
# Create team-a.json with layers: ["platform"]

# Index team scope
kg index --scope team-a

# Set as default for this team
kg config set-default-scope team-a

# Work - automatically includes platform knowledge
kg search "authentication"  # Searches team-a.db + platform.db
kg add observation <id> "[INVESTIGATION] ..."  # Goes to team-a.db
```

## Backward Compatibility

**No Scopes Defined**:
- If `.ai/scope/` doesn't exist → legacy mode
- Uses `.ai/knowledge.db`
- All commands work as before
- Zero migration required

**Migration Path**:
1. Keep using `knowledge.db` (no changes needed)
2. When ready: create `.ai/scope/` with scope configs
3. Run `kg index --all` to populate new databases
4. Set default scope
5. Optionally: delete or keep `knowledge.db` as backup

## Testing Strategy

**Unit Tests**:
- Scope configuration parsing
- Path filtering logic (modules matching, glob patterns)
- Federated merge logic (priority handling, deduplication)

**Integration Tests**:
- Multi-scope indexing
- Federated search across layers
- MCP server with scopes
- Default scope handling

**Real-World Test** (Task #2):
- Set up scopes in `ios_core` monorepo
- Index platform and Selling scopes
- Validate search quality and performance
- Confirm MCP integration

## Performance Considerations

**Indexing**:
- Scoped indexing is faster (fewer files to scan)
- Can index scopes in parallel for initial setup
- Re-indexing only touches relevant scope

**Search**:
- Federation adds latency (N queries instead of 1)
- Mitigation: Layers are read-only (concurrent access)
- Typical case: 2 layers (platform + domain) ≈ 2x query time
- Network is not involved (all local SQLite)

**Storage**:
- Each scope has its own database file
- Duplicate entities across scopes (entity in both platform and team)
- Trade-off: Some duplication for cleaner separation

## Future Enhancements

1. **Layer Caching**: Cache platform search results for repeated queries
2. **Scope Templates**: Predefined scope configs for common patterns
3. **Scope Inheritance**: More complex layer hierarchies
4. **Cross-Scope Relations**: Links between entities in different scopes
5. **Scope Metrics**: Track which scopes are most queried/useful
6. **Automatic Scope Detection**: Suggest scope boundaries based on code organization

## Files Modified/Added

**New Files**:
- `src/kg/internal/knowledge/scope.go` - scope configuration and filtering
- `src/kg/internal/knowledge/federated.go` - federated store implementation
- `src/kg/config.go` - scope config commands
- `docs/kg-scopes.md` - user-facing scope documentation
- `docs/kg-scopes-implementation.md` - this document

**Modified Files**:
- `src/kg/helpers.go` - added `openStoreModeWithScope()`
- `src/kg/index.go` - scope-aware indexing with --scope/--all flags
- `src/kg/search.go` - federated search support
- `src/kg/handle_server.go` - scope detection for MCP server
- `src/kg/internal/knowledge/mcp_server.go` - federated search in MCP tools
- `src/kg/internal/knowledge/indexer.go` - added `scopeFilter` field and filtering
- `src/kg/README.md` - architecture and CLI docs updated
- `docs/kg-claude-integration.md` - multi-scope section added
- `README.md` - mention multi-scope support

## Commits

1. `9e3eb0f` - feat: add multi-scope knowledge graphs for monorepos
2. `2dbce3a` - feat: implement federated queries across layered scopes
3. `1ef727a` - feat: add scope support to MCP server and update documentation

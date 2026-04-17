# Knowledge Graph Scopes

Scopes enable multi-layered knowledge graphs in monorepos. This allows you to maintain separate KGs for shared/platform code and domain-specific areas, while federating queries across layers.

## Use Cases

### Monorepo with Platform + Domain Teams

For a large monorepo with shared platform modules and team-specific domain modules:

```
monorepo/
  .ai/
    platform.db          # Platform knowledge (shared)
    team-a.db            # Team A domain knowledge
    config.json          # Optional: {"defaultScope": "team-a"}
    scope/
      platform.json      # Platform scope config
      team-a.json        # Team A scope config
      team-b.json        # Team B scope config
  modules/
    CoreUI/              # Platform module
    Networking/          # Platform module
    FeatureA/            # Team A domain module
    FeatureB/            # Team B domain module
    ...
```

## Configuration

### Scope Definition (`.ai/scope/<name>.json`)

Each scope defines:
- Which database to use
- Which files/modules to index
- Which other scopes to layer on top of

**Platform scope** (`.ai/scope/platform.json`):
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
    "Telemetry",
    "FeatureFlags",
    "SharedComponents"
  ]
}
```

**Team A scope** (`.ai/scope/team-a.json`):
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

### Scope Fields

- **name** (required): Scope identifier
- **database** (required): Database filename (relative to `.ai/`)
- **layers** (optional): Array of scope names to federate with (read-only)
- **include** (optional): Glob patterns to include (default: `["**/*"]`)
- **exclude** (optional): Glob patterns to exclude
- **includeModules** (optional): When set, only these `modules/<name>/` directories are indexed. Implicitly excludes all other modules.

### Default Scope

Set a default scope so `kg index` and `kg search` use it without needing `--scope`:

```bash
kg config set-default-scope team-a
```

List all scopes:

```bash
kg config list-scopes
```

## Commands

### Indexing

```bash
# Index default scope (or all if no default set)
kg index

# Index specific scope
kg index --scope team-a

# Index all scopes
kg index --all
```

### Searching

```bash
# Search in default scope (+ its layers)
kg search "authentication"

# Search specific scope (+ its layers if defined)
kg search "authentication" --scope team-a

# Search across all scopes (independent queries, no layer federation)
kg search "authentication" --all
```

**Note:** When searching a scope with layers, results are automatically federated:
- Queries run against the scope's database AND all layer databases
- Results are merged with priority (higher layers override lower)
- Duplicate entities (same ID) are deduplicated, keeping the highest-priority version

## Backward Compatibility

If no scope configs exist in `.ai/scope/`, kg operates in legacy single-DB mode using `.ai/knowledge.db` for everything. This ensures existing projects continue to work without changes.

## How Layers Work

When a scope defines `layers`, queries will:
1. Search the scope's own database
2. Search each layer database (in order)
3. Merge results with priority (later layers override earlier for conflicts)

For example, `team-a` scope with `layers: ["platform"]` means:
- `kg search` from Team A's context searches both `team-a.db` and `platform.db`
- Team-specific entities/observations override platform ones if there's overlap
- Platform knowledge is read-only (indexed separately via `kg index --scope platform`)

## Example Workflow

```bash
# Initial setup for monorepo
cd ~/Projects/monorepo

# Create scope directory
mkdir -p .ai/scope

# Create platform scope config
cat > .ai/scope/platform.json << 'EOF'
{
  "name": "platform",
  "database": "platform.db",
  "include": ["**/*"],
  "exclude": ["modules/**/*"],
  "includeModules": ["CoreUI", "Networking", "Authentication"]
}
EOF

# Create team scope config
cat > .ai/scope/team-a.json << 'EOF'
{
  "name": "team-a",
  "database": "team-a.db",
  "layers": ["platform"],
  "includeModules": ["FeatureA", "FeatureASupport"]
}
EOF

# Set default scope for team's work
kg config set-default-scope team-a

# Index platform once (maintained by platform team)
kg index --scope platform

# Index team domain (run regularly by team)
kg index --scope team-a

# Search queries now automatically include platform knowledge
kg search "networking layer"
```

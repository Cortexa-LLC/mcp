# kg CLI Reference

`kg` is both an MCP server and a standalone CLI for managing a project knowledge
graph. Run it from the project root — it auto-discovers the database location by
walking up the directory tree to find a `.ai/` directory, a git root, or common
project markers (`go.mod`, `package.json`, `Cargo.toml`, etc.).

**Database location:** `.ai/knowledge.db` relative to the detected project root, or
`.ai/<scope>.db` when the project defines [scopes](kg-scopes.md). The user-global
[personal store](kg-personal-store.md) lives at `$KG_HOME`, default `~/.kg/knowledge.db`,
and is reached with `--personal`.

---

## First-Time Setup: `kg index`

`kg index` is the starting point for any new project. It scans the codebase and
populates the knowledge graph with structural entities (files, functions, types,
packages) and their relationships (imports, contains). Run it once to bootstrap,
then again after large structural changes.

```bash
kg index
```

Example output:

```
🔍 Indexing codebase at /path/to/your-project...
✅ Indexing complete!
   Files scanned:     191
   Entities created:  1113
   Relations created: 1517
   Duration:          5.2s
```

**What gets indexed:**

| Language / Format | Extensions |
|-------------------|-----------|
| Go | `.go` |
| Python | `.py` |
| TypeScript / TSX | `.ts`, `.tsx` |
| JavaScript / JSX | `.js`, `.jsx` |
| Rust | `.rs` |
| Java | `.java` |
| Kotlin | `.kt`, `.kts` |
| Scala | `.scala`, `.sc` |
| C / C++ | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` |
| C# | `.cs` |
| Swift | `.swift` |
| Ruby | `.rb` |
| Bash | `.sh`, `.bash` |
| Groovy | `.groovy` |
| CSS | `.css` |
| HTML | `.html`, `.htm` |
| YAML | `.yaml`, `.yml` |
| Markdown | `.md` |
| GraphQL | `.graphql`, `.graphqls`, `.gql` |
| JSON Schema | `.schema.json`, `.json` |
| PDF | `.pdf` |
| Assembly | `.s` |
| Makefile | `Makefile`, `*.mk`, `*.make`, `CMakeLists.txt` |

**What is skipped:**

- Paths matching `.gitignore` patterns
- Paths matching `.claudeignore` patterns (if present)
- Always-skipped directories: `.git`, `node_modules`, `vendor`, `.claude`, `.beads`,
  `dist`, `build`, `.build`, `__pycache__`, `.mypy_cache`, `.pytest_cache`, `.next`,
  `.nuxt`, `target` (Rust/Maven), `coverage`
- Binary files

**When to re-run:**
- After adding new packages or significantly restructuring the codebase
- After pulling large upstream changes
- The MCP tools (`kg__index_project`) can also trigger this from within a Claude session

---

## All Commands

### `kg index`

Scan the project and populate the knowledge graph.

```bash
kg index                     # default scope, or .ai/knowledge.db if no scopes exist
kg index --scope team-a      # one scope
kg index --all               # every defined scope, in turn
```

---

### `kg search <query>`

Keyword search across all entities and observations. Hybrid search — combines
exact keyword matching with vector similarity if embeddings are configured.

```bash
kg search "auth middleware"
kg search "token expiry"
kg search "database connection pool"
```

With scopes, `kg search` federates across the active scope **and its layers**, merging by
priority:

```bash
kg search "auth middleware"                # default scope + its layers
kg search "auth middleware" --scope team-a  # team-a + its layers
kg search "auth middleware" --all           # every scope independently, no layer merging
```

The personal store is searchable the same way — `--personal` instead of the project, or
`--with-personal` alongside it (the two are mutually exclusive):

```bash
kg search "retention" --personal              # personal store only
kg search "retention" --with-personal         # project graph + personal, personal ranked lower
```

---

### `kg stats`

Show a count summary of entities, relations, and observations in the graph.

```bash
kg stats
kg stats --scope platform    # counts for one scope's database only (never federated)
kg stats --personal            # size of the personal store
```

---

### `kg meta`

Show the provenance stamp `kg index` records into the database: which repo and
commit it was built from, when, and a staleness check against the local HEAD.

```bash
kg meta
kg meta --scope platform     # stamp for one scope's database
```

```
Project:     my-repo
Database:    /path/to/my-repo/.ai/knowledge.db
Repo:        git@github.com:org/my-repo.git
Commit:      3f2a91c04b7d8e6f15c2a90d4e7b6a3c8d1f0e25
Indexed at:  2026-08-20 09:14:03 PDT
Embed model: (not set)
kg version:  dev
Staleness:   local HEAD is 2 commit(s) ahead of the indexed commit
```

`(dirty)` after the commit means the working tree had uncommitted changes at
index time. Databases indexed before this stamp existed print a hint to re-run
`kg index`.

---

### `kg show <entity-id>`

Show a single entity with its relations and observations.

```bash
kg show "function:parseToken:internal/auth/token.go"
kg show "function:parseToken:internal/auth/token.go" --scope platform
```

`kg show` reads a single database — pass `--scope` to look inside a layer, or `--personal`
for an entity in the personal store.

---

### `kg add entity`

Manually add an entity to the graph. Useful for concepts, topics, or decisions
that don't exist as named code symbols.

```bash
kg add entity --name "auth-session-design" --type "topic"
kg add entity --name "parseToken" --type "function" --summary "Validates JWT and returns claims"
```

**Entity types:** `function`, `type`, `file`, `module`, `package`, `topic`, `import`, `concept`

---

### `kg add observation <entity-id> <content>`

Attach a note to an existing entity. Observations are the primary way to record
findings, decisions, and caveats that go beyond what the code itself says.

```bash
kg add observation "topic:auth-session-design" \
  "[DECISION] Using JWT over session cookies — mobile clients cannot share cookies across subdomains."

kg add observation "function:parseToken:internal/auth/token.go" \
  "[CAVEAT] Does not validate the 'aud' claim — any valid JWT will pass."
```

**Recommended prefixes:**

| Prefix | Use for |
|--------|---------|
| `[INVESTIGATION]` | Findings from debugging or exploration |
| `[DECISION]` | Architectural or design choices and rationale |
| `[CAVEAT]` | Known limitations, edge cases, gotchas |
| `[PERFORMANCE]` | Measured characteristics or bottlenecks |

---

### `kg link <from-id> --rel <RELATION> <to-id>`

Create a directed relationship between two entities.

```bash
kg link "file:cmd/server/main.go" --rel IMPORTS "package:internal/auth"
kg link "function:handleRequest" --rel CALLS "function:validateToken"
```

**Relation types:** `CONTAINS`, `IMPORTS`, `CALLS`, `IMPLEMENTS`, `BELONGS_TO`,
`DEPENDS_ON`, `RELATES_TO`

---

### `kg config list-scopes` / `kg config set-default-scope <name>`

List the scopes defined in `.ai/scope/`, or set the default scope in `.ai/config.json` so
that `kg index`, `kg search`, `kg stats`, and the MCP server use it without `--scope`.

```bash
kg config list-scopes
kg config set-default-scope team-a
```

See [kg-scopes.md](kg-scopes.md) for scope configuration and federation behaviour.

---

### `kg perf`

A/B report comparing task executions with and without KG preflight context. Reads
`.ai/tasks/*/metrics.json` (ai-pack projects) and prints an aggregate table.

```bash
kg perf
kg perf --json     # machine-readable output
```

---

### `kg personal init` / `path` / `review` / `forget`

Create the personal knowledge store, or print where it is. `init` is safe to
re-run, and the first `--personal` write creates the store anyway.

```bash
kg personal init
kg personal path
```

`--personal` then targets it from any directory:

```bash
kg add entity --personal --name "kafka-retention" --type decision --summary "[DECISION] …"
kg add observation --personal "<entity-id>" "[CAVEAT] compacted topics may differ"
kg link --personal "<from-id>" --rel RELATES_TO "<to-id>"
kg search "retention" --personal
kg show "<entity-id>" --personal
kg stats --personal
```

Review what is in there, and remove anything unwanted:

```bash
kg personal review                  # recent entries, newest first, with who recorded each
kg personal review --agent-only     # only entries an agent recorded via MCP
kg personal review --limit 50
kg personal forget "<entity-id>"    # delete an entry and its observations
```

Location is `$KG_HOME` if set, otherwise `~/.kg/knowledge.db`. Never run `kg index` against
it — it holds hand-written knowledge, not indexed source. Full guide:
[kg-personal-store.md](kg-personal-store.md).

---

### `kg push`

Push already-indexed scope databases to a shared knowledge hub (see
`kg hub serve`). Each graph carries its provenance stamp — git commit, repo
URL, dirty flag — and its scope's layer topology, so the hub knows exactly
which commit each graph reflects. Requires `KG_HUB_SEED_TOKEN` in the
environment. Full guide: [kg-shared-service.md](kg-shared-service.md).

```bash
KG_HUB_SEED_TOKEN=... kg push --hub http://hub.internal:7411   # default scope
kg push --all-scopes                    # every scope; hub from .ai/config.json
kg push --scope platform                # one scope
kg push --graph monorepo-platform       # rename the graph on the hub (single db only)
kg push --commit <sha>                  # override the recorded commit
kg push --allow-dirty                   # push despite a dirty-tree stamp
```

The hub URL comes from `--hub` or the `"hub"` key in `.ai/config.json`.
Databases indexed before provenance stamps existed fall back to the current
git HEAD with a warning — re-run `kg index` to stamp them. A database stamped
`(dirty)` is refused unless `--allow-dirty` is passed.

```
⬆️  platform @ 3f2a91c04b7d → http://hub.internal:7411
⬆️  team-a @ 3f2a91c04b7d → http://hub.internal:7411
✅ Pushed 2 graph(s)
```

---

### `kg hub serve`

Run a shared knowledge hub: an HTTP service hosting read-only knowledge
graphs seeded with `kg push`. Teams push their scope databases; anyone with
read access can search them without cloning or indexing the source.

```bash
KG_HUB_SEED_TOKEN=... kg hub serve                     # listen on :7411, data in ~/.kg-hub
kg hub serve --listen 127.0.0.1:8080 --data /srv/kg    # explicit address and storage
```

| Flag / Variable | Effect |
|-----------------|--------|
| `--listen` | Listen address (default `:7411`) |
| `--data` | Data directory (default `$KG_HUB_HOME`, else `~/.kg-hub`) |
| `KG_HUB_READ_TOKEN` | Bearer token required for reads; unset = open reads |
| `KG_HUB_SEED_TOKEN` | Bearer token required for `kg push`; unset = seeding disabled |

HTTP API (reads take `Authorization: Bearer <read-token>` when configured):

| Route | Purpose |
|-------|---------|
| `GET /healthz` | Liveness check, no auth |
| `GET /v1/graphs` | List hosted graphs with provenance |
| `GET /v1/graphs/{name}` | One graph's provenance |
| `POST /v1/graphs/{name}/search` | `{"query": "...", "limit": 20}` → ranked results; `"include_layers": true` also searches the graph's hub-side layers |
| `POST /v1/search` | Same body plus optional `"graphs": [...]` — search many graphs |
| `PUT /v1/graphs/{name}` | Seed a graph (used by `kg push`) |

Each push is stored under `graphs/<name>/<commit>/` and swapped in atomically;
the previous commit is kept for rollback and older ones are pruned. A
`src/kg/Dockerfile` is provided for container deployment (build from the
repo's `src/` directory: `docker build -f kg/Dockerfile .`).

---

### `kg hub list`

List the graphs hosted on a hub, with the commit each was indexed from.

```bash
kg hub list --hub http://hub.internal:7411
kg hub list                              # hub from .ai/config.json
```

```
NAME      COMMIT                INDEXED           LAYERS    PROJECT
platform  3f2a91c04b7d          2026-08-20 09:14            monorepo
team-a    9c81d2e4f0aa (dirty)  2026-08-20 10:02  platform  monorepo
```

Uses `KG_HUB_READ_TOKEN` from the environment when the hub requires read auth.

---

### `kg hub status`

Compare the hub's copy of every graph related to this project — the active scope's
`remotes` plus any local scope names the hub also hosts — against local git history.

```bash
kg hub status
kg hub status --hub http://hub.internal:7411 --scope team-a
```

```
NAME      COMMIT        INDEXED           STATUS
platform  3f2a91c04b7d  2026-08-20 09:14  hub is 4 commit(s) behind local HEAD
team-a    9c81d2e4f0aa  2026-08-20 10:02  up to date with local HEAD
```

`not in local history (different repo?)` is normal for a remote graph pushed from
another repository. Consuming hub graphs via `remotes` in a scope config is covered in
[kg-shared-service.md](kg-shared-service.md).

---

### Not yet implemented

These commands are registered but are placeholders — they print
`Not yet implemented` and exit successfully. Do not script against them.

| Command | Intent |
|---------|--------|
| `kg export` | Export the graph to GraphML/JSON for external tools |
| `kg graph` | Write GraphML to stdout |
| `kg gc` | Remove orphaned nodes, observations, and relations |
| `kg embed` | Generate and attach vector embeddings as a batch job |

Embeddings are still populated during indexing when `OPENAI_API_KEY` or `OLLAMA_HOST` is
set — only the standalone batch command is missing.

---

### `kg server --stdio`

Start the MCP server over stdio. This is how MCP clients (Claude Code, Claude
Desktop) communicate with `kg`. You normally do not run this manually —
the MCP client configuration handles it.

```bash
kg server --stdio
```

The server resolves the default scope at startup, so `search_knowledge` and
`get_preflight_context` federate over that scope's layers. `query_graph`,
`get_file_context`, and the write tools use the scope's own database only.

If a [personal store](kg-personal-store.md) exists, the server also offers
`search_personal_knowledge`. Recording personal knowledge is off unless enabled:

```bash
kg server --stdio --personal-writes    # or KG_PERSONAL_WRITES=1
```

Without it `add_personal_knowledge` is not registered at all, so an agent cannot write to the
personal store however it is asked. With it, writes require the user's quoted request, carry
permanent `[VIA:mcp]` provenance, are capped at 8 KB, and are reversible with
`kg personal forget`.

---

### `kg version`

Print version, commit, and build info.

```bash
kg version
```

---

## Typical Workflow

```bash
# 1. New project — index once to bootstrap the graph
cd your-project
kg index

# 2. Orient yourself
kg search "entry point"
kg search "database layer"
kg stats

# 3. Record a finding during investigation
kg add observation "function:processPayment" \
  "[INVESTIGATION] Idempotency key checked AFTER the charge is created — window for duplicate charges on retry."

# 4. Record an architectural decision
kg add entity --name "payment-idempotency" --type "topic"
kg add observation "topic:payment-idempotency" \
  "[DECISION] Moving idempotency check to before the Stripe call. See issue #847."

# 5. After a big refactor, re-index
kg index
```

---

## Scopes and Federated Search

Monorepos can split the graph into scopes — one database per team or subsystem — where a
scope may `layer` others. `kg search` then queries the scope's database **and** every
layer, merging results by priority so the scope's own knowledge wins on conflicts.

```bash
kg config list-scopes                 # what's defined
kg config set-default-scope team-a    # avoid passing --scope everywhere
kg index --scope platform            # index a shared layer once
kg index --scope team-a              # index the domain regularly
kg search "networking"                # searches team-a.db + platform.db
```

Only search federates — Cypher, `get_file_context`, and every write path use a single
database. Full configuration reference: [kg-scopes.md](kg-scopes.md); internals:
[kg-scopes-implementation.md](kg-scopes-implementation.md).

---

## Cypher Queries (via MCP)

The `kg__query_graph` MCP tool accepts raw Cypher for precise graph traversal. It runs
against the active scope's database only — no layer federation.
Common patterns:

```cypher
-- Everything in a package
MATCH (p:package {name: "internal/auth"})-[:CONTAINS]->(e) RETURN e.name, e.type

-- All callers of a function
MATCH (caller)-[:CALLS]->(f:function {name: "validateToken"}) RETURN caller.name

-- All imports of a file
MATCH (f:file {name: "cmd/server/main.go"})-[:IMPORTS]->(dep) RETURN dep.name

-- Recent observations containing a keyword
MATCH (e)-[:HAS_OBSERVATION]->(o:observation)
WHERE o.content CONTAINS "CAVEAT"
RETURN e.name, o.content LIMIT 20

-- Entities with no inbound relations (potential orphans)
MATCH (e) WHERE NOT ()-[]->(e) RETURN e.name, e.type LIMIT 20
```

---

## Environment Variables

| Variable | Effect |
|----------|--------|
| `OPENAI_API_KEY` | Enables OpenAI embeddings for semantic (vector) search |
| `OLLAMA_HOST` | Enables Ollama embeddings (default: `http://localhost:11434`) |
| `KG_HOME` | Directory holding the personal knowledge store (default: `~/.kg`) |
| `KG_HUB_HOME` | Data directory for `kg hub serve` (default: `~/.kg-hub`) |
| `KG_HUB_READ_TOKEN` | Read token: required by `kg hub serve` clients, sent by `kg hub list` |
| `KG_HUB_SEED_TOKEN` | Seed token: enables seeding on `kg hub serve`, required by `kg push` |

Embeddings are optional. Without them, `kg search` uses keyword matching only.

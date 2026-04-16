# kg CLI Reference

`kg` is both an MCP server and a standalone CLI for managing a project knowledge
graph. Run it from the project root — it auto-discovers the database location by
walking up the directory tree to find a `.ai/` directory, a git root, or common
project markers (`go.mod`, `package.json`, `Cargo.toml`, etc.).

**Database location:** `.ai/knowledge.db` relative to the detected project root.

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
kg index
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

---

### `kg stats`

Show a count summary of entities, relations, and observations in the graph.

```bash
kg stats
```

---

### `kg show <entity-id>`

Show a single entity with its relations and observations.

```bash
kg show "function:parseToken:internal/auth/token.go"
```

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

### `kg export`

Export the knowledge graph to GraphML or JSON for use in external tools.

```bash
kg export
```

---

### `kg graph`

Output the current graph in GraphML format (stdout).

```bash
kg graph
kg graph > knowledge-graph.graphml
```

---

### `kg gc`

Remove orphaned or unreferenced nodes, observations, and relations. Run
occasionally to keep the graph clean after large refactors.

```bash
kg gc
```

---

### `kg server --stdio`

Start the MCP server over stdio. This is how MCP clients (Claude Code, Claude
Desktop) communicate with `kg`. You normally do not run this manually —
the MCP client configuration handles it.

```bash
kg server --stdio
```

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

## Cypher Queries (via MCP)

The `kg__query_graph` MCP tool accepts raw Cypher for precise graph traversal.
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

Embeddings are optional. Without them, `kg search` uses keyword matching only.

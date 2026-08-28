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

Unexported symbols are indexed and searchable. They rank below equally-relevant
exported ones and are marked `(unexported)` in the output. `--public-only` hides
them, which is what you want when you are reading a package's API surface rather
than looking for a specific function:

```bash
kg search "handler"                # everything, exported first
kg search "handler" --public-only  # only the exported API surface
```

See [Symbol visibility](#symbol-visibility) for why unexported symbols are
indexed at all.

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

### `kg graph`

Render part of the graph as a diagram — mermaid by default, Graphviz DOT, or JSON.

```bash
kg graph --root parseToken                       # neighbourhood, 2 hops, mermaid
kg graph --root parseToken --depth 3             # wider
kg graph --root Store --direction in             # what depends on Store
kg graph --root main.go --rel CALLS,CONTAINS     # only those relations
kg graph --type file,package --limit 60          # whole graph, structure only
kg graph --format dot | dot -Tsvg -o graph.svg   # render an image
kg graph --scope platform -o platform.mmd        # one scope, to a file
kg graph --personal --format json                # personal store, machine-readable
```

**Start from a `--root`.** Without one the whole graph is rendered, and for a real
project that is a hairball no renderer can help with — a few hundred entities is
already too many to read. The unit that works is one entity and a hop or two
around it, which is what `--root` plus `--depth` draws. Find a root with
`kg search`.

`--root` accepts an entity ID or a name. A name matching more than one entity is
an error listing the candidates, because on a Go project `--root main` means both
the function and the package and guessing would be worse than asking:

```
$ kg graph --root main
Error: "main" matches 2 entities — use an ID instead:
  main (function) function:main.go:main
  main (package) package:main
```

| Flag | Default | Effect |
|------|---------|--------|
| `--root`, `-r` | *(whole graph)* | Entity ID or name to centre on |
| `--depth`, `-d` | `2` | Hops to follow from `--root` |
| `--direction` | `both` | `out` = what this depends on, `in` = what depends on this |
| `--format`, `-f` | `mermaid` | `mermaid`, `dot`, or `json` |
| `--type` | *(all)* | Only these entity types; `--root` is always kept |
| `--rel` | *(all)* | Only follow these relation types |
| `--limit` | `200` | Maximum nodes; `0` for no limit |
| `--output`, `-o` | *(stdout)* | Write to a file |
| `--federated` | off | Render the scope together with every layer it federates with |
| `--layer` | *(all)* | With `--federated`, load only these scopes |
| `--join-max-layers` | `3` | With `--federated`, the boilerplate guard (see below) |

`--rel` constrains what the walk follows, not just what is drawn, so filtering to
`CONTAINS` gives the containment tree rather than the same neighbourhood with
arrows missing. `--type` works the other way: it excludes nodes, and relations
between two surviving nodes are still drawn — including links between neighbours
the walk reached separately, which is what makes the picture a graph and not a
tree. The walk still travels *through* an excluded node, so a match sitting
behind one is reached: with `--type import`, a file whose function imports `fmt`
still shows `fmt` at depth 2, even though the function in between is filtered
out of the drawing.

Output is deterministic: the same graph renders byte-identically every run, so a
diagram can be committed and diffed. When `--limit` cuts the walk short, the
render says so in its header comment and on stderr — a truncated picture that
looked complete would be worse than no picture.

Entity types are drawn as distinguishable mermaid shapes: rectangle for files,
stadium for functions, hexagon for types, subroutine for packages, parallelogram
for imports, rounded for topics. The `--root` node is outlined thicker.

#### Federated rendering: `kg graph --federated`

Ordinarily `kg graph` reads one database. `--federated` reads the scope and every
layer it federates with, and merges them into a single graph:

```bash
kg graph --federated --root AddressClient --depth 1     # across every layer
kg graph --federated --layer payments,libraries --root Charge
kg graph --federated --join-max-layers 6 --root Deployment
```

Nodes are grouped into a mermaid `subgraph` per layer, so it stays legible which
database each part of the picture came from.

**Why this needs its own code path.** kg's federation is search-only by design —
`SearchLayer` in `kglib/federated.go` merges query *results* and nothing
enumerates entities across databases. Relations are also stored per-database, so
a plain union of the layers would be a set of disconnected components. What
connects them is joining identities across layers.

**The join rule.** Two rows in two databases are the same node when their
`(name, type)` match. That surfaces genuinely shared symbols, and equally
surfaces the same name implemented separately in several services — often the
more interesting answer:

```
$ kg graph --federated --layer clients,libraries,mobileapi,payments \
    --root AddressClient --depth 1
Federated 4 layer(s): 355429 node(s), 678308 relation(s).
Joined 5016 identities across layers, merging 36967 duplicate row(s).
```

…which draws six separate `AddressClient` definitions across three layers,
converging on one node.

**The boilerplate guard.** An unguarded join is worse than no join. On a real
59-layer estate the most widely shared names are markdown headings from a
documentation template (`Service Overview`, `Deployment`, `Known Issues and
Failure Modes`) and Helm values keys indexed as types (`accountName`,
`environment`, `chart`) — several of them present in all 60 layers. Joining on
those fuses every unrelated service into one hub and the picture stops meaning
anything.

So a `(name, type)` found in more than `--join-max-layers` layers (default 3) is
read as boilerplate and left unjoined. The command says what it suppressed, so
the threshold is inspectable rather than magic:

```
Left 5112 name(s) unjoined: they appear in more than 3 layers, which reads as
boilerplate rather than one shared symbol. Raise --join-max-layers to join them
anyway. Worst:
  accountName (type) in 60 layers
  environment (type) in 60 layers
  jobs (type) in 60 layers
```

Two other things get reported to stderr because a merged graph would otherwise
hide them: a layer that could not be opened (a warning, not a fatal error — a
render missing one database is still useful, a silent hole is not), and nodes
renamed because two layers minted the same ID for different things. Indexer IDs
are repo-relative paths, so `import:..` legitimately means something different
in every layer.

**Cost.** Databases are read one at a time and released, so peak memory is the
merged graph plus the largest single layer rather than all of them at once.
Measured on a 61-layer estate — 668k entities, 1.2M relations — a full load is
about 6 seconds and 1.2 GB resident. Use `--layer` to narrow it when you know
which layers you care about.

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

The hub URL comes from `--hub`, `KG_HUB_URL`, or `kg config set-hub` — all
user-controlled. It is deliberately **not** read from the repository: federated
search sends `KG_HUB_READ_TOKEN` to the hub as a bearer token, so whoever picks
the URL picks where that credential goes, and a cloned repo must not get to pick.
A project still chooses which *graphs* to federate via a scope's `remotes`.
Databases indexed before provenance stamps existed fall back to the current
git HEAD with a warning — re-run `kg index` to stamp them. A database stamped
`(dirty)` is refused unless `--allow-dirty` is passed.

```
⬆️  platform @ 3f2a91c04b7d → http://hub.internal:7411
⬆️  team-a @ 3f2a91c04b7d → http://hub.internal:7411
✅ Pushed 2 graph(s)
```

---

### Graph naming on a hub

A hub's namespace is shared by every repository that pushes to it, so `kg push`
derives a name that is unique across them:

| Situation | Graph name |
|-----------|------------|
| Repo with no scopes, or its default scope | `<repo>` |
| Any other named scope | `<repo>.<scope>` |

`<repo>` comes from the git remote's repository name — not the checkout
directory, which is whatever the person who cloned it chose. Within a GitHub
organisation repo names are unique by construction, and a hub serves one
organisation, so that alone is enough; an owner segment would be a constant.

This handles both shapes of repository without configuration. A meta-repo that
is a single project stays `depop`. A repo split into scopes publishes `depop`
for its default scope and `depop.checkout` for the rest — and because the
default scope keeps the bare name, adding a second scope later does not rename
the graph the first one was already pushing to.

The separator is `.` rather than `/` because a graph name becomes a single
filesystem path component on the hub; `/` is rejected.

Override per-push with `--graph`, or per-scope with `hubGraph` in the scope
config — `--graph` names one database, so a monorepo pinning published names
across `--all-scopes` needs the config field.

The hub refuses a push to a graph another repository seeded, since a name
collision would otherwise replace their knowledge silently. Resend with
`X-KG-Force: 1` when a repo genuinely moved.

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

### `kg export`

Write a knowledge graph to JSONL, one record per line. Entities are identified by
name and type rather than by ID, so a dump stays valid across re-indexing.

```bash
kg export -o backup.jsonl              # this project's default scope
kg export --personal -o personal.jsonl # the personal knowledge store
kg export --scope selling              # a named scope
kg export --journal -o compacted.jsonl # hand-written knowledge only, compacted
```

Writes to stdout when `-o` is omitted. Output is deterministic — records carry
each row's real creation time, not the time of the export — so two dumps of an
unchanged graph are identical and a backup can be diffed or version-controlled.

`--journal` emits only hand-written knowledge, collapsed to its current state: a
create followed by six edits and a delete becomes whatever the graph holds now.
That is journal compaction, and the output is a valid replacement for
`<db>.journal.jsonl`.

---

### `kg import [file]`

Read a JSONL export (or a journal) and apply it to a knowledge graph. Reads stdin
when no file is given.

```bash
kg import backup.jsonl
kg import --personal personal.jsonl
kg import --dry-run backup.jsonl        # report what would change, write nothing
kg import --preserve-project dump.jsonl # keep the file's project ID
```

Importing is idempotent: entities are matched by name and type, and records
already present are skipped rather than duplicated. Importing the same file twice
leaves the same graph.

Records are re-projected onto the graph being imported into, so a dump taken from
one project is searchable after being imported into another. Without that, the
rows would land in the database under the source project's ID and no search
against the target would ever return them. `--preserve-project` opts out.

---

### `kg migrate`

Rebuild any knowledge graph written by a Kuzu storage format this build cannot
read.

```bash
kg migrate     # every scope in this project, plus the personal store
```

Normally unnecessary — a graph that needs rebuilding is rebuilt automatically by
the next `kg index` (project scopes) or the next write (the personal store).
Indexed content is regenerated by re-indexing; hand-written knowledge is restored
from the journal kept beside each database. The previous database is archived as
`<db>.old-<version>` rather than deleted.

---

### `kg embed`

Backfill vector embeddings for anything in the graph that lacks them.

```bash
kg embed                   # default scope
kg embed --scope selling   # a named scope
kg embed --personal        # the personal knowledge store
```

Indexing embeds as it goes when a provider is configured, so this is for the
case where one was configured *afterwards*: it embeds only what is missing,
rather than making you re-index the project to pick embeddings up.

Requires `OPENAI_API_KEY` or `OLLAMA_HOST`, and fails with a non-zero exit if
neither is set. Without embeddings, search falls back to keyword matching.

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

## Symbol visibility

Code symbols carry a `visibility` — `public` or `private` — recorded at index
time from the language's own convention: capitalisation in Go, a leading
underscore in Python. Search uses it as a **tie-break, not a filter**: unexported
symbols are indexed and findable, they just sort below equally-relevant exported
ones.

That is a deliberate reversal. kg used to index exported symbols only, which is
defensible for a library — the exported set is the API surface, and the rest is
noise. It is wrong for a command layer. In a `package main`, *nothing* can be
exported, because nothing can import it. kg's own CLI was 73% invisible to kg.

Three values, and the third matters:

| Value | Meaning |
|-------|---------|
| `public` | Exported in its language's terms |
| `private` | Not exported — indexed, ranked lower, marked `(unexported)` |
| *empty* | No such concept, or not yet known |

Empty is **not** private. It covers files, markdown topics and Makefile targets,
which have no visibility; hand-written entities from `kg add` and the MCP tools,
which are not source symbols at all; and rows written before the column existed.
Search ranks empty alongside `public`, because hand-written knowledge is the last
thing that should sink below an exported getter.

Existing graphs pick the column up on their next write-mode open, and fill it on
the next `kg index`. Read-only opens never migrate, so kg checks whether a graph
actually has the column before querying it — a graph that has not been re-indexed
keeps working, it simply reports empty visibility for everything.

`--public-only` on `kg search`, and `public_only` on the `search_knowledge` MCP
tool, filter to the exported surface. Both keep empty-visibility entities: they
have no API surface to be outside of.

---

## Durability

Two kinds of data live in a kg graph, and they survive differently.

**Indexed content** — everything `kg index` derives from the tree — is disposable.
If it is lost or unreadable, re-indexing regenerates it.

**Hand-written knowledge** — `kg add entity`, `kg add observation`, `kg link`, the
MCP write tools, and the whole personal store — has no source to be regenerated
from. Every such write is therefore also appended to a journal beside the
database:

```
.ai/knowledge.db                 the graph
.ai/knowledge.db.journal.jsonl   every hand-write, in order
.ai/knowledge.db.format.json     which Kuzu build wrote the graph
```

The journal identifies entities by name and type, never by ID — IDs are
regenerated on every re-index — so it stays replayable across rebuilds. Deletes
are recorded too, so forgetting something stays forgotten.

This buys two things:

- **`kg index` no longer destroys hand-written entities.** Indexing clears the
  project before rebuilding it, which used to take hand-written rows with it.
  The journal is replayed afterwards, restoring them.
- **A Kuzu storage-format upgrade costs a slower run, not your knowledge.** kg
  compares `format.json` against its own engine version before opening a graph.
  On a mismatch it archives the old database as `<db>.old-<version>`, rebuilds,
  and replays the journal. See `kg migrate`.

Journals stay small — hand-writes are rare by construction — but `kg export
--journal` compacts one to its current state if you want to prune churn.

Installing kg keeps the binary it replaces, as `kg.old-<version>` beside the new
one (so, normally, in `/usr/local/bin`). That covers the one case neither the
journal nor a rebuild can: a database written before journaling existed, which
has nothing to replay. The two most recent are kept; older ones are pruned.

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

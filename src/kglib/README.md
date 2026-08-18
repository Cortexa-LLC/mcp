# kglib

The shared knowledge-graph library behind the [`kg`](../kg/) binary.
It owns the [KuzuDB](https://kuzudb.com) schema, entity/relation/observation CRUD, keyword +
vector + hybrid search, embedding backends, a read-only Cypher guard, and **federated
(multi-database) search**.

It is a library only — nothing is installed to `$PATH`. Requires CGO (KuzuDB is bundled
statically).

```
module github.com/cortexa-llc/mcp/kglib
```

Consumers depend on it via a local `replace` directive:

```go
// src/kg/go.mod
require github.com/cortexa-llc/mcp/kglib v0.1.0
replace github.com/cortexa-llc/mcp/kglib => ../kglib
```

---

## Opening a store

```go
store, err := kglib.OpenStore(dbPath, &kglib.SchemaConfig{
    AdditionalRelTypes: []string{"CALLS", "IMPORTS", "TAGGED_WITH"},
})
defer store.Close()

ro, err := kglib.OpenStoreReadOnly(dbPath)   // no schema config — schema must already exist
```

`SchemaConfig.AdditionalRelTypes` is how a consumer defines its own edge vocabulary — `kg`
passes its code-relation types (`CALLS`, `IMPORTS`, `RELATES_TO`, …). Relation types not in
the list are rejected by `CreateRelation`.

Every read and write is scoped by a `projectID` string, so one database can hold several
logical graphs. `kg` uses the project root's base name for project graphs and the literal
`"personal"` for the user-global store.

**Entity IDs come from two places, and the difference matters for federation.** The `kg`
indexer assigns deterministic, content-derived IDs (`file:<path>`,
`function:<path>:<name>`), so the same source file indexed into two databases produces the
same ID in both. `CreateEntity` instead assigns a random UUID. Federated merging
deduplicates by entity ID, so it collapses duplicates of *indexed* entities and never
collapses hand-added ones — two manually created entities with identical names remain two
results.

## Core API

| Area | Functions |
|------|-----------|
| Store | `OpenStore`, `OpenStoreReadOnly`, `Close`, `Query`, `QueryParams` |
| Entities | `CreateEntity`, `GetEntity`, `GetEntityByName`, `ListEntities`, `DeleteEntity` |
| Observations | `CreateObservation`, `GetObservations`, `GetTopObservations`, `DeleteObservation` |
| Relations | `CreateRelation`, `GetRelations`, `TraverseRelations`, `DeleteRelation` |
| Search | `KeywordSearch`, `VectorSearch`, `HybridSearch`, `DefaultSearchConfig` |
| Embeddings | `NewEmbedder`, `NewEmbedderFromEnv`, `BatchEmbed`, `SetEmbedding`, `SetObservationEmbedding` |
| Safety | `IsReadOnlyCypher` |
| Federation | `NewFederatedStore`, `LayerConfig`, `FederatedStore` |

`SearchConfig` tunes hybrid scoring — `KeywordWeight` (α, default 0.4), `RecencyWeight`
(β, default 0.1), `Limit` (default 20). A zero-value config passed to `HybridSearch` is
replaced by `DefaultSearchConfig()`.

Embedders come from `OPENAI_API_KEY` (OpenAI) or `OLLAMA_HOST` (local Ollama) via
`NewEmbedderFromEnv`. Embeddings are optional — keyword search works without them.

---

## Federated mode

A `FederatedStore` fans one query out across several KG databases (*layers*) and merges
the results into a single ranked list. This is the mechanism behind `kg`'s layered
scopes: a team scope queries its own database *and* the shared platform database, with
its own knowledge winning on conflicts.

### Constructing one

Pass layers **lowest priority first**, with the primary (read-write) store last:

```go
platform, _ := kglib.OpenStoreReadOnly(filepath.Join(aiDir, "platform.db"))
teamA, _    := kglib.OpenStore(filepath.Join(aiDir, "team-a.db"), schemaCfg)

fs := kglib.NewFederatedStore([]kglib.LayerConfig{
    {Name: "platform", Store: platform, Priority: 1},
    {Name: "team-a",   Store: teamA,    Priority: 11}, // primary — highest priority, last
})
defer fs.Close() // closes every layer; returns the first error encountered

results, err := fs.HybridSearch(projectID, "auth middleware", queryEmbedding, kglib.DefaultSearchConfig())
```

`LayerConfig` is `{Name string, Store *Store, Priority int, ProjectID string}`. `Name` is
only used for priority comparison and warning messages; `Priority` is what actually orders
the merge.

`ProjectID` overrides the project ID used when querying that one layer, and is empty for
most layers. It exists because a layer does not necessarily file its entities under the
caller's project ID: `kg`'s personal store scopes everything to `"personal"`
regardless of which project is being searched, so federating it into a project search
requires querying that layer under its own ID. Without the override such a layer silently
contributes nothing — the query scopes it to a project ID it has never seen.

### Read and write behaviour

- **Reads** (`HybridSearch`, `KeywordSearch`, `VectorSearch`) hit **every** layer.
- **Writes** are not federated. `fs.PrimaryStore()` returns the last (highest-priority)
  layer — open that one read-write and do all mutations through it. `PrimaryStore()`
  returns `nil` if the store has no layers.

### Merge semantics

1. Each layer is queried independently with the same `SearchConfig`.
2. Results are keyed by **entity ID**:
   - unseen entity → kept, tagged with its source layer;
   - duplicate from a **higher**-priority layer → replaces the existing result;
   - duplicate at **equal** priority → the two scores are **summed** (a cross-layer boost);
   - duplicate from a **lower**-priority layer → discarded.
3. The merged set is sorted by score descending and truncated to `config.Limit`.

Because deduplication is by entity ID, only genuinely shared IDs collapse — two layers
that each indexed their own copy of a similarly-named function produce two results.

### Failure handling

A layer whose query fails does **not** fail the whole search. The error is reported as
`Warning: search in layer <name> failed: …` on **stderr** — never stdout, which an MCP
server owns for JSON-RPC — and the remaining layers are merged as normal.

A layer that cannot even be opened is a different case: it must be kept out of the layer
list, because there is no store to query. Check the database exists first; a read-only open
of a missing database fails with the misleading "locked" message described below.

**Do not close a layer's store while a `FederatedStore` still holds it.** `Close()` on the
federation closes every layer, and querying a store after close is a hard crash inside
Kuzu's C layer, not a Go error. Give ownership of each opened store to exactly one
`FederatedStore`.

### Cost and scale

Federation is N queries instead of 1, run sequentially, plus the merge, so latency grows
roughly linearly in the number of layers. Sixty-layer federations query in about a second
in practice, so layer counts in the dozens are fine.

Scaling that far depends on how read-only opens are configured, which is worth knowing
about before you change `openStoreWithConfig`:

| Kuzu setting | Default | `OpenStoreReadOnly` |
|--------------|---------|---------------------|
| `MaxDbSize` | unlimited — reserves ~8 TiB of **virtual address space** per database | 128 GiB |
| `BufferPoolSize` | 80% of total system memory, per database | 256 MiB |

The defaults assume a process holding one database open. A federated query opens one
database per layer at once, so with defaults the 16th open exhausts a 47-bit address space
(arm64 macOS) and fails. Kuzu reports that as a bare `status 1`, which is
indistinguishable from lock contention — `OpenStoreReadOnly` bounds both values so
federation scales to hundreds of layers. Knowledge graphs are tiny relative to either
bound.

**Diagnostic gotcha:** `status 1` is surfaced as *"knowledge graph database is locked by
another process"*, and that same message appears when the database simply **does not
exist** — read-only mode cannot create one. Check the path before hunting for a
lock holder.

### Relationship to `kg`'s federated store

`kg` no longer has its own copy of this type — `src/kg/internal/knowledge/federated.go` is
now a thin adapter that aliases `kglib.FederatedStore` and builds the layer list from `kg`'s
scope configuration:

```go
fs, err := knowledge.OpenFederatedStore(aiDir, scopeConfig, readOnly)
```

That constructor reads `.ai/scope/<name>.json`, opens each `layers` entry read-only, opens
the primary scope's database read-write (or read-only), and assigns priorities automatically
(`i+1` for layers, `len(layers)+10` for the primary). A sibling,
`OpenFederatedStoreWithExtra`, appends caller-supplied layers below the scope layers — how
`kg search --with-personal` mixes in the personal store at priority 0.

Only the search paths go through it: in the `kg` MCP server, `search_knowledge` and
`get_preflight_context` use the federated store when the active scope has layers, while
`query_graph`, `get_file_context`, and every write tool talk to the scope's own database
only.

Merge behaviour therefore lives in exactly one place — this package — and is covered by
`federated_test.go`. Use `NewFederatedStore` directly when your layers don't come from
`.ai/scope/` configs.

For the user-facing scope/layer configuration see
[docs/kg-scopes.md](../../docs/kg-scopes.md) and
[docs/kg-scopes-implementation.md](../../docs/kg-scopes-implementation.md).

---

## Cypher safety

Anything that forwards user- or model-supplied Cypher should gate it first:

```go
if err := kglib.IsReadOnlyCypher(query); err != nil {
    return err // query contains CREATE / MERGE / SET / DELETE / DROP / …
}
result, err := store.Query(query)
```

This is what backs the read-only `query_graph` MCP tool.

## Tests

```bash
cd src/kglib && go test ./...     # CGO required
make test                         # from the repo root — runs kglib + every server
```

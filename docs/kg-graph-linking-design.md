# Cross-layer entity linking for `kg graph` — Design Proposal

**Status:** proposal (not yet implemented) · **Date:** 2026-08-26 · **Follows:** PR #5 (`kg graph`), PR #6 (`--federated`)

How to make a federated entity-relationship graph say something true. `--federated`
(PR #6) merges a scope and its layers into one graph; this proposal fixes *what
connects them*, because the current rule manufactures edges that nobody recorded.

## Problem

A federated render is meant to show how entities across an estate relate. Measured
against the real 61-layer estate at `~/Projects` — 667,858 entities, 1,223,605
relations — it does not:

| Measurement | Value |
|---|---|
| Relations crossing a layer boundary | 67,263 (5.5%) |
| …of those, existing **only** because of the `(name, type)` join | **67,263 (100%)** |
| …involving `node_modules` | 63 (0.1%) |

Not one cross-layer relation was written by an indexer. That is not a bug in the
data: relations are stored per-database, and nothing indexes two repositories into
one database, so the federated union is genuinely 61 disconnected components. Every
bridge in the current picture is produced by the join.

And the joins producing them are not shared symbols. The identifiers generating the
most crossings:

```
Foundation      1567     print   528     forEach  227
CodingKeys      1079     Kr      478     models   221
createDataFrame  539     map     294     hasError 300
```

Swift and JavaScript boilerplate. They evade the existing `--join-max-layers 3`
guard precisely because they appear in two or three layers rather than sixty.

**The shape of the failure:** a name-identity join is right for *coarse* entities
whose names are chosen to be globally meaningful (packages, modules, services) and
wrong for *fine* ones whose names are local (functions, fields, generated types).
The current rule applies it to everything.

## Goals

- **Cross-layer edges that correspond to something real** — a dependency that exists
  in the source, not a name coincidence.
- **A join policy that is per-type and defensible**, defaulting to off for the entity
  kinds where names are local.
- **No loss of the honest parts of PR #6** — duplicate-implementation discovery
  (six `AddressClient` definitions in three layers) stays; it is just no longer
  allowed to imply a call graph.
- **Measurable acceptance**: after the change, the number of cross-layer relations
  that survive should be justifiable edge by edge.

## Non-goals

- **Inferring dependencies from source.** No new parsing; this works from what the
  indexers already record.
- **Rollup/aggregation.** Deferred deliberately — see [Follow-up](#follow-up).
  Aggregating over false edges launders the artifact rather than fixing it.
- **Changing single-scope rendering.** PR #5 behaviour is untouched.

## Design

### 1. Per-type join policy

Replace the single `--join-max-layers` guard with a policy keyed on entity type.

| Entity type | Join across layers | Why |
|---|---|---|
| `package` | **yes** | A package name is chosen to be globally unique; that is what a package name is *for*. |
| `import` | **yes** | An import names something outside the current repo by design. |
| `file` | no | Path-derived and repo-relative; `src/index.ts` is not one file. |
| `type` | opt-in | Sometimes meaningful (`AddressClient`), often not (`CodingKeys`). Off by default. |
| `function` | **no** | `print`, `map`, `forEach`. This is the type generating the false edges. |
| `topic` | no | Documentation headings — the boilerplate case already known. |

The `--join-max-layers` count guard stays as a second filter on the types that do
join, since even a package name can turn out to be generic.

`--join-types file,function` overrides the defaults for anyone who wants the old
behaviour, and `--join-types none` disables joining entirely.

### 2. Package-identity linking — the real edges

Joining is identity ("these are the same node"). Dependency is a *relation*, and
the indexers already record enough to recover it:

- Layer A holds an `import` entity named `@depop/foo` (or `com.depop.foo`).
- Layer B holds a `package` entity named `@depop/foo`.

That is a genuine cross-repo dependency, and it becomes an explicit relation rather
than a fused node:

```
(file in A) --IMPORTS--> (import:@depop/foo in A)  ==>  A's file --DEPENDS_ON--> B's package
```

Rules:

- Match on exact package name. No prefix or fuzzy matching in v1.
- The synthesised edge is typed `DEPENDS_ON` and marked as **derived**, so a reader
  can tell it from a relation an indexer wrote. Derived edges are dashed in mermaid
  and DOT.
- Self-links (an import resolving to a package in the same layer) are dropped —
  that structure is already in the layer's own graph.
- Ambiguity (the same package name defined in two layers) is reported and skipped,
  not guessed.

**This needs measuring before implementation.** The estate holds 5,996 `package`
entities and 66,878 `import` entities; how many imports resolve to a package in
another layer is unknown, and if the answer is near zero the feature is not worth
building as specified. That measurement is the first task, not the first commit.

### 3. Provenance on every edge

`GraphEdge` gains:

```go
// Derived marks an edge synthesised by cross-layer linking rather than read
// from a database. A reader must be able to tell the difference.
Derived bool `json:"derived,omitempty"`
```

Renderers draw derived edges dashed. JSON carries the flag. The header comment
counts them: `%% kg graph: 412 nodes, 690 relations (37 derived)`.

## CLI surface

```bash
kg graph --federated --root @depop/some-lib --depth 2   # who depends on this library
kg graph --federated --join-types package,import,type   # widen the join
kg graph --federated --join-types none                  # union with no bridges at all
kg graph --federated --no-derived                       # only relations an indexer wrote
```

| Flag | Default | Effect |
|---|---|---|
| `--join-types` | `package,import` | Entity types eligible for cross-layer identity join |
| `--join-max-layers` | `3` | Unchanged; a second filter on the eligible types |
| `--no-derived` | off | Suppress synthesised `DEPENDS_ON` edges |

## Implementation sketch

- `internal/knowledge/graph_federated.go`: `joinable` becomes a per-type policy
  rather than a single count threshold.
- New `internal/knowledge/graph_link.go`: `LinkPackages(g *Graph) []GraphEdge` — a
  pure function over the merged graph, so it is testable without databases, in
  keeping with the rest of the graph code.
- `FederationReport` gains `DerivedEdges int` and `AmbiguousPackages []string`.
- Renderers: dashed styling for `Derived`.

## Test plan

- Per-type join policy: a `function` named `print` in three layers stays three nodes;
  a `package` named `@depop/foo` in two layers becomes one.
- Package linking: an import in layer A resolving to a package in layer B produces
  one derived `DEPENDS_ON`; a same-layer resolution produces none; an ambiguous
  package name produces none and is reported.
- Derived edges survive JSON round-trip and render dashed in both formats.
- Regression: the estate's top false-edge generators (`Foundation`, `CodingKeys`,
  `print`) produce zero cross-layer relations under the new defaults.

## Acceptance

Re-run the measurement above. The success condition is not "more edges" — it is that
**every surviving cross-layer relation can be traced to a package an import names**,
and the count of name-coincidence edges is zero under default settings.

## Follow-up

Once edges are real, aggregation becomes worth building — `--rollup layer|package`,
collapsing the graph to a granularity a person can read (tens of nodes, weighted
edges), with `--limit` capping groups rather than entities. Aggregating today's
graph would only make the artifacts look authoritative, which is why it is second
and not first.

## Open questions

1. **Does package linking find anything?** See the measurement gate in §2. If
   cross-layer import→package resolution is rare, the alternative is to accept that
   layers are disconnected and make `--federated` a *comparison* tool (find the same
   name across repos) rather than a connection tool.
2. **Naming conventions across ecosystems.** `@depop/foo` (npm), `com.depop.foo`
   (JVM), and a bare Go module path are three different shapes; v1 matches exact
   names only, which may under-match on JVM layers.
3. **Should `type` join by default?** `AddressClient` argues yes, `CodingKeys` argues
   no. Proposal: off, revisited with data once §2 is measured.

## See also

- [kg-cli-reference.md](kg-cli-reference.md#kg-graph) — the shipped command
- PR #5 — `kg graph`
- PR #6 — `--federated`, whose join this proposal corrects

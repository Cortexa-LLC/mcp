# Cross-layer entity linking for `kg graph` — Design Proposal

**Status:** implemented · gate run and passed · **Date:** 2026-08-26 · **Follows:** PR #5 (`kg graph`), PR #6 (`--federated`)

## Re-measured — 2026-08-29

The estate was re-indexed with `kg` at `3ac2f1a` and every figure re-derived. Two
corrections came out of it, one about history and one about what re-indexing does.

### The warning this replaces was built on a wrong premise

An earlier revision said the figures could not be trusted because before `5211bc2`
the only package entities `kg index` minted were Go's bare identifiers, which the
three-segment floor discards. Two facts contradict that:

- `indexer_treesitter.go` at `5211bc2^` already matched `package_clause` generically,
  and that tree-sitter node type is **Scala as well as Go**. `5211bc2` added Java
  (`package_declaration`) and Kotlin (`package_header`); it did not add Scala.
- The pre-`5211bc2` databases held 4,300 package entities with three or more dotted
  segments. Spot-checking `package:backend_driven.models.item` resolved it to `.scala`
  files under `mobile-api-browse/app/backend_driven/models/item/`.

So the original figures did come from indexed data — Scala's.

### Re-indexing lowers the derived-edge count

The expectation on record was that indexing more package declarations would raise the
number of derived edges. It halves it, and the reason is worth keeping.

| Figure | Before re-index | After |
|---|---|---|
| Package entities | 5,138 | **7,031** |
| …linkable (3+ segments) | 4,300 | **6,140** |
| Derived `DEPENDS_ON` edges | 2,532 | **2,240** |
| Distinct import names resolving | 845 | **557** |
| Same-layer, left alone | 5,046 | **12,832** |
| Ambiguous, skipped | 3,355 | 3,346 |
| Federated merge | 717,633 nodes / 1,229,204 rel | 719,575 / 1,238,257 |
| Full-load cost | 7.6 s, 1.30 GB | 7.0 s, 1.33 GB |

309 previously-derived links disappeared and 21 appeared. Sampling the ones that went
shows both mechanisms, and both are the rule behaving correctly on better data:

- **The package is now defined in the importing layer too.** `com.depop.common` was
  indexed only in `libraries`, so every `clients` import of it looked like a
  cross-repo dependency. Kotlin indexing means `clients` now declares it as well, the
  name resolves to two layers, and the one-layer rule abstains rather than guessing.
- **A more specific package now matches.** Longest-prefix resolution finds a local
  `com.depop.backenddrivenui.models.parameter` where it previously had to settle for a
  shorter name defined elsewhere — so the import is same-layer and no edge is drawn,
  which is why that count more than doubled.

**The earlier cross-layer links were partly artifacts of incomplete indexing.** A
dependency edge drawn because the importing repository's own copy of a package had not
been indexed is not a dependency. Fewer edges here is a more truthful graph, not a
regression — and it sharpens
[open question 3](#open-questions): completeness of indexing *increases* ambiguity, so
the one-layer rule discards more as coverage improves.

### What the re-index cost, and an earlier miscount

971 s for all 61 scopes, no failures, entities +1,901 and relations +8,709 with no
scope losing any. **All 420 observations survived** — the selective re-index preserves
hand-written knowledge as designed.

An earlier draft of this section put the value of re-indexing at "seventy-five `.java`
files and no Kotlin". That was measured with `find -maxdepth 4`, which misses source
trees nested deeper than four levels — the whole Android app among them. The estate
actually holds **30,906 `.scala`, 7,591 `.kt` and 1,835 `.java`** files, so `5211bc2`
gave package declarations to roughly nine thousand files, not seventy-five.

Still unindexed, and unchanged by `5211bc2`: Python, TypeScript and Go, which
[§Follow-up](#follow-up) covers.

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

**Matching rule (set by measurement — see below): longest namespace-prefix match.**
An import resolves to the longest `package` name that is a dotted prefix of it, with
a minimum of three segments so that `com.depop` cannot claim every JVM import in the
estate.

Rules:

- Longest-prefix wins; minimum three segments; ties are impossible by construction.
- **Ambiguity is skipped, not guessed.** If the matched package name is defined in
  more than one layer, no edge is drawn and the name is reported. This is not a rare
  path: it discards 74% of otherwise-matching imports (below).
- The synthesised edge is typed `DEPENDS_ON` and marked as **derived**, so a reader
  can tell it from a relation an indexer wrote. Derived edges are dashed in mermaid
  and DOT.
- Self-links (an import resolving to a package in the same layer) are dropped —
  that structure is already in the layer's own graph.

### Measurement gate — result

Run against the 61-layer estate (5,138 distinct package names, 50,993 distinct import
names) before writing any of the above. The gate was: does import→package resolution
find anything real?

| Rule | Cross-layer matches | Unambiguous |
|---|---|---|
| Exact name match (the original v1 proposal) | 71 | 25 |
| Dotted prefix, min 3 segments | 3,204 | **845** |
| Slash prefix (npm / Go), min 2 segments | 0 | 0 |

**Exact matching fails.** Its 71 hits are generic package fragments — `api`, `auth`,
`client`, `clients`, `app` — and `clients` alone is "defined in" 40 layers. It
reproduces the boilerplate problem one level up.

**Prefix matching passes, and finds real dependencies:**

```
com.depop.auth.client.AccessToken       imported in [ads, attribution] -> package com.depop.auth.client in [libraries]
com.depop.auth.client.AuthClient        imported in [martech, user]    -> package com.depop.auth.client in [libraries]
com.depop.auth.client.ClientCredentials imported in [engage, feature]  -> package com.depop.auth.client in [libraries]
```

Services depending on the shared auth client. The resulting layer pairs are a
plausible dependency map rather than a name-coincidence map: `libraries—mobileapi`
(174), `clients—product` (151), `libraries—product` (102), `libraries—search` (83),
`libraries—user` (80).

**Slash-separated ecosystems get nothing, and the reason is upstream.** Not one of
the 5,138 `package` entity names contains a `/` — the indexers never mint package
entities for npm or Go module paths, so the 8,690 slash-style import names have
nothing to resolve against. This is a limitation of what is indexed, not of the
matching rule; see [Follow-up](#follow-up).

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

Re-run the measurement. The success condition is not "more edges" — it is that
**every surviving cross-layer relation can be traced to a package an import names**,
and the count of name-coincidence edges is zero under default settings.

Yield on the estate, as implemented: **2,240 derived `DEPENDS_ON` edges** on the
re-indexed estate, replacing 67,263 manufactured ones. (2,525 as first shipped;
2,532 after the wildcard-import fix; see [§Re-measured](#re-measured--2026-08-29)
for why better indexing lowers the count.)

> Re-derived 2026-08-29 against the estate — see
> [§Re-measured](#re-measured--2026-08-29) at the top of this document.

The gate predicted 845, and both numbers are right: 845 is the count of distinct
import *names* that resolve, while the 2,525 shipped at the time counted import
*nodes* — the same name appears as a separate node in each layer that imports it,
since imports join only across three layers or fewer. Verified by recomputing the
rule independently against the shipped output: 2,525 = 2,525, and the name count
reproduced 845 exactly.

## Follow-up

**Package entities for npm and Go.** What the indexers mint is now:

| language | package entity | linkable |
|---|---|---|
| Java, Kotlin, Scala | dotted namespace (`com.depop.auth.client`) | yes |
| Go | bare identifier (`auth`) | no — one segment, below the specificity floor |
| TypeScript, JavaScript, Python, C/C++ | none | no |

This table has been wrong in both directions. An early draft said the indexers
mint package entities "only for dotted namespaces", which understated Go. The
correction then overshot, asserting that Go's `package_clause` was the *only*
source and that every package entity was therefore filtered out — but
`package_clause` is Scala's node type too, so Scala has been minting dotted
package names since well before `5211bc2`. That change added Java and Kotlin.
See [§Re-measured](#re-measured--2026-08-29).

The remaining gap is npm and Go. Reading `package.json` `name` fields and
`go.mod` module paths would give the web, client and tooling layers a namespace
specific enough to link, where today they get no derived edges. That is an
indexer change, independent of this proposal and probably larger than it.

Once edges are real, aggregation becomes worth building — `--rollup layer|package`,
collapsing the graph to a granularity a person can read (tens of nodes, weighted
edges), with `--limit` capping groups rather than entities. Aggregating today's
graph would only make the artifacts look authoritative, which is why it is second
and not first.

## Open questions

1. ~~Does package linking find anything?~~ **Answered by the gate:** yes, with
   prefix matching (845 unambiguous cross-layer resolutions at the time, 557 on the
   re-indexed estate); no, with exact matching (71, mostly generic). The matching rule
   in §2 changed accordingly.
2. ~~Naming conventions across ecosystems.~~ **Answered:** dotted namespaces resolve;
   npm and Go produce nothing because no package entity name contains a `/`. Fixing
   that is an indexer change — see [Follow-up](#follow-up).
3. **Is the ambiguity rule still acceptable?** On the re-indexed estate it discards
   3,346 imports and keeps 2,240 — and the discarded share *grows as indexing coverage
   improves*, because a package indexed in more repositories resolves to more layers.
   The linking therefore gets weaker over time rather than stronger, which the
   [re-measurement](#re-measured--2026-08-29) is the evidence for. The discarded ones
   are mostly packages genuinely duplicated across repos. Options: accept the loss;
   draw to all candidate layers with a marker; rank by layer priority; or prefer the
   importing layer's own definition, which would reclassify most of these as
   same-layer and draw nothing — the truthful answer in the `com.depop.common` case.
   v1 accepts the loss and reports the count; that is worth revisiting first.
4. **Should `type` join by default?** `AddressClient` argues yes, `CodingKeys` argues
   no. Proposal: off, revisited once derived edges exist and can be compared against.

## See also

- [kg-cli-reference.md](kg-cli-reference.md#kg-graph) — the shipped command
- PR #5 — `kg graph`
- PR #6 — `--federated`, whose join this proposal corrects

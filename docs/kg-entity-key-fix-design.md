# ADR: Fix Entity primary-key / project-scope mismatch in the kg indexer

Status: **Accepted — Option A (A1 variant)**. Multi-project-per-DB is a supported
scenario (product decision, 2026-10-09), so the global-PK bug is fixed by a
namespaced primary key, not by foreclosing multi-project DBs.
Date: 2026-10-09

## Context

`kg index` emits large volumes of warnings of the form:

```
Note: COPY FROM JSON unavailable (... Found duplicated primary key value ...); falling back to row-by-row insert
Warning: insert entity <kind>:<id>: ... Found duplicated primary key value <id>, which violates the uniqueness constraint ...
```

These are **not benign**. Investigation (source-verified) shows:

1. **The `Entity` primary key is global `id` alone** — `src/kglib/schema.go:45`
   (`id STRING PRIMARY KEY`). Same for `Observation` (`schema.go:57`).
2. **Every clear/delete and nearly every read is scoped by `project_id`**:
   - `clearCodeDerivedData` — `src/kg/internal/knowledge/indexer.go:280-288`
   - `clearProjectData` — `indexer.go:373-399`
   - `deleteProjectRelations` — `indexer.go:209-226`
   - reads in `graph.go:136-178`, `ListEntities` (`src/kglib/entity.go:326`)
3. **In-run entity dedup is airtight**: `writeEntityVis` (`indexer.go:584`) is the
   *only* append site and guards every append with `if seen[id] { return false }`.
   So a batch never contains duplicate ids — collisions are strictly against rows
   **already in the DB**.
4. **Relations match endpoints by bare `id` with no project filter in the MATCH** —
   `CreateRelation`, `src/kglib/relation.go:24-28`
   (`WHERE from.id = $from_id AND to.id = $to_id`).

### Failure mechanism

Because the PK is global but the clear is project-scoped, any pre-existing
code-derived row under a **different `project_id`** survives the clear. When the
current run writes the identically-id'd entity it collides and is **silently
dropped** (first/old row wins; `CREATE` at `indexer.go:659-677` and the COPY path
at `indexer.go:644-653`; the log indexer repeats the pattern at
`log_indexer.go:278-335`). Downstream:

- The current project's entity, plus its freshly-computed observations/relations
  (which attach **by id**), never land.
- Relations can bind to the *other* project's surviving node (relation.go MATCH is
  project-blind), or be dropped as dangling (`batchCreateRelations`, indexer.go:763).
- `kg stats` / graph reads scoped to the live `project_id` under-return.

A mismatched-`project_id` partition arises from any of: legacy `project_id = ""`
rows, a changed `projectIDFromCwd` (`src/kg/index.go:140`) across versions, a
moved/renamed repo, or two scope configs whose `cfg.Database` point at the same DB
file (`indexScopeDB`, `src/kg/index.go:103-118`). The *presence* of the warnings
proves such a partition exists — a same-project prior run would have been emptied
by the clear, producing no collision.

### Hard constraint

Kuzu **v0.11.3** (`src/kg*/go.mod`) node tables support exactly **one** PRIMARY KEY
column. A literal composite `(project_id, id)` node PK is **not expressible**. The
realistic options are a namespaced single key, or a structural one-project-per-DB
guarantee.

## Gating open question (needs a human decision)

**Is "multiple `project_id`s in one DB file" a supported scenario, or an accident?**

- The deployment path opens **one DB per scope** and derives `project_id` from cwd
  (`index.go:103-140`), i.e. one project per file in practice.
- But `KGMeta` keys by `project_id` (`schema.go:69`), and reads filter by
  `project_id` — the schema *looks* built for many projects per file.
- Federated linking (`graph_federated.go`) links across **separate** graph files,
  not multiple projects in one file.

The answer selects the recommended option:

- **One project per DB is the real model → Option B (+ C).** Minimal, correct.
- **Multi-project-per-DB must be supported → Option A.** Larger, fully correct.

Recommendation absent other input: **one project per DB is the real model → Option B
primary, C as hardening.** Multi-project-per-file appears to be the accident that
creates stale partitions.

## Accepted design — Option A1 (namespaced `pk`, `id` stays local)

Decision: multi-project-per-DB **must** be supported, so Option B is rejected and
the key is made project-safe via a namespaced primary key. Within Option A, the
**A1** variant is chosen: keep the existing `id` column human-facing and unchanged
(`file:…`, `function:…`, UUIDs), and add a separate namespaced primary-key column.
This preserves the id contract that search, CLI display, and the journal
`stableIDHint` all depend on; A2 (rewriting `id` itself) is rejected as needlessly
breaking those.

Concrete decisions:

1. **Schema** (`src/kglib/schema.go`): Entity and Observation gain
   `pk STRING PRIMARY KEY`; the current `id` becomes a plain (non-unique) STRING
   attribute. `entity_id` on Observation stays, but observation→entity joins key on
   the pair `(project_id, id)` (see 4).
2. **Key derivation** — one central helper:
   `pk(projectID, id) = projectID + "\x00" + id`. NUL is the separator because
   neither a `project_id` nor a code-derived/UUID `id` ever contains one; document
   that invariant next to the helper. All writes derive `pk` here, never ad hoc.
3. **Writes populate `pk`**: COPY column lists (`indexer.go:646`, the log-indexer
   COPY in `log_indexer.go`, the observation COPY) and every `CREATE`
   (`indexer.go:659-677`, `entity.go:148`, `log_indexer.go` fallback) add `pk`.
4. **Lookups/endpoint matches become project-qualified** (the real correctness
   fix): every `{id: …}` / `WHERE e.id = …` that is not already project-scoped must
   match on `(project_id, id)` (equivalently on `pk`):
   - `CreateRelation` MATCH — `relation.go:24-28` (currently project-blind — this is
     the cross-project-bleed site). Add `from.project_id = $pid AND to.project_id =
     $pid`, or match on `from.pk`/`to.pk`.
   - `GetEntity` — `entity.go` (confirm it filters project_id; if it keys on id
     alone, qualify it).
   - Observation joins / `HAS_OBSERVATION` creation keyed on `entity_id`.
   - `clearCodeDerivedData`, `clearProjectData`, `deleteProjectRelations` already
     filter `project_id`; with a project-unique `pk` the DETACH DELETE now both
     targets only this project's rows **and** stops colliding — no logic change
     beyond confirming they read the new column where needed.
5. **Upsert**: with `pk` project-safe, switch entity writes `CREATE` → `MERGE` on
   `pk` with last-write-wins refresh of `name`/`type`/`updated_at`. This makes
   re-index idempotent and eliminates the warnings outright. (`MERGE` was unsafe
   under the old global PK; it is correct now.)
6. **Federated linking** — `graph_federated.go`: cross-graph joins must key on the
   **local `id`** (and/or name), never on `pk`, since `pk` embeds a per-graph
   `project_id` that differs across files. Audit every cross-graph comparison.
7. **Migration**: Kuzu has no `ALTER TABLE … ADD PRIMARY KEY`, so the new `pk`
   column forces a table recreate → rebuild-from-source. Bump `KGMeta.kg_version`
   (`StampMeta`, `index.go:216`); on version mismatch, ride `archiveIfMismatch`
   (`index.go:124`) to archive the old DB and rebuild fresh, then replay hand-writes
   via `restoreHandWrites` (`index.go:210`). The journal is the only copy of
   hand-writes — archive, never delete.

Open sub-question for the engineer to resolve during implementation: whether Kuzu
rel tables in v0.11.3 let `MERGE`/`MATCH` resolve endpoints efficiently by a
non-PK `(project_id, id)` pair, or whether endpoint matching should go through the
`pk` column directly (likely faster, since `pk` is the indexed PK). Prefer `pk`.

## Options (for the record)

### Option A — Namespaced single primary key (supports multi-project-per-DB)

Store PK as `pk = project_id + "\x00" + localID`; keep `id` (the current
`file:…`/`function:…`/UUID value) as a **non-PK indexed** attribute for display,
search, and the journal hint.

- Rewrite all MATCH/CREATE/COPY on Entity/Observation to key on `pk`, including
  relation endpoint matching (`relation.go:24-28` must match on `pk`, not `id`).
- Blast radius (each must change):
  - id construction sites: `indexer_treesitter.go:198` (`qualifiedSymbolID`),
    and `fmt.Sprintf("file:%s"|"type:%s:%s"|"function:%s:%s"|"import:%s")` across
    `indexer_yaml.go`, `indexer_makefile.go`, `indexer_asm.go`,
    `indexer_jsonschema.go`, `indexer_graphql.go`, `indexer_html.go`,
    `indexer_markdown.go`, `indexer_pdf.go`, `indexer_cmake.go` — these now feed
    `localID`, with `pk` derived centrally in `writeEntityVis`.
  - `codeDerivedIDClause` / `codeDerivedIDPrefixes` — `kg_types.go:34-52`
    (prefix test must run against `localID`, not `pk`).
  - COPY column lists — `indexer.go:646`, `log_indexer.go` COPY, observation COPY.
  - reads — `graph.go`, `entity.go` (`ListEntities`, `GetEntity`),
    `src/kglib/search.go`.
  - journal `stableIDHint` and replay — `relation.go` note, `index.go:210`
    (`restoreHandWrites`); the hint must remain the *localID* so replay still works.
  - federated cross-graph linking — `graph_federated.go` (joins by id across files).
- Pro: preserves multi-project-per-DB; fully correct. Con: wide, touches the
  human-facing/stable id contract and the journal.

### Option B — Enforce one `project_id` per DB file (recommended primary)

At index time, read the DB's existing `KGMeta.project_id`. If it differs from the
current run's `project_id` (or legacy `""` rows exist), treat it exactly like a
storage-format mismatch: **archive the old DB and rebuild fresh** — reusing the
existing `archiveIfMismatch` lever (`index.go:124`). With one project per file, the
global `id` PK is project-scoped by construction and collisions cannot occur.

- Change surface is small and localized to `index.go` (a project_id check beside
  `archiveIfMismatch`) plus a KGMeta read helper.
- Journal safety: archived DB keeps its journal (`index.go:128-132`); hand-writes
  replay into the fresh DB via `restoreHandWrites` (`index.go:210`), which is
  idempotent.
- Pro: minimal, correct, matches real deployment. Con: forecloses
  multi-project-per-DB (acceptable iff the gating question resolves that way).

### Option C — Global code-derived clear + MERGE upsert (hardening, pairs with B)

Under the one-project-per-DB premise, code-derived ids are globally unique within a
DB, so:

- Drop the `{project_id: $project_id}` filter from the **code-derived** DETACH DELETE
  in `clearCodeDerivedData` (indexer.go:280-288) so a rebuild clears *all*
  code-derived rows regardless of a stale project_id — making re-index idempotent and
  warning-free even on a legacy DB.
- Switch entity writes `CREATE` → Kuzu **`MERGE`** (idempotent upsert) with
  last-write-wins refresh of `name`/`type`/`updated_at`.
  - **MERGE is unsafe under the current global PK** (one project would overwrite
    another's row). It is correct only after B (or A) makes the key project-safe.

## Migration & schema versioning

- Bump the schema/kg version stamp (`KGMeta.kg_version`, written by `StampMeta`,
  `index.go:216`) so older DBs are recognized.
- Option B needs no table rewrite (fresh rebuild on project_id mismatch).
- Option A requires a one-time rebuild (new PK column); ride `archiveIfMismatch`:
  on version mismatch, archive + rebuild-from-source (code-derived data is
  regenerable), then journal-replay hand-writes.
- In both, the journal is the **only** copy of hand-written entities — migration must
  archive (not delete) the prior DB and run `restoreHandWrites` against the fresh one.

## Acceptance criteria

- [ ] `cd src/kg && go build ./... && go test ./...` exits 0
- [ ] `cd src/kglib && go build ./... && go test ./...` exits 0
- [ ] New regression test: index project A, then project B that shares entity ids
      (e.g. both `import:logging`, `file:common.py`); assert **both** projects retain
      their own entities and `kg stats` per project is correct.
- [ ] Re-indexing an already-indexed project emits **zero** `duplicated primary key`
      warnings and **no** `COPY FROM JSON unavailable` note.
- [ ] Hand-written entities/relations (`kg add`) survive a re-index and a migration
      rebuild (journal replay verified).
- [ ] `MATCH (e:Entity) RETURN DISTINCT e.project_id` on a migrated DB shows exactly
      one project_id (Option B) — a field diagnostic to confirm the fix landed.

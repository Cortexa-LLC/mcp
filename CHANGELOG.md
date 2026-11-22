# Changelog

Notable changes to the MCP servers in this repository — `kg`, `kglib`, and `markitdown`.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Builds are
version-stamped from `git describe`, so an installed binary reports the tag it descends
from plus its commit (`v0.1.0-43-g0ae49e6`); only `v0.1.0` has been tagged so far, and
everything below it is unreleased.

## [Unreleased]

### Added

- **`kg graph`** — render the knowledge graph as mermaid, DOT, or JSON, with `--root`,
  `--depth`, `--type`, and `--limit`. (#5)
- **`kg graph --federated`** — one graph across a scope and every local layer it
  federates with, grouped into a subgraph per layer. (#6)
- **`kg graph --format html`** — a self-contained interactive page for the same
  neighbourhood the other formats draw: drag, zoom, click a node to isolate its
  neighbourhood, filter by name or entity type. Carries no external references,
  so it works offline and under a strict script-src policy, and its output is
  deterministic like the other formats.
- **Cross-layer entity linking** for `kg graph --federated`: derived `DEPENDS_ON` edges
  are resolved from imports to the packages they name, replacing the `(name, type)`
  join that manufactured edges nobody recorded. `--join-types` and `--no-derived`
  control it. (#7 design, #10 implementation, [design](docs/kg-graph-linking-design.md))
- **Package declarations indexed for Java and Kotlin**, giving those layers the dotted
  namespaces cross-layer linking needs. Scala already had them: its `package_clause` is
  the same tree-sitter node Go uses, and the indexer matched it generically. Go's own
  package names are single identifiers with no dot to match on.
- **Shared knowledge hub** — `kg hub serve` and `kg hub list` host read-only graphs;
  `kg push` seeds them. Hub graphs federate into local search as remote layers, queried
  in place rather than downloaded.
- **Per-user hub authentication** via a pluggable verifier, with token, OIDC, and
  GitHub org/team membership verifiers built.
- **`kg health`** — a report on graph condition, and selective re-index that preserves
  hand-written knowledge.
- **Personal knowledge store** (`kg personal init`) — knowledge that follows you between
  repositories, searchable alongside a project graph via `--with-personal`. Replaces the
  `upk` server.
- **Federation plumbing** — the `SearchLayer` interface, a `KGMeta` provenance stamp, and
  `kg meta`.
- **Export and import**, and survival across Kuzu storage-format upgrades.
- **Scala indexing** and call-graph resolution.
- Unexported symbols are indexed, carrying a visibility marker.
- **CI** — per-module Go build and test, enforced `gofmt`, and an automated Claude
  reviewer that posts a verdict as a required check. (#8, #13)

### Fixed

- **Windows builds.** Both servers now build, install, and run on Windows, which had
  never been built there before. Four things were in the way:
  - `github.com/coder/hnsw` calls `renameio.TempFile`, but `github.com/google/renameio`
    guards every file with a `!windows` constraint in v1 and v2 alike, so `kglib` would
    not compile at all. `src/renameio-compat` stands in for it through a `replace`
    directive, mirroring upstream's semantics so macOS and Linux behaviour is unchanged.
  - Every `COPY ... FROM '<path>'` bulk load failed to parse on Windows, because Kuzu
    reads the backslashes in a native path as string escapes. Indexing still produced a
    correct graph — it fell back to row-by-row inserts — but silently gave up the bulk
    loader. Paths are now rendered with `cypherPathLiteral`.
  - `install.py` now copies `kuzu_shared.dll` beside `kg.exe`. Kuzu links dynamically on
    Windows, which has no rpath to fall back on, so the installed binary exited at
    startup with `kuzu_shared.dll: cannot open shared object file`.
  - The MCP config snippet `install.py` prints is built with `json.dumps`, so Windows
    paths come out as valid JSON instead of unescaped `C:\Users\...`.
- **`markitdown` binary name** in `install.py` and the README caught up with the
  `markitdown-mcp` → `markitdown` rename.
- **`kg index` no longer destroys hand-written knowledge.** Hand-writes are journalled
  and replayed after indexing.
- **Security:** Cypher and repository config could escape the filesystem sandbox; both
  are now guarded on every surface. Panics no longer end an MCP session.
- **Security:** a repository can no longer choose where `kg` sends your hub token.
- Hub seeding hardened against hostile metadata and slow-push denial of service.
- Search results are deterministic, and a broken primary layer surfaces as an error
  instead of being silently hidden.
- `--public-only` now filters, and the demotion it implies actually fires.
- Same-named methods on different types get distinct identities; relations are swept by
  catalog rather than by name.
- An export is now a faithful backup.
- Hub layers are sent in the hub namespace, and installs are atomic.
- The Kuzu version stays resolvable when Go build info omits it (notably in test
  binaries).
- Observation-age statistics over a graph with no timestamped rows no longer fail the
  whole `kg health` run; a null registry entry no longer panics the hub at startup. (#4)
- Legacy PDF cleanup matches extensions case-insensitively.
- Binary files that share a source extension (e.g. HLS `.ts` video segments masquerading
  as TypeScript) are skipped by a NUL-byte sniff before the tree-sitter parse, instead of
  burning the full 10s parse deadline on content the parser cannot handle. Skips are
  counted in the index summary. (#18)

### Changed

- **The hub no longer keeps a `current` symlink.** A graph's live database is
  resolved as `graphs/<name>/<commit>/knowledge.db` from the commit in
  `registry.json`, whose atomic write was already `install`'s commit point. Two
  records of the same fact could disagree — a search read the database from the
  symlink but its ProjectID from the registry, answering HTTP 200 with zero
  results whenever the project ID had changed — and keeping them in step cost a
  second rollback path in `install` and a repair pass at startup. Both are gone.

  This is also what unblocks the hub on Windows, where creating a symlink needs
  `SeCreateSymbolicLinkPrivilege` that normal users do not hold, so every push
  failed with "A required privilege is not held by the client". A directory
  junction needs no privilege but cannot be swapped atomically, because Windows
  refuses to rename any directory over an existing one; renaming a file over a
  file, which is how the registry commits, works everywhere.

  Existing hubs need no migration — the registry already records the commit, and
  commit directories are already named for it. A stale `current` is inert and
  the next push's prune removes it. The change is one-way: a hub data directory
  written by this version is not readable by an older binary, which still looks
  for `current`.
- Hub graph names are derived from the repository rather than supplied.
- `make install` retains the outgoing binary, so a bad build can be rolled back.
- The personal store moved out of `~/.ai` to `~/.kg` (`KG_HOME`).

### Removed

- The `slack-mcp` server.
- The `upk` server, superseded by the personal knowledge store.

## [v0.1.0] — 2026-04-21

First tagged release. `kg` as an independent Go module with multi-scope monorepo graphs
and federated queries across layered scopes; `markitdown` as a native Go MCP server with
PDF, image OCR, and PPTX support, plus optional OpenAI Vision enhancement; `kglib`
extracted as the shared schema, search, and embedder library.

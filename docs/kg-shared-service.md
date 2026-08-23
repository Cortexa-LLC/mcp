# The Shared Knowledge Hub

One HTTP service hosting read-only knowledge graphs for a whole **organisation**, so a
project can search graphs it never indexed. Teams push their scope databases with
`kg push`; consumers list them as `remotes` in a scope config and every `kg search` (and
MCP `search_knowledge`) federates the hub's answers in alongside local results. The hub
never sees your source — it stores already-indexed databases, each stamped with the git
commit it was built from.

A [scope layer](kg-scopes.md) answers *what does my sibling team's code look like, in
this repo*. A hub remote answers the same question **across repos** — without cloning or
indexing the other codebase.

## Run a hub

```bash
KG_HUB_SEED_TOKEN=... kg hub serve                     # listen on :7411, data in ~/.kg-hub
kg hub serve --listen 127.0.0.1:8080 --data /srv/kg    # explicit address and storage
```

| Flag / Variable | Effect |
|-----------------|--------|
| `--listen` | Listen address (default `:7411`) |
| `--data` | Data directory (default `$KG_HUB_HOME`, else `~/.kg-hub`) |
| `KG_HUB_READ_AUTH` | Auth scheme for reads: `token` (default), `oidc` or `github` |
| `KG_HUB_SEED_AUTH` | Auth scheme for `kg push`: `token` (default), `oidc` or `github` |
| `KG_HUB_READ_TOKEN` | token mode: bearer token required for reads; unset = open reads |
| `KG_HUB_SEED_TOKEN` | token mode: bearer token required for `kg push`; unset = seeding disabled |
| `KG_HUB_OIDC_ISSUER` | oidc mode: issuer URL; its discovery document supplies the signing keys |
| `KG_HUB_OIDC_AUDIENCE` | oidc mode: required `aud` claim |
| `KG_HUB_GITHUB_ORG` | github mode: org the caller must be an active member of |
| `KG_HUB_GITHUB_TEAMS` | github mode: comma-separated team slugs; set = membership of at least one also required |
| `KG_HUB_GITHUB_API` | github mode: API base URL for GitHub Enterprise (default `https://api.github.com`) |
| `KG_HUB_SEED_SUBJECTS` | oidc/github seeding: comma-separated subjects/emails/logins allowed to push; unset = any authenticated identity |

The tokens are shared secrets, a v1 expedient: one token held by everyone cannot be
revoked per person and cannot tell you who pushed what. Per-user identity over OIDC is
the agreed direction — see
[the design doc's Authentication section](kg-shared-service-design.md#authentication) —
and the hub now supports it: set `KG_HUB_READ_AUTH=oidc` with the issuer and audience
variables, and readers authenticate with an access token from your IdP instead of a
shared secret; the pusher's identity is named in the hub's log. For orgs whose identity
lives on GitHub (which has no OIDC for user sign-in), `KG_HUB_READ_AUTH=github` accepts
a GitHub token with the `read:org` scope and checks org — and optionally team —
membership instead. The schemes mix per
surface, so the expected migration is OIDC reads first while CI keeps pushing with the
seed token. Treat any token as a credential and store it accordingly rather than
pasting it into shell history or committed config.

Or in a container (build from the repo's `src/` directory):

```bash
docker build -f kg/Dockerfile -t kg-hub . && docker run -p 7411:7411 -v kg-hub-data:/data -e KG_HUB_SEED_TOKEN=... kg-hub
```

The hub is one Go binary plus a data directory — single-node by design. For hosting
options (VPS + Tailscale, Fly.io, existing k8s) and disk/backup guidance, see
[kg-shared-service-design.md](kg-shared-service-design.md#hosting).

## Seed it

From CI, on merge to main:

```bash
kg index --all
KG_HUB_SEED_TOKEN=... kg push --all-scopes --hub http://hub.internal:7411
```

From an existing federated project, the same two commands work from a checkout — each
scope becomes a hub graph under its scope name, carrying its layer topology (`team-a`
pushed with `layers: ["platform"]` tells the hub that `team-a` builds on `platform`).

Two rules worth knowing:

- **Dirty trees are refused.** A database stamped `(dirty)` at index time will not push
  unless you pass `--allow-dirty` — a hub graph should correspond to a commit others can
  check out.
- **Provenance travels with the graph.** Every push records the git commit, repo URL,
  and index time from the database's own stamp (inspect yours with `kg meta`). That is
  what `kg hub list` and `kg hub status` report.

## Consume it

Two pieces of configuration in the consuming project. First, point the project at the
hub in `.ai/config.json`:

```json
{
  "hub": "http://hub.internal:7411",
  "defaultScope": "main"
}
```

The hub URL is **not** read from this file. Federated search sends
`KG_HUB_READ_TOKEN` to the hub as a bearer token, so whoever chooses the URL
chooses where that credential goes — and `.ai/` is committed and shared, so a
repository you cloned must not be able to choose. Set it per user instead:

```bash
kg config set-hub https://kg.internal:7411   # stored in ~/.kg/config.json
kg config show-hub                            # what is trusted, and what this project asks for
```

A `"hub"` key here is still *reported* — `kg config show-hub` will tell you the
project expects it and offer the command to trust it — so a team can share the
address without sharing the authority to use it.


Then list hub graphs as `remotes` in the scope that should see them
(`.ai/scope/main.json`):

```json
{
  "name": "main",
  "database": "main.db",
  "remotes": ["team-a", "platform"]
}
```

That's it — `kg search` and the MCP search tools now federate the hub in. If the hub
requires read auth, export `KG_HUB_READ_TOKEN` in the client's environment.

Results merge by priority, exactly like [scope layers](kg-scopes.md) — remotes slot in
below everything local:

| Priority | Layer |
|----------|-------|
| lowest | personal store (`--with-personal`) |
| ↑ | remotes, in config order |
| ↑ | local scope layers |
| highest | the scope's own database |

So a hub answer never crowds out local knowledge: where a local entity and a remote one
both match, the local one wins.

**Degradation:** remotes are read-only and best-effort. When the hub is down or
unreachable, each search prints a warning to stderr and returns local results — a dead
hub slows a search but never breaks it. Remote layers are searched sequentially with a
3-second HTTP timeout each, so the worst case is 3 seconds per listed remote
(connection-refused fails much faster; the full timeout only bites when the hub is
black-holed). Writes (`kg add`, `kg index`) never touch the hub.

## Check freshness: `kg hub status`

Compares the hub's copy of every graph related to this project — your scope's remotes,
plus any local scope names the hub also hosts — against your local git history:

```bash
kg hub status
```

```
NAME      COMMIT        INDEXED           STATUS
platform  3f2a91c04b7d  2026-08-20 09:14  hub is 4 commit(s) behind local HEAD
team-a    9c81d2e4f0aa  2026-08-20 10:02  up to date with local HEAD
```

`not in local history (different repo?)` means the hub's commit is not in your
checkout's history — normal when the remote graph comes from another repository. Takes
`--hub` and `--scope` to override the defaults.

## Remote graphs bring their layers

A hub graph's own layer topology is honoured server-side: searching the remote `team-a`
automatically searches `platform` too when `team-a` was pushed with
`layers: ["platform"]` (the client asks with `include_layers`, the hub expands it). List
only the graph you care about in `remotes` — its hub-side foundations come along for
free.

## See also

- [kg-shared-service-design.md](kg-shared-service-design.md) — the design: architecture, storage layout, hosting options, rollout
- [kg-scopes.md](kg-scopes.md) — scopes and local layer federation, which remotes plug into
- [kg-personal-store.md](kg-personal-store.md) — the personal store, the other cross-project layer
- [kg-cli-reference.md](kg-cli-reference.md) — `kg push`, `kg hub serve`, `kg hub list`, `kg hub status` reference

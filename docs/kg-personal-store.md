# The Personal Knowledge Store

One knowledge graph per **user**, held outside any project, for the knowledge that follows
you between repositories: decisions and their reasoning, conversations, constraints you keep
rediscovering, who owns what.

A project graph answers *how does this codebase work*. The personal store answers *what have
I already learned, anywhere*.

## Create it

```bash
kg personal init
```

```
✅ Created personal knowledge store: /Users/you/.kg/knowledge.db
```

Location is `$KG_HOME` if set, otherwise `~/.kg/knowledge.db`. `kg personal path` prints it,
and `kg personal init` is safe to re-run.

It deliberately does **not** live in `~/.ai`. Project-root discovery treats a `.ai`
directory as a root marker and picks the nearest ancestor that has one, ahead of the git
root — so a `~/.ai` would make `$HOME` the project root for every repo beneath it without its
own `.ai`, and `kg index` there would try to walk your entire home directory. For the same
reason, do not point `KG_HOME` at a directory named `.ai`. You can skip init entirely — the first `--personal`
write creates the store — but running it explicitly confirms where it landed.

Set `KG_HOME` to keep the store on synced storage:

```bash
export KG_HOME="$HOME/Library/Mobile Documents/com~apple~CloudDocs/kg"
```

## Write to it

Any write command takes `--personal`:

```bash
kg add entity --personal --name "kafka-retention-decision" --type decision \
  --summary "[DECISION] 7-day retention: replay window beats storage cost"

kg add observation --personal "<entity-id>" "[CAVEAT] Compacted topics may need a different window"

kg link --personal "<from-id>" --rel RELATES_TO "<to-id>"
```

Entity types are free-form — `conversation`, `decision`, `learning`, `person`, `topic` are
useful conventions, not a fixed list. Relation types are validated: `RELATES_TO`,
`SUPERSEDES`, `DOCUMENTS`, `DEPENDS_ON`, `CAUSED_BY`, `FIXES`, `IMPLEMENTS`, `TESTS`,
`CALLS`, `IMPORTS`, `CONTAINS`, `BELONGS_TO`. Anything else is rejected on write.

Prefix observations with `[DECISION]`, `[CAVEAT]`, `[ACTION]`, or `[INVESTIGATION]` — the
same convention project graphs use, so one search habit covers both.

**Never run `kg index` against the personal store.** It holds hand-written knowledge; there
is no source tree to scan.

## Read it back

```bash
kg search "retention" --personal          # personal store only
kg search "retention" --with-personal     # this project's graph plus personal
kg show "<entity-id>" --personal
kg stats --personal
```

`--with-personal` is the point of the design. From inside any repo, one search returns the code
knowledge *and* the conversation where the relevant decision was made. `--personal` and
`--with-personal` are mutually exclusive: the first replaces the project search, the second
adds to it.

## How `--with-personal` federates

The personal store joins the project's search as an ordinary
[federation layer](kg-scopes.md), with two specifics:

- **Priority 0** — below every scope layer, so project knowledge outranks personal notes
  wherever both match a query. Personal entries add context; they do not crowd out code.
- **Its own project ID** — everything in the personal store is filed under `personal`, while
  project entities are filed under the project's own ID. The layer carries a
  `LayerConfig.ProjectID` override so a single query reaches both. Without that override a
  cross-store search silently returns nothing from the personal side.

If the personal store does not exist, `--with-personal` prints a note to stderr and searches
the project alone rather than failing.

Only search federates. `kg show --personal` and `kg stats --personal` read the personal store
directly, and writes always go to exactly one store — whichever `--personal` selects.

## MCP tools

Agents reach the personal store through two tools, kept deliberately separate from the
project ones. Both appear only if the store already exists — a user without one sees no
personal tools at all.

| Tool | Registered when | Purpose |
|------|-----------------|---------|
| `search_personal_knowledge` | the store exists | Search personal knowledge. Additive: it never changes what `search_knowledge` returns |
| `add_personal_knowledge` | writes are enabled (below) | Record an entry, on the user's explicit instruction only |

Keeping the read tool separate rather than folding personal results into
`search_knowledge` means provenance is never ambiguous. `SearchResult` carries no source
field, so a merged list would leave an agent unable to tell a hand-written note (possibly
older than the code) from indexed source truth. Two tools, two answers.

### Protective mode: enabling writes

Writes are **off by default** — without being enabled, `add_personal_knowledge` is not
registered, so an agent has no way to add to the store however it is asked. Turn it on per
project in `.mcp.json`:

```json
{
  "mcpServers": {
    "kg": {
      "command": "/usr/local/bin/kg",
      "args": ["server", "--stdio", "--personal-writes"]
    }
  }
}
```

Or without touching MCP config, via the environment:

```bash
export KG_PERSONAL_WRITES=1
```

Accepted as on: `1`, `true`, `yes`, or any other non-empty value. Off: unset, empty, `0`,
`false`, `no`, `off`.

### What guards a write once enabled

Say "add this to my personal knowledge" and the agent calls the tool. Four things constrain
what it can do:

1. **A quoted request is mandatory.** `user_request` must contain the user's own words
   asking for the save, and the write is refused without it. An agent recording something
   unprompted has nothing to put there. It is also refused if it merely duplicates the
   content, which is the obvious shortcut.
2. **Provenance is permanent.** Every entry gets an extra observation reading
   `[VIA:mcp] [REQUESTED] "<the user's words>"`. Agent-written entries stay distinguishable
   from your own for the life of the entry, and the request that produced them travels with
   them.
3. **Size is capped at 8 KB.** Enough for a distilled decision, not enough for a
   transcript. The store has no re-index to clean up after a dump.
4. **Every write reports its own undo.** The tool's response names the entity ID plus
   `kg personal review` and `kg personal forget <id>`, so an unwanted entry is one command away
   from gone.

The tool description also tells the agent to call it only on explicit request, and never for
findings about the current codebase — those belong in `add_observation` on the project graph.
That is guidance rather than enforcement, which is why the four mechanical guards exist.

### Reviewing and undoing

```bash
kg personal review                  # recent entries, newest first, with who recorded each
kg personal review --agent-only     # only what agents wrote
kg personal review --limit 50
kg personal forget <entity-id>      # delete an entry and its observations
```

```
2026-08-18 12:45  kafka-retention (decision) — recorded by agent
  id: edc2e89c-db30-45ca-a677-2001a1cf7c08
  - 7-day retention on the events topic: replay window beats storage cost.
  - [VIA:mcp] [REQUESTED] "remember this decision in my personal knowledge"
```

Reviewing periodically is worth the habit: the personal store is never re-indexed, so
nothing else will correct a wrong entry.

### What the MCP tools do not do

Project search still ignores personal knowledge — `search_knowledge` and
`get_preflight_context` federate the active scope's layers only, exactly as before. Personal
knowledge is reached by asking for it, via the dedicated tool or `kg search --with-personal`.
Whether personal knowledge should be folded into every project search by default remains an
open product question, and would need a `Source` field on search results first.

## Capturing conversations

The [capture-conversation skill](../skills/capture-conversation/SKILL.md) automates the
common case: read a Slack thread through the native Slack connector, distil the decisions,
and write them to the personal store. Install it from [skills/](../skills/README.md).

Worth knowing before you capture: summarise around customer PII, credentials, and
commercially sensitive detail rather than copying it into the graph. A knowledge store is
long-lived and gets read by future agent sessions.

## What belongs here, and what does not

| Put it in the personal store | Put it in the project graph |
|------------------------------|-----------------------------|
| Why a decision was made, across repos | Structure of this codebase |
| Conversations, meetings, calls | Findings about specific files or functions |
| Constraints you keep rediscovering | Anything `kg index` can derive |
| People and ownership | Investigation notes tied to this repo |

Rule of thumb: if it would still be true and useful in a different repository, it belongs in
the personal store.

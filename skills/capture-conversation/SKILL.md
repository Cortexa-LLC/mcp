---
name: capture-conversation
description: Use when the user wants to save a Slack thread, meeting, call, or discussion into their personal knowledge store — "capture this thread", "save this conversation to my KG", "record what we decided", "remember this discussion". Reads the source via the Slack connector, distils it, and writes it to the user-global kg store so future sessions in any repo can find it.
---

# Capture a conversation into the personal knowledge store

Turn a discussion into durable, searchable knowledge. The value is not a transcript — it is
the handful of decisions, constraints, and open questions that a future session would
otherwise have to rediscover or ask about again.

## Prerequisites

The `kg` binary must be on `PATH` (`kg version`). The personal store lives at
`$KG_HOME`, or `~/.kg/knowledge.db` by default:

```bash
kg personal path                       # where it is
kg personal init                       # create it (safe to re-run)
```

Reading Slack needs a connected Slack integration in this session — the native connector's
thread/channel-reading tools. If no Slack tool is available, ask the user to paste the
conversation instead and continue from step 2; never invent thread content.

## Steps

### 1. Get the source material

For a Slack permalink (`https://<workspace>.slack.com/archives/<CHANNEL>/p<TS>`), split the
`p<TS>` into a timestamp (`p1234567890123456` → `1234567890.123456`) and read the **whole
thread**, not just the linked message — decisions usually land in replies.

If the user refers vaguely to "that thread", ask which channel and roughly when, then search
rather than guessing. Read the full thread before writing anything.

For meetings or calls with no Slack source, work from the notes or transcript the user
provides.

### 2. Distil it

Write a summary that stands alone months later. Someone reading it without the thread should
understand what was decided and why. Include:

- **What was decided** and the reasoning — the "why" is the part that decays fastest.
- **Constraints and caveats** discovered along the way.
- **Open questions** and who owns them.
- **Who was involved**, by name only where it matters for follow-up.

Leave out pleasantries, scheduling chatter, and duplicated back-and-forth. Aim for a short
paragraph plus a few tagged observations, not a transcript.

**Before writing, check what you are about to store.** Conversations often contain customer
PII, credentials, or commercially sensitive detail. Summarise around it — refer to "the
seller in ticket X" rather than copying names, emails, or order data, and never copy tokens
or keys into the graph. If the material is sensitive enough that storing a summary is
questionable, ask the user first. Follow your organisation's data-handling policy; when in
doubt, store less.

### 3. Write it to the personal store

Create the conversation entity. Keep the name a short kebab-case slug — it is what the user
will recognise in search results:

```bash
kg add entity --personal \
  --name "kafka-retention-thread" \
  --type conversation \
  --summary "$SUMMARY"
```

Put the summary in a shell variable via a quoted heredoc first, so quotes, backticks, and
newlines in the text cannot break the command or be interpreted by the shell:

```bash
SUMMARY="$(cat <<'EOF'
Discussed Kafka retention for the events topic. Settled on 7 days: long enough to replay a
full weekend incident, short enough to stay inside the storage budget. Open question on
whether the compacted topics need the same window — Priya owns that.
EOF
)"
```

The command prints the new entity's ID. Capture it, then attach the specifics as
observations, each with a category prefix so later searches can filter by intent:

```bash
kg add observation --personal "<entity-id>" "[DECISION] 7-day retention on events topic — replay window beats storage cost"
kg add observation --personal "<entity-id>" "[CAVEAT] Compacted topics may need a different window; unresolved"
kg add observation --personal "<entity-id>" "[ACTION] Priya to confirm compacted-topic retention"
```

Use `[DECISION]`, `[CAVEAT]`, `[ACTION]`, `[INVESTIGATION]` — the same prefixes the project
KG uses, so one search convention works across both.

Optionally connect it to a topic so related conversations cluster:

```bash
kg add entity --personal --name "kafka" --type topic          # reuse if it exists
kg link --personal "<conversation-id>" --rel RELATES_TO "<topic-id>"
```

Only `RELATES_TO`, `DOCUMENTS`, `DEPENDS_ON`, `SUPERSEDES`, `CAUSED_BY`, `FIXES`,
`IMPLEMENTS`, `TESTS`, `CALLS`, `IMPORTS`, `CONTAINS`, and `BELONGS_TO` are accepted —
anything else is rejected on write. `RELATES_TO` is the right default here, and
`SUPERSEDES` is useful when a new decision replaces an older one: link the new conversation
to the one it overrides.

### 4. Verify and report back

```bash
kg search "retention" --personal
```

Confirm the entity comes back, then tell the user what was stored, where, and the entity ID.
Show the summary you wrote so they can correct it — a wrong summary is worse than none,
because it will be trusted later.

## Reading it back later

```bash
kg search "<query>" --personal          # personal store only
kg search "<query>" --with-personal     # this project's graph plus personal knowledge
kg show "<entity-id>" --personal        # one conversation with its observations
kg stats --personal                     # size of the personal store
```

`--with-personal` is the payoff: working in any repo, a search surfaces both the code
knowledge and the conversation where the relevant decision was made. Personal entries rank
below project ones, so they add context without crowding out code results.

## If personal writes are enabled over MCP

When the `kg` server runs with `--personal-writes` (or `KG_PERSONAL_WRITES=1`), an
`add_personal_knowledge` tool is available and replaces step 3's shell commands for the
simple case: pass `title`, `content`, an optional `type`, and `user_request` quoting what the
user actually asked. It records provenance automatically and tells you the undo command.

Prefer the CLI when the entry needs several observations or topic links — the tool records
one entry with one body. Either way, never record without an explicit request from the user.

## Notes

- The personal store is **not** a project graph. Never run `kg index` against `$KG_HOME` —
  it holds hand-written knowledge, not indexed source.
- Capture at the end of a discussion, while the reasoning is fresh. A thread captured a week
  later loses exactly the "why" that made it worth storing.
- One conversation per entity. Resist bundling a day's threads into a single entry; they
  will not surface cleanly in search.

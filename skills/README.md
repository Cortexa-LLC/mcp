# Skills

Claude Code skills that build on the servers in this repo. A skill is a set of
instructions Claude loads on demand — no binary, no MCP registration.

| Skill | What it does |
|-------|--------------|
| [capture-conversation](capture-conversation/SKILL.md) | Save a Slack thread, meeting, or discussion into the personal knowledge store with `kg add entity --personal` |

## Install

Skills are discovered from `~/.claude/skills/`. Symlink so repo updates apply without
re-copying:

```bash
mkdir -p ~/.claude/skills
ln -s "$PWD/skills/capture-conversation" ~/.claude/skills/capture-conversation
```

Copy instead if you would rather pin a version:

```bash
cp -R skills/capture-conversation ~/.claude/skills/
```

Then start a new session — Claude picks the skill up at startup. Invoke it by asking
("capture this thread into my knowledge store") or with `/capture-conversation`.

## Requirements

- `kg` on `PATH` (`cd src/kg && make install`), and a personal store (`kg personal init`).
- A connected Slack integration in the session for reading threads. Without one the skill
  still works from pasted text.

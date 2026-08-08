---
name: ostraka
version: "1"
description: >
  Protocol for structured agent↔user communication via ostraka.
  Load this skill at session start when a `.ostraka/` directory exists in
  the project root, or when the user mentions ostraka, asks, inbox, or handoff.
---

# ostraka agent protocol

ostraka is a structured side-channel between you and the user. Three channels:
- **inbox** — tasks the user has assigned to you
- **asks** — questions/decisions/checklists you need from the user
- **handoff** — your running session journal (you append; user reads)

All access is through the `ostraka` CLI. Never read or write `.ostraka/` files directly.

## Which binary

When working *on* the ostraka repo itself, build and use the local tree rather than
any `ostraka` already on PATH — otherwise you exercise a stale build instead of your
own changes:

```bash
go build -o /tmp/ostraka ./cmd/ostraka && OSTRAKA=/tmp/ostraka
```

Use `$OSTRAKA` in place of `ostraka` for every command below. In any other project,
use `ostraka` from PATH.

## Session start

```bash
# Read your assignments
ostraka item list --channel inbox --status active --json

# Check for any asks that the user has answered since last session
ostraka item list --channel asks --status pending-agent --json

# Open a session handoff item (keep this ID for the rest of the session)
HANDOFF=$(ostraka item add --channel handoff \
  --title "Session open" \
  --body "What this session is picking up.")
```

## Raising an ask

When you have a question, decision, or test checklist for the user:

```bash
ID=$(ostraka item add --channel asks \
  --title "One-line summary of the question" \
  --body "The question in full, with the context needed to answer it.")
ostraka item status "$ID" pending-user
```

The user answers via the TUI. Poll `--status pending-agent` to find answered asks at the start of your next turn.

## Adding a turn to an existing thread

```bash
ostraka item turn <id> --actor agent "Your response"
```

An agent turn hands the item back automatically: `active` and `pending-agent`
both become `pending-user`, so an answered item stops showing up in the
pending-agent queue. Parked statuses (`backlog`, `done`, `archived`) are left
alone. You do not need to set the status yourself after replying.

If you resolve an ask inline during chat, record it and close it:

```bash
ostraka item turn <id> --actor agent "Resolved: went with X because..."
ostraka item status <id> done
```

## Forking a thread

If you want to spin off a sub-question from an existing item, quote the relevant context in the new item's body and link back via `--parent`:

```bash
CHILD=$(ostraka item add --channel asks --parent <parent-id> \
  --title "One-line summary of the sub-question" \
  --body "> [quote of relevant excerpt]

Sub-question here")
ostraka item status "$CHILD" pending-user
```

## Session end

```bash
ostraka item turn "$HANDOFF" --actor agent "Summary of work this session: ..."
ostraka item status "$HANDOFF" done
```

## CLI quick reference

```
ostraka item list [--channel inbox|asks|handoff] [--status <s>] [--json]
ostraka item show <id> [--json]
ostraka item add --channel <c> --title <title> --body <body> [--parent <id>] [--status <s>]
ostraka item turn <id> --actor agent|user <content>
ostraka item status <id> backlog|active|pending-user|pending-agent|done|archived
ostraka item rm <id> [-y]
```

## Notes

- `ostraka item add` prints the new item's ID — capture it if you need to reference the item later.
- `--title` and `--body` are both required and are different things. The title is
  a single line and is what list views render, so a title that runs to paragraphs
  crowds every other item off the screen; a multi-line title is rejected. Put the
  detail in `--body`, which has no length limit.
- Statuses `done` and `archived` move the file to `.ostraka/ARCHIVE/`; other status changes are in-place.
- If `.ostraka/` does not exist, run `ostraka init` first.

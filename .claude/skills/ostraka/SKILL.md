---
name: ostraka
description: Protocol for structured agent-user communication through Ostraka. Use when a project has a `.ostraka/` directory or the user mentions Ostraka, asks, inbox, handoff, or an Ostraka item ID.
compatibility: Requires the Ostraka CLI; use the repository-local Go command when working in the Ostraka repository.
metadata:
  version: "1"
---

# Ostraka protocol

ostraka is a structured side-channel between you and the user. Three channels:
- **inbox** — tasks the user has assigned to you
- **asks** — questions/decisions/checklists you need from the user
- **handoff** — your running session journal (you append; user reads)

All access is through the `ostraka` CLI. Never read or write `.ostraka/` files directly.

## Choose the CLI command

Use `<cli>` below as a placeholder for the appropriate command. When working in
the Ostraka repository (`cmd/ostraka` and `go.mod` are in the project root), use:

```bash
go run ./cmd/ostraka <arguments>
```

Otherwise, use `ostraka <arguments>` from `PATH`. Do not use a stale globally
installed binary when working on the Ostraka repository.

## Session start

```bash
# Read your assignments
<cli> item list --channel inbox --status active --json

# Check for any asks that the user has answered since last session
<cli> item list --channel asks --status pending-agent --json

# Open a session handoff item (keep this ID for the rest of the session)
HANDOFF=$(<cli> item add --channel handoff \
  --title "Session open" \
  --body "What this session is picking up.")
```

## Raising an ask

When you have a question, decision, or test checklist for the user:

```bash
ID=$(<cli> item add --channel asks \
  --title "One-line summary of the question" \
  --body "The question in full, with the context needed to answer it.")
<cli> item status "$ID" pending-user
```

The user answers via the TUI. Poll `--status pending-agent` to find answered asks at the start of your next turn.

## Adding a turn to an existing thread

```bash
<cli> item turn <id> --actor agent "Your response"
```

An agent turn hands the item back automatically: `active`, `pending-agent` and
`agent-acknowledged` all become `pending-user`, so an answered item stops
showing up in the pending-agent queue. Parked statuses (`backlog`, `done`, `archived`) are left
alone. You do not need to set the status yourself after replying.

### Dispatch lifecycle

When a dispatch prompt embeds the latest user turn, address that turn directly.
Otherwise, read the item with `<cli> item show <id> --json` before replying.

Treat the agent item turn as the final action of a dispatch: it moves the item
to `pending-user`, ends the live dispatch, and replaces the live trace with
the durable response. Complete all work, validation, and normal progress
reporting before posting it.

Do not post progress turns while the Ostraka TUI shows the live harness trace.
Use the final item turn for the substantive summary. Post an interim turn only
when there is no visible live trace and the user would otherwise experience a
long silent interval; that turn ends the current dispatch.

If you resolve an ask inline during chat, record it and close it:

```bash
<cli> item turn <id> --actor agent "Resolved: went with X because..."
<cli> item status <id> done
```

## Forking a thread

If you want to spin off a sub-question from an existing item, quote the relevant context in the new item's body and link back via `--parent`:

```bash
CHILD=$(<cli> item add --channel asks --parent <parent-id> \
  --title "One-line summary of the sub-question" \
  --body "> [quote of relevant excerpt]

Sub-question here")
<cli> item status "$CHILD" pending-user
```

## Session end

```bash
<cli> item turn "$HANDOFF" --actor agent "Summary of work this session: ..."
<cli> item status "$HANDOFF" done
```

## CLI quick reference

```
<cli> item list [--channel inbox|asks|handoff] [--status <s>] [--json]
<cli> item show <id> [--json]
<cli> item add --channel <c> --title <title> --body <body> [--parent <id>] [--status <s>]
<cli> item turn <id> --actor agent|user <content>
<cli> item status <id> backlog|active|pending-user|pending-agent|agent-acknowledged|done|archived
<cli> item rm <id> [-y]
```

## Notes

- `ostraka item add` prints the new item's ID — capture it if you need to reference the item later.
- `--title` and `--body` are both required and are different things. The title is
  a single line and is what list views render, so a title that runs to paragraphs
  crowds every other item off the screen; a multi-line title is rejected. Put the
  detail in `--body`, which has no length limit.
- Statuses `done` and `archived` move the file to `.ostraka/ARCHIVE/`; other status changes are in-place.
- `agent-acknowledged` is set by the supervisor while a dispatch is running and
  cleared when it ends. It is a progress indicator, not something to set by hand.
  Note that the item you were dispatched for is in this status, not
  `pending-agent`, for the whole time you are running — so a `--status
  pending-agent` search will not find it. The dispatch prompt names the item id
  directly; use that.
- If `.ostraka/` does not exist, run `<cli> init` first.

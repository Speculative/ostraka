# ostraka

TUI and file-backed `.notes/` protocol for coding agent sessions — a structured
inbox so multi-part work and cross-session pending items don't get lost in chat
scroll.

The TUI dispatches turns through Claude Code by default. Each item keeps its
own provider session and resume cursor, so unrelated work never shares agent
context. Press `S` on an item to start a fresh Claude or Codex session for that
item. The supervisor also starts a fresh session automatically on the next
dispatch after the provider's cache-reuse window expires, preserving the
selected model and effort while rebuilding context from the item. On a Claude
subscription that window is one hour; Codex's App Server uses the GPT-5.6
30-minute cache-reuse window. Both are recommendations: providers may retain
cache entries longer.

Use `ostraka item reparent <item-id> <root-id>` (or `--parent <root-id>`) to
move an item under another live root. The `m` TUI action opens the same root
picker. An item with subthreads requires `--flatten-children`; that moves the
item and its direct children as siblings under the destination. Passing the
flag for an item without children is rejected.

Use `ostraka item rename <item-id> <new-title>` to change an item's label
without changing its body or conversation.

In the composer, type `@` to find and insert an item reference. In item
conversations, references show `@[title]` with the target's current status marker and a
dedicated lavender-on-plum background that marks the full title boundary.
Stored text keeps `@item-id`; unknown IDs and references inside code or links
remain literal.

Roots can be assigned one optional lowercase group slug. Use
`ostraka item add --group <group>`, `ostraka item group <item-id> <group|none>`,
and `ostraka item list --group <group|none>` to organize and filter complete
item families. In the TUI, select an item and press `g` to edit its group in
the bottom composer; existing group names appear in a floating autocomplete
and typing filters them. The input starts empty; use up/down to choose a
suggestion and Tab to complete it. Enter or `ctrl+s` applies the text as typed,
and `none`/an empty value clears the group.

Run `ostraka preamble` when an agent needs the static orientation and final
reply guidance. Normal dispatched sessions receive the same guidance directly
in their initial prompt.

## Browser workspace

Run `ostraka web` and open http://localhost:8090 for the responsive mobile and
desktop browser interface. It shares project files with the CLI and TUI and
streams conversations and live agent progress. To run agents without the TUI,
close the TUI and use `ostraka web --supervise`.
See [docs/web.md](docs/web.md) for preview setup, operations and limitations.

## Commit messages

Run `./scripts/install-git-hooks.sh` to install the repository's commit-message
hook for this checkout. It checks Conventional Commit subjects (maximum 50
characters), the blank line after the subject, a nonempty body,
and a 72-character limit for each body line. When `OSTRAKA_AGENT_ITEM_ID` is
set for an agent dispatch, it also requires a final `Co-Authored-By` trailer.

## Project context

`ostraka project instructions` holds user-owned rules for how agents work in
this project. An explicit current user request takes precedence over an older
project instruction for that item; Ostraka's required item workflow still
applies. Agents edit the standing instructions when the user explicitly asks.
If a user states a standing preference without asking to edit instructions,
agents confirm whether to add it there. `ostraka project brief` holds
agent-curated, stable facts and enduring design choices about the project. It
guides work but does not set agent behavior. If a brief entry conflicts with
instructions, agents follow the instructions, correct the brief, and mention
the correction in the item reply. Current work and decisions belong on items.

Optional project workflows can be adopted through user-owned project
instructions. Run `ostraka project instructions pack list` to see available
packs, `pack show <name>` to read one, and `pack add <name>` to append it to
the current project's instructions. Add preserves existing instructions,
skips an already installed pack, and reports edited marked content for manual
reconciliation. The [numbered plan workflow
pack](internal/instructionpacks/numbered-plan.md) is for projects that use
numbered execution items. Packs do not change Ostraka's default prompt.

## Installation

Once a release is published, install the CLI with
`go install github.com/Speculative/ostraka/cmd/ostraka@latest`, then run
`ostraka init` in each project that should have an Ostraka inbox, or launch
`ostraka tui` and accept its offer to initialise the current directory when no
project exists in the current directory or an ancestor. The supervisor supplies
item context directly and `ostraka preamble` provides the static agent guidance,
so no separate Ostraka skill installation is required.
See [docs/deploying.md](docs/deploying.md) for release, local checkout, and
Carthage setup details.

Goal mode keeps one item working until the agent explicitly ends its loop.
Use Shift+Tab to select GOAL, then send its initiating user turn. See
[goal mode](docs/goals.md) for queueing, child delegation, and controls.

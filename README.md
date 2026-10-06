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

Roots can be assigned one optional lowercase group slug. Use
`ostraka item add --group <group>`, `ostraka item group <item-id> <group|none>`,
and `ostraka item list --group <group|none>` to organize and filter complete
item families. In the TUI, select an item and press `g` to edit its group in
the bottom composer; existing group names appear in a floating autocomplete
and typing filters them. Press Enter to select a suggestion, `ctrl+s` to apply,
or use `none`/an empty value to clear it.

Run `ostraka preamble` when an agent needs the static orientation and final
reply guidance. Normal dispatched sessions receive the same guidance directly
in their initial prompt.

Optional project workflows can be adopted through user-owned project
instructions. The [numbered plan workflow
pack](internal/instructionpacks/numbered-plan.md) is a
paste-ready example for projects that use numbered execution items. Merge it
with existing instructions rather than replacing them; it does not change
Ostraka's default prompt.

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

# ostraka

TUI and file-backed `.notes/` protocol for coding agent sessions — a structured
inbox so multi-part work and cross-session pending items don't get lost in chat
scroll.

The TUI dispatches turns through Claude Code by default. Each item keeps its
own provider session and resume cursor, so unrelated work never shares agent
context. Press `S` on an item to start a fresh Claude or Codex session for that
item. On a Claude subscription, the session picker recommends a fresh context
after one hour, which is Claude Code's documented cache TTL. Codex's App
Server uses the GPT-5.6 30-minute cache-reuse window. Both are recommendations:
providers may retain cache entries longer.

Run `ostraka preamble` when an agent needs the static orientation and final
reply guidance. Normal dispatched sessions receive the same guidance directly
in their initial prompt.

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

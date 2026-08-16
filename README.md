# ostraka

TUI and file-backed `.notes/` protocol for coding agent sessions — structured
inbox, asks, and handoff channels so multi-part decisions, test checklists,
and cross-session pending items don't get lost in chat scroll.

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

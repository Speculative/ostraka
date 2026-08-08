# ostraka

TUI and file-backed `.notes/` protocol for coding agent sessions — structured
inbox, asks, and handoff channels so multi-part decisions, test checklists,
and cross-session pending items don't get lost in chat scroll.

The TUI dispatches turns through Claude Code by default. Press `S` to start a
fresh Claude or Codex session; Ostraka persists the selected provider with its
resume cursor, so sessions are never resumed by the wrong harness.

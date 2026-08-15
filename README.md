# ostraka

TUI and file-backed `.notes/` protocol for coding agent sessions — structured
inbox, asks, and handoff channels so multi-part decisions, test checklists,
and cross-session pending items don't get lost in chat scroll.

The TUI dispatches turns through Claude Code by default. Press `S` to start a
fresh Claude or Codex session; Ostraka persists the selected provider with its
resume cursor, so sessions are never resumed by the wrong harness.

## Agent skill

The portable Ostraka Agent Skill is exported at `.agents/skills/ostraka/`.
Consumers can copy or symlink that directory into their agent's skill discovery
location; Ostraka does not install or maintain agent-specific copies.

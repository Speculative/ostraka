# Turn editor

The main turn editor always uses Vim-style modal editing. It opens in insert
mode. Settings support is deferred; project document editors, new-item forms,
and review composers retain their existing editing behavior.

The footer identifies INSERT, NORMAL, VISUAL, or VISUAL LINE. Normal mode uses
a blinking block cursor. Insert mode uses a blinking underline cursor, keeping
the character beneath it visible. These are drawn cursors: the current Bubble
Tea renderer hides the hardware cursor and does not expose a vertical bar.

## Application bindings

Ctrl+S submits in either editing mode. Shift+Tab cycles the item's CHAT, CODE,
or GOAL execution mode without changing the editor mode. PgUp/PgDn scroll the
reading pane without moving the draft cursor. Ctrl+N opens a related item;
Ctrl+C discards the turn draft.

Esc dismisses an open mention picker first. In insert mode, the next Esc
enters normal mode. Esc in normal mode saves the draft and leaves the editor.
Esc with a visual selection cancels that selection and stays in normal mode.
Enter inserts a newline in insert mode; it selects a mention when the picker
is open. Normal-mode letters belong to the editor and cannot trigger item or
navigation shortcuts. Unbound commands have no effect.

Terminal bracketed paste inserts literal text in either editing mode; its
contents cannot submit a turn or execute commands.

## Supported commands

| Commands | Behavior |
| --- | --- |
| `i`, `a`, `I`, `A` | Insert at/beyond the cursor, at the first nonblank, or at line end. |
| `o`, `O` | Open a line below/above and enter insert mode. |
| `h/j/k/l`, arrows | Move by character or logical line; vertical movements retain the desired column across shorter lines. |
| `gj/gk` | Move through displayed, wrapped rows. |
| `w/b/e`, `W/B/E` | Move to the next word, previous word start, or word end. Uppercase motions treat non-whitespace as one word. |
| `0`, `^`, `$` | Line start, first nonblank, or line end. |
| `gg`, `G` | First or last line; with a count, go to that line. |
| `f/F/t/T` | Find a character forward/backward, or stop just before/after it, within the current line. |
| `;`, `,` | Repeat the last character find in its original/opposite direction. |
| `%` | Match parentheses, brackets, or braces. Search forward on the current line for a bracket; its match may be on another line. Matching is lexical, including brackets in quoted text. |
| `d/c/y` + motion | Delete, change, or yank the range; changing enters insert mode. |
| `dd/cc/yy` | Delete, change, or yank whole lines. |
| `iw/aw` after an operator | Inner word or word with adjacent spaces. |
| `D/C` | Delete/change through line end. |
| `x/X`, `r`, `s/S` | Delete at/before the cursor, replace a character, or substitute characters/a whole line. |
| `p/P` | Put the editor-local unnamed register after/before the cursor; linewise contents put whole lines. |
| `v/V` | Select characters/lines, move to extend the selection, then use `d/c/y`. |
| `u`, Ctrl+R | Undo/redo. An insert session, including the deletion for a change or initial newline for an open command, is one undo unit. |
| `.` | Repeat the last change, including inserted text or a completed mention. |

Numeric counts compose with commands: `3w`, `d2w`, `2dd`, and `3i` followed by
text and Esc. A count on `.` replaces the prior command's count. Counts are
bounded at 10,000. Character replacements cannot cross line boundaries.

The unnamed register persists while the TUI runs. Undo/redo and repeat history
belong to the current editing session and reset when the editor is reopened;
undo retains the last 100 changes. Draft text still uses the existing durable
draft checkpoints.

This is a bounded Vim subset. Search, Ex commands, named/system clipboard
registers, macros, visual block mode, additional text objects, surround, and
comment toggles are not implemented.

# Review storage and CLI

Reviews are active discussions attached to one Ostraka item. They do not save
diff snapshots. A thread's current anchor stores a file, diff side, inclusive
line range, and excerpt; each message keeps the excerpt that accompanied it.
Review objects live in `.ostraka/REVIEWS/` and use stable, monotonically
allocated thread numbers within a review.

## Store API

The TUI should use the store API so its drafts and agent CLI share one
cross-process review lock:

- `CreateReview(itemID)` creates a review routed to one item.
- `ListReviews()` and `GetReview(reviewID)` read active reviews and their
  threads.
- `CreateReviewThread(reviewID, anchor, actor, content)` adds a thread and its
  first message. User messages are drafts; agent messages await the final item
  turn.
- `AddReviewReply(reviewID, threadNumber, actor, content)` adds a user draft
  or an agent reply. It does not mutate item turns or status.
- `UpdateReviewDraft` and `DeleteReviewDraft` edit or discard unsent user
  messages without changing the destination item.
- `AttachReviewThread` and `DetachReviewThread` update the current anchor and
  add an agent attachment note for the final item turn.
- `SubmitReviewBatch(reviewID)` formats every currently unsent user message,
  including its saved excerpt and thread number, into one user item turn.
  Agent CLI reminders appear in dispatch prompts, outside the saved turn.
- `RequestReviewCommit(reviewID)` posts user commit approval and retains the
  review; unsent drafts must first be submitted or discarded.
- `CompleteReviewCommit(reviewID, commit)` verifies a new commit at HEAD and a
  clean working tree, then marks the review for cleanup with the final turn.
- `FinishReview(reviewID)` is the legacy store cleanup API; it cannot bypass
  the commit workflow once approval has been requested.
- `AddAgentTurnWithReview(itemID, content)` appends the final agent turn and
  includes unrecorded review replies, excerpts, and attachment notes once.
- `ReviewThreadNeedsReply(thread)` reports whether a submitted user comment is
  still awaiting an agent reply.

After `SubmitReviewBatch`, the TUI should follow its normal user-turn flow:
advance a dispatchable item to pending-agent and enqueue it. A backlog item
stays parked. Standalone CLI submissions are observed by a running TUI through
the existing external-turn watcher.

## TUI

Open **Review changes** with `4`. Use `j/k` to choose a file, focus the diff
with `l` to start at its first changed line, and move by line. Files follow a directory walk in sibling name
order, with tracked and untracked files interleaved. Source code uses Chroma
syntax colors in both layouts; unknown languages retain plain text. Old and
new versions are tokenized separately using full-file context. File status dots are yellow for modified, green
for added/untracked, and red for deleted files. Press `/` to search filenames
from the file list or code from the diff pane; Enter finds the next match,
`n`/`N` repeat forward/backward, and Esc cancels search. Each `/` starts
with an empty query. Search wraps within
the file list or the selected file.

Press `c` to toggle normal diff context versus full-file context, and `d` to
toggle unified versus side-by-side layout. The side-by-side layout shows HEAD
on the left and working-tree contents on the right, pairing removed/added
lines within replacement blocks. Both layouts support code selection and
questions; source anchors survive layout/context changes when still visible.
Side-by-side navigation defaults to the new side; `s` explicitly switches
sides. Movement, hunk jumps, and code search skip lines absent from the chosen
side. Switching sides prefers the corresponding replacement row, then the
nearest available source line (forward before backward). A deleted file has
no new-side cursor; press `s` to select its old code.
The header shows the active context, layout, and attachment side. Press `v`, move to the other end of the range, and
press Enter to finish; `s` switches old/new side. Press `a` to ask about that
range, or leave the range clear for an item-wide question. The first question
opens the `@` destination picker; later questions reuse that destination. Use
`@` to choose a different item. In the composer, `Ctrl+S` saves a draft;
repeat `a` to add more questions. The `s` control chooses which source version
the selected excerpt comes from: `old` for HEAD or `new` for working-tree code. `b` submits all unsent questions for the
selected review as one item turn.

Press `f` for a full-screen review without the file list. The same movement,
selection, search, and question controls work there. The destination picker
and composer remain full width; Ctrl+S saves the question and returns to the
full-screen review. `f` or `h` returns to the normal layout. Esc clears a
selection first, then exits full screen.

**Unsubmitted saved questions survive TUI restarts.** Ctrl+S writes them to
`.ostraka/REVIEWS/<review-id>.json` with their code excerpts and destination,
before any item turn is posted. Reopen with Tab to inspect, reply, or submit them.
Text still being typed in the composer without Ctrl+S is not persisted.

Code attached to a verified thread has a shaded background and a `[#N]`
marker. With code focused, press `l` again to open its thread, selected and
scrolled into view. Stale attachments remain in the thread reader without
marking unrelated code. Overlapping markers show destination IDs to
distinguish review-scoped thread numbers; `l` opens the first match.

Press Tab to switch between code and threads. The thread reader fills the
width without an item list and immediately focuses its first thread. It
shows all threads in the current review, with a distinct cyan title band
and a whole-thread highlight for the selected one. Unsubmitted comments show `[unsent]` beside their author and timestamp;
comments without a code attachment have no special discussion label. `j/k`
select the next/previous thread and bring it into view. At the ends, they
continue into the adjacent nonempty review when multiple destinations have
active reviews. The reader heading identifies the current destination.
`t` (or Enter) composes a reply inside the selected thread; `a` starts a new comment thread. `h` returns to
the selected thread’s file and source location. Ctrl+S saves replies for the same batch flow. There is no edit-draft
control; `x` discards the latest unsent message in the selected thread.
`F` submits “LGTM, please commit” to the current destination and queues the
agent. It works from the file list, code, and thread views. If there is only
one active review, it is selected automatically; with multiple reviews and no
current destination, Finish opens the thread reader to choose before sending.
With no active review, Finish prompts for a destination session; selecting it
immediately sends “LGTM, please commit” and queues the agent without creating
a review.
Destination suggestions show live items first, ordered by most recent turn
(or creation time for items without turns), so Tab usually selects the session
just worked on. Typing an ID or title narrows the list, with closer matches
ranked first.
After sending, it opens the destination item at the newest content so the
submitted commit prompt is visible immediately, including from full-screen
review. It leaves the review and its threads intact. Chat destinations switch
to CODE for this request; backlog destinations are queued too. Archived or
terminal destinations must be reopened first. Unsent drafts must be submitted
or discarded before approval.

After committing successfully, the agent runs `ostraka review finish
<review-id> --commit <commit-hash>`. Completion requires a new commit at HEAD
(descending from HEAD at approval) and a clean working tree. It marks the
review for deletion by the final agent item turn, after pending replies are
recorded. A failed check or commit leaves the review open for intervention;
an empty diff alone never closes it. Recorded discussion remains in item turns.
Review objects refresh when the project watcher sees
external changes. Agent explanations should be added only when the user asks.
Use `r` to refresh the working-tree diff. Stored attachments whose excerpt
cannot be verified at its range in the loaded diff are flagged for agent
relocation or detachment; browsing does not automatically invoke an agent.

The implementation acquires the review lock before the item lock whenever an
operation touches both. User-batch IDs and per-message agent receipts are
written into item turns, allowing restart recovery if a process stops between
the item write and review-object update.

## CLI

```text
ostraka review list [item-id]
ostraka review create <item-id>
ostraka review show <review-id>
ostraka review thread list <review-id>
ostraka review thread show <review-id> <thread-number>
ostraka review thread create <review-id> --content-stdin [anchor flags]
ostraka review reply <review-id> <thread-number> --content-stdin
ostraka review attach <review-id> <thread-number> --file ... --side old|new --start N --end N --excerpt ...
ostraka review detach <review-id> <thread-number>
ostraka review submit <review-id>
ostraka review finish <review-id>                    # user: request commit
ostraka review finish <review-id> --commit <hash>    # agent: verify success
```

An agent dispatch can only inspect and mutate reviews routed to its marked
item. Review replies do not post item turns; one final `ostraka item turn`
automatically records all outstanding agent review messages before cleanup.
Commit approval retains all review discussion. Successful completion records
pending agent replies in the final item turn before removing the review.

The second row of each file shows the attached thread count alongside its
modified/untracked/deleted status, for example `modified [1]`.
A cyan `[N+]` marks agent comments newer than the last submitted user message
in an attached thread. Saved drafts do not clear this indicator. Detached
threads are not counted; stale attachments remain counted but are not painted
on code. Returning with `h` validates the attachment before positioning the
code cursor and opens full-file context when needed. Unavailable or changed
attachments show a warning rather than selecting unrelated code.

Review excerpts in submitted turns and the thread reader use language-tagged
code fences inferred from filenames, with plain text for unknown languages.
The Markdown renderer highlights tagged blocks using its existing Chroma support.

Markdown prose displays em/en dashes as ASCII `--`/`-` to avoid overlapping
glyphs in terminal fonts. Code spans, fenced code, link destinations, and
stored messages retain their original characters.

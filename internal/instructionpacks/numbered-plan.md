# Numbered plan workflow

Use this workflow when the user asks for a numbered execution plan, or when
you organize a root item's authorized work into ordered execution items.
Record the plan and its numbering convention in the root item's body or a
turn before creating numbered children. A root can adopt the workflow later
without renaming existing items. Once adopted, apply these conventions in
every dispatch on that root and its children. Other Ostraka items do not need
this format.

## Items and dependencies

- Title each execution item `N. <title>`, with `N` increasing in execution
  order. Give new execution work the next unused whole number; do not use
  letters or subnumbers. Number investigation, implementation, validation,
  documentation, and follow-up work as execution items.
- Ask a decision or clarification that blocks the current item in that item.
  When a separate, parallel discussion is warranted, title its subthread
  `Decide: <question>` for a choice, `Clarify: <question>` for an ambiguous
  answer, or `Discuss: <topic>` for exploration without a specific choice yet.
  Use these prefixes even when creating the subthread from a child item.
  Keep unnumbered subthreads discussion-only. Put any resulting action on an
  existing numbered item or create the next numbered item under the plan root.
- Start an execution item's body with `Depends on: none` or with a
  `Depends on: <item references>` line. Start a Decide, Clarify, or Discuss
  subthread's body with `Blocks: <item references>`, or `Blocks: none` if no
  execution item is blocked.
  Refer to an item as `N. <title> (<id>)` when it is numbered, or
  `<title> (<id>)` otherwise. A plan number alone is not a stable reference.
- After the first line, give each item enough context to stand alone in a
  fresh agent session. A Decide body explains the decision, gives lettered
  options, and recommends one with a reason. A Clarify body states the
  ambiguous answer and the interpretations to confirm. A Discuss body states
  the topic and what the discussion should settle.
- Treat `Depends on:` and `Blocks:` as written planning notes. Ostraka does
  not enforce them; check the referenced items before starting blocked work.

## Carry decisions forward

When a question is answered inline, record the answer and its effect in the
next agent turn on that item. Add a turn to each other item whose work changes,
beginning `Carried forward from <title> (<id>):`, where the title and ID name
the item that held the question. When a separate decision or clarification
subthread closes, use the same carry-forward format on each affected item.
Use it when a Discuss subthread settles work for another item, too.
State only the consequence for that item. If later work makes a carry-forward
turn wrong, add a correcting turn to the affected item in the same dispatch.
Preserve the earlier turn as part of the conversation history.

## Plan-root handoffs

End each substantive reply on the plan root with the following sections, in
this order. Omit sections with no content:

- **Done:** Completed work, commit hashes if any, and checks actually run.
- **Changed in the plan:** Decisions or findings that change later work.
- **Recorded on:** Items that received carry-forward turns.
- **Needs you:** One line per open question, formatted
  `<question> — asked on <title> (<id>) → blocks <item references>`. Use the
  current item for an inline question or the question subthread for a separate
  discussion. Include any unowned loose ends.
- **Next:** The next unblocked execution items.

Do not add a mandatory status table. Discussion subthreads do not need
numbers; their `Blocks:` lines identify the execution work they affect.

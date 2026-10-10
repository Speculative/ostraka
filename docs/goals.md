# Goal mode

On a live item, Shift+Tab cycles **CHAT → CODE → GOAL → CHAT**.
Selecting GOAL arms it; the next user turn supplies the goal and starts work.
Only one item can be armed or running in goal mode in a project. Its status
marker is blue, and the navigation bar shows the owner's ID and whether the
goal is armed or active, even when the item is hidden by the current view.

The initiating user turn is saved with the item and repeated in every fresh
or resumed dispatch, independently of conversation-history limits. Later
user turns steer the work without replacing that initiating text. The
supervisor continues dispatching the item when a provider turn finishes,
including when the agent forgets to post a reply. The agent can checkpoint
by posting the usual item reply; it does not need to request continuation.
Ostraka trusts the agent to decide when the goal is complete.

To end the loop, the agent must use the initiating item's ID:

```sh
ostraka goal end <item-id>
```

A child dispatch cannot end its parent's goal, even if it passes the parent's
ID. Ending a goal returns the item to CODE without archiving it. The user can
also end it by cycling out of GOAL in the TUI. Parking, archiving, deleting,
or moving a goal owner requires ending its goal first.

The CLI can arm a goal as well:

```sh
ostraka goal start <item-id>
ostraka item turn <item-id> --actor user --content-stdin
```

Goal mode holds new dispatches for unrelated items; already running turns
finish normally. A root goal permits dispatches for its direct children. A
child goal permits only its own item; newly created siblings do not join it.
Goal turns use the serialized code slot. Chat children can overlap, and each
item retains the usual rule that two dispatches cannot run at once.

For delegated work during a root goal, an agent creates a self-contained
child with `item add --parent <goal-id> --status active`. Ostraka queues that
child for one ordinary dispatch and gives queued child work a turn between
parent continuations. A child with `--status pending-user` stays idle as a
question for the user. User-authored child closures appear in subsequent
parent nudges through the existing activity mechanism. Children do not
inherit the parent's continuous loop.

`P` pauses/resumes new dispatches. Ctrl+C during an active goal pauses new
dispatches before interrupting active turns, preventing immediate relaunch.
A goal provider failure also pauses dispatches to avoid an error loop; inspect
the existing warning, recover, then resume with `P`. Pausing is process-local.
The durable goal survives supervisor shutdown and resumes when Ostraka next
runs, until it is explicitly ended. Scheduling works only while Ostraka runs.

This version implements continuous goal dispatches. Delayed wakeups, event
subscriptions, live context-pressure nudges, and automatic context-based
rotation remain future work. Existing session rotation based on cache age
continues to apply.

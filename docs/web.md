# Web workspace (v0)

Build once with `go build -o /tmp/ostraka ./cmd/ostraka`, then run from the
project directory:

```sh
/tmp/ostraka web
```

Open http://localhost:8090. The server attaches to the existing project's
files; a running TUI continues to own agent dispatch. Browser mutations use
Ostraka's store operations and appear in the TUI, and changes made by the
CLI/TUI stream back to the browser. No frontend build tools or CDN needed.

To run without the TUI, close it first and use:

```sh
/tmp/ostraka web --supervise
```

This owns the same supervisor lock as the TUI and enables pause and interrupt.
Model discovery and changes for the next turn also work in attach mode. Closing the server shuts down its agents.
Do not start a second supervisor for this project. Attach mode does not expose
pause/interrupt controls for another process's supervisor.

The UI is designed for desktop browsers and narrow screens. The bundled web
manifest supports a standalone desktop window in browsers that offer
"Install" or "Open as app"; it uses the same local server and project files.
There is no offline mode or bundled native runtime. Mobile and desktop browser
layouts are the supported targets; installing a desktop window is optional.

## Appearance

The workspace uses Jeff Tao's design system v0.1: ink on white paper, a single
blue accent, square panels and controls, and locally bundled Lato and JetBrains
Mono. Phone layouts use 44px controls and bottom navigation. Item Properties
start collapsed on phones and can be opened to change status, mode, group or
parent. See [Web design](web-design.md) for tokens, component mappings and
asset provenance.

## Container preview

Carthage configuration publishes host loopback port 8090. If the running
container predates this mapping, recreate it from the host with the project's
normal `carthage down` / `carthage up` workflow. The Dockerfile also adds
headless Chromium libraries for layout testing; run `carthage build` before
recreating the container to include those. Inside the container run:

```sh
go run ./cmd/ostraka web --listen 0.0.0.0:8090
```

The `--listen` flag is required inside Docker: the default `127.0.0.1`
bind accepts only connections originating inside the container. Docker forwards
host traffic to the container's network interface, so a loopback-only server
can produce Firefox's `NS_ERROR_NET_EMPTY_RESPONSE` even with the port mapping
in place. If that happens, stop the running web server and restart it with the
command above. The compose mapping still restricts host access to loopback.

Then open http://localhost:8090 on the host. Attach mode is suitable while the
TUI running this implementation remains open.

## API and trust boundary

The server is a trusted local development tool with access to project files
and coding agents. Bind to loopback by default. Do not expose it on an
untrusted network without an authenticated reverse proxy. It has no accounts
or remote authentication. Mutations require JSON and same-origin requests;
cross-origin API requests are rejected, while external links can open the UI.
Markdown is sanitized
before rendering. No endpoint accepts arbitrary shell commands or file paths.

The server accepts any HTTP Host and trusts the listener/proxy boundary for
access control. No public-host configuration or OAuth integration is needed.
`--listen` selects a network interface and port, not an external hostname.
Behind Traefik forward-auth (including a tunnel/router chain), preserve the
public Host and browser Origin. HTTPS may terminate at the proxy; do not
rewrite Origin or enable CORS. The UI lives at the hostname root (`/`). All
routes, including `/api/events`, must pass through the authenticated proxy;
stream SSE without buffering and allow its 20-second heartbeats. A router in
the same container can use the default loopback listener; a sidecar needs a
container-network listener. Network exposure is managed by the deployment,
not by an application Host allowlist.

`GET /api/state` returns items, reviews, project context, ordering and server
status. `GET /api/items/{id}` returns conversation, activity, retained traces,
live progress, sanitized Markdown, session information and draft. Raw items use
Go field names; review, activity and trace objects use their existing JSON
schemas. `POST /api/action` is a typed operation envelope, not a CLI shell.
`GET /api/events` emits an initial `change` event and subsequent invalidations
when project files change, plus heartbeat comments. Clients refetch the
relevant resources; reconnecting always sends an invalidation. v0 detects
changes using a metadata scan every 750 ms per connected client. Git status,
HEAD and changed-file metadata are included, so the diff updates after edits,
staging and commits even when no Ostraka item changes. Multiple browsers
subscribe independently to the same project state.

## Workspace operations

Inbox includes active and backlog conversations in foldable families, with
all non-archived statuses shown by default. Search, group and status filters
narrow the list; matching children retain their parent for context. Archive
remains a separate view. Ordering buttons preserve saved family and backlog
order. Select a conversation to read Markdown, activities and retained/live
traces and submit turns. The turn composer formats Markdown as you type while
leaving every source character visible: emphasis, strikethrough, code, links,
headings, quotes and list markers. It keeps native text selection, undo, paste
and keyboard input; the sent message and saved draft retain the original text. Drafts persist in the project and on the browser.
Composers follow shared draft updates once local edits have been saved, even
while focused. Sending or clearing a draft in either interface clears the other
clean composer; unchanged TUI checkpoints never write cached text back.
Unsaved local edits are preserved while other clients update. On reopening an
item, the shared draft takes precedence over saved browser caches. Unsaved
browser recovery text is restored only when its original shared draft still
matches. The TUI watches draft-only writes, including the first draft in a
project. A project still permits only one running TUI; multiple browser
clients can attach alongside it. Status/mode selectors and child/review buttons
expose item operations. Double-click the conversation title (or focus
it and press Enter) to rename it in place; Enter saves and Escape cancels.
Group and move fields offer dropdown suggestions and accept typed values.
Clear the move field to make a conversation a root. Type `@` in a message or
review text field to find items by title or ID, then click a suggestion or
use arrow keys and Enter/Tab to insert its mention. Delete is beside the
status controls. The footer shows the current/latest dispatched model and the
selection for the next turn, with a Change model button. Provider, model and
reasoning effort use dropdowns populated from the same choices as the TUI;
model discovery can be retried if it fails. A model change starts
a fresh provider session on the next turn; it neither queues work nor changes
the running turn. Provider defaults are labeled when the exact model has not
yet been reported. Settings are shared safely with the TUI through locked,
atomic session updates.

Send submits a new message and queues the agent for normal live items.
Backlog turns stay parked. Parked items instead offer Activate & queue (or
Accept & queue for proposals); failures offer Retry agent. These actions first
explain that they queue the existing conversation without adding a new message
and change its status to Queued (`pending-agent`). Attach mode can queue work
for the existing TUI supervisor; pause/interrupt still require `--supervise`.

The Reviews view shows current Git changes, including when there is no active
review, with full-file and changed-lines views and Unified / Side by side
layouts. The layout choice is remembered in the browser. Drag across code
lines to select a range; on touch screens drag the line numbers and swipe
code to scroll. Clicking a line or Shift-clicking a range also works.

A comment composer opens immediately beneath the selected range and scrolls
into view. Choose its item target and Add comment to create an anchored draft;
the first comment can create a review directly. The Review target selector
and Start review button also start or open a review from this panel without
visiting an item. Code remains attached to the chosen old/new side and source
line numbers in either layout. Open files and unfinished text survive live
review updates. Threads support
replies, editing/deleting unsent comments, changing code attachments, batch
submission, requesting a commit, and dismissal. Unsubmitted text is saved on
the current browser; added comments are durable in the project's review store.
Request commit explicitly asks an agent to commit; simply creating or reading
a review never commits anything.

Project edits shared instructions and brief. External changes update an open
editor when it has no unsaved edits; local edits survive live refreshes.
Review replies also retain text, focus and caret when other clients update
review threads. Pause/resume controls are also available there, including on mobile, when this process owns the supervisor.

v0 uses ordering arrows rather than drag and drop. Diff content reflects the
current working tree. Markdown supports GFM tables and Chroma syntax colors
for code blocks. Fenced code in the turn composer uses the same server-side
lexer with debounced updates and exact source preservation; unavailable
highlighting leaves the source readable. Browser visual validation uses Playwright's Chromium
and the system libraries included in the Carthage Dockerfile; DOM/API
interaction checks can run without a browser. The local server has no offline cache or authentication.

## Validation

`go test ./...` covers the Go server and shared operations. Optional frontend
interaction checks run with `npm ci --prefix internal/webui/tests`, then
`npm test --prefix internal/webui/tests`. They exercise composer preservation,
ordering, navigation, reviews, touch selection, and project editing in jsdom.

`npm run integration --prefix internal/webui/tests` exercises a real server at
`OSTRAKA_TEST_ORIGIN` (default http://127.0.0.1:18973). **Use a disposable
initialized project:** this check creates conversations and reviews and edits
project instructions. It must never target your working project.

Install the optional browser with:

```sh
npm exec --prefix internal/webui/tests -- playwright install chromium
```

Then run `npm run browser --prefix internal/webui/tests`.
Set `OSTRAKA_TEST_PROJECT` to a **fresh disposable Git project** served at
`OSTRAKA_TEST_ORIGIN`, and `OSTRAKA_TEST_BINARY` to the absolute path of the built
CLI. This check creates items and a working-tree file; it requires no active
reviews, so run it before the integration check or use a new disposable project.
It checks desktop and mobile layouts, two independent browser clients, CLI live
updates, inline actions, mention completion, shared drafts and review-free diffs.
It also checks dropdown model selection without dispatch, explicit queueing,
Markdown source preservation and code colors, undo and scroll alignment,
mouse and touch range dragging, old/new anchors in side-by-side diffs, and
starting targeted reviews and inline comments from the Reviews panel.
The test creates a fixture commit inside the disposable repository to exercise
tracked additions and deletions; it never commits the working project.
Set `OSTRAKA_TEST_SCREENSHOTS` to an output directory to retain screenshots.

`npm run draft-sync --prefix internal/webui/tests` runs a focused two-browser
check against the same disposable server. It covers edits in either client,
clearing after send, reopening items, stale caches, and unsaved recovery.

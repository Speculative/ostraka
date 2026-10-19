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

This owns the same supervisor lock as the TUI and enables pause, interrupt,
model discovery and fresh sessions. Closing the server shuts down its agents.
Do not start a second supervisor for this project. Attach mode does not expose
controls for another process's supervisor.

The UI is designed for desktop browsers and narrow screens. The bundled web
manifest supports a standalone desktop window in browsers that offer
"Install" or "Open as app"; it uses the same local server and project files.
There is no offline mode or bundled native runtime. Mobile and desktop browser
layouts are the supported targets; installing a desktop window is optional.

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
cross-site requests and unknown DNS hosts are rejected. Markdown is sanitized
before rendering. No endpoint accepts arbitrary shell commands or file paths.

`GET /api/state` returns items, reviews, project context, ordering and server
status. `GET /api/items/{id}` returns conversation, activity, retained traces,
live progress, sanitized Markdown, session information and draft. Raw items use
Go field names; review, activity and trace objects use their existing JSON
schemas. `POST /api/action` is a typed operation envelope, not a CLI shell.
`GET /api/events` emits an initial `change` event and subsequent invalidations
when project files change, plus heartbeat comments. Clients refetch the
relevant resources; reconnecting always sends an invalidation. v0 detects
changes using a metadata scan every 750 ms per connected client.

## Workspace operations

Inbox, Backlog and Archive group conversations into foldable families, with
search, group filters and ordering buttons. Select a conversation to read
Markdown, activities and retained/live traces and submit turns. Drafts persist
in the project and on the browser. The status and mode selectors, child/review
buttons and More menu expose item operations. Start agent explicitly queues
an item, including a backlog item or proposal; adding a turn to a backlog item
keeps it parked. Attach mode can queue work for the existing TUI supervisor;
it cannot pause, interrupt or change that process's provider sessions.

The Reviews view shows current Git changes, with full-file and changed-lines
views. Click a line for code context and Shift-click for a range, or enable
"Extend selection to next tapped line" on touch devices. Threads support
replies, editing/deleting unsent comments, changing code attachments, batch
submission, requesting a commit, and dismissal. Unsubmitted text is saved on
the current browser; added comments are durable in the project's review store.
Request commit explicitly asks an agent to commit; simply creating or reading
a review never commits anything.

Project edits shared instructions and brief. Pause/resume controls are also
available there, including on mobile, when this process owns the supervisor.

v0 uses ordering arrows rather than drag and drop. Diff content reflects the
current working tree. Server-rendered Markdown supports GFM tables but has no
syntax-coloring engine. Browser visual validation requires Chromium's system
libraries, which are absent from the current Carthage image; DOM/API interaction
checks run without them. The local server has no offline cache or authentication.

## Validation

`go test ./...` covers the Go server and shared operations. Optional frontend
interaction checks run with `npm ci --prefix internal/webui/tests`, then
`npm test --prefix internal/webui/tests`. They exercise composer preservation,
ordering, navigation, reviews, touch selection, and project editing in jsdom.

`npm run integration --prefix internal/webui/tests` exercises a real server at
`OSTRAKA_TEST_ORIGIN` (default http://127.0.0.1:18973). **Use a disposable
initialized project:** this check creates conversations and reviews and edits
project instructions. It must never target your working project.

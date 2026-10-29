const { JSDOM } = require("jsdom");
const fs = require("fs"),
  assert = require("node:assert/strict"),
  path = require("node:path");
const staticRoot = path.join(__dirname, "../static");
const html = fs.readFileSync(path.join(staticRoot, "index.html"), "utf8");
const dom = new JSDOM(html, {
  url: "http://localhost/#inbox/root",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const w = dom.window,
  d = w.document;
w.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
w.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
w.HTMLElement.prototype.scrollIntoView = function () {};
const now = new Date().toISOString();
const item = (ID, Title, Parent = "", Status = "active") => ({
  ID,
  Title,
  Parent,
  Status,
  Mode: "code",
  Group: "web",
  Created: now,
  Body: "Opening **context**",
  Turns: [{ Actor: "agent", Timestamp: now, Content: "Hello" }],
});
const state = {
  project: "Demo",
  items: [
    item("root", "First conversation"),
    item("kid", "Child", "root"),
    item("kid2", "Second child", "root"),
    item("later", "Saved for later", "", "backlog"),
    item("archived", "Old conversation", "", "archived"),
  ],
  reviews: [],
  child_orders: { root: ["kid", "kid2"] },
  backlog_order: ["later"],
  supervised: true,
  paused: false,
  instructions: "Rules",
  brief: "Facts",
};
const calls = [],
  saved = {},
  source = {};
let retainedPartials = [];
let holdDetail = null,
  holdTurn = null;
const gate = () => {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { promise, release, started: false };
};
let rejectDraft = false,
  rejectModels = false;
w.EventSource = class {
  constructor() {}
  addEventListener(name, fn) {
    source[name] = fn;
  }
};
w.fetch = async (path, options) => {
  let value;
  if (path === "/api/state") value = state;
  else if (path.startsWith("/api/items/")) {
    const id = decodeURIComponent(path.split("/").at(-1));
    value = {
      item: state.items.find((i) => i.ID === id),
      draft: saved[id] || "",
      body_html: "<p>Opening <strong>context</strong></p>",
      turns_html: ["<p>Hello</p>"],
      activities: [],
      partials: retainedPartials,
      live: "Progress text",
      live_active: true,
      session: {
        provider: "codex",
        model: "test",
        current: { provider: "codex", model: "current-test", effort: "medium" },
      },
      error: "",
    };
  } else if (path === "/api/diff")
    value = {
      Files: [
        {
          Path: "app.go",
          Status: "modified",
          Lines: [
            { Kind: 64, Text: "@@ -1,2 +1,2 @@" },
            { Kind: 32, Text: "first", OldLine: 1, NewLine: 1 },
            { Kind: 43, Text: "second", NewLine: 2 },
          ],
        },
      ],
    };
  else if (path === "/api/highlight") {
    const { source } = JSON.parse(options.body);
    value = [{ class: "syntax-keyword", text: source }];
  } else if (path.startsWith("/api/models")) {
    if (rejectModels)
      return {
        ok: false,
        json: async () => ({ error: "Discovery unavailable" }),
      };
    value = [
      {
        ID: "test",
        DisplayName: "Test",
        SupportedReasoningEfforts: ["medium"],
      },
    ];
  } else if (path === "/api/action") {
    const a = JSON.parse(options.body);
    calls.push(a);
    if (a.op === "draft") {
      if (rejectDraft)
        return { ok: false, json: async () => ({ error: "offline" }) };
      saved[a.id] = a.content;
    }
    if (a.op === "turn") {
      if (holdTurn) {
        const pending = holdTurn;
        holdTurn = null;
        pending.started = true;
        await pending.promise;
      }
      saved[a.id] = "";
      state.items
        .find((i) => i.ID === a.id)
        .Turns.push({ Actor: "user", Timestamp: now, Content: a.content });
    }
    if (a.op === "review-create") {
      const r = { id: "rev1", item_id: a.id, threads: [] };
      state.reviews.push(r);
      value = r;
    }
    if (a.op === "review-thread") {
      state.reviews[0].threads.push({
        number: 1,
        anchor: a.anchor,
        messages: [
          { id: "m1", actor: "user", content: a.content, created_at: now },
        ],
      });
    }
    if (a.op === "session") state.nextSession = a;
    if (a.op === "rename")
      state.items.find((i) => i.ID === a.id).Title = a.title;
    if (a.op === "group")
      state.items.find((i) => i.ID === a.id).Group = a.group;
    if (a.op === "reparent")
      state.items.find((i) => i.ID === a.id).Parent = a.parent;
    if (a.op === "unparent") state.items.find((i) => i.ID === a.id).Parent = "";
    if (a.op === "pause") state.paused = !state.paused;
    if (a.op === "instructions") state.instructions = a.content;
    if (a.op === "child-order") state.child_orders[a.id] = a.ids;
  }
  const snapshot = JSON.parse(JSON.stringify(value ?? null));
  if (path.startsWith("/api/items/") && holdDetail) {
    const pending = holdDetail;
    holdDetail = null;
    pending.started = true;
    await pending.promise;
  }
  return { ok: true, json: async () => snapshot };
};
const wait = (ms = 60) => new Promise((r) => setTimeout(r, ms));
const clickText = (root, text) => {
  const b = [...root.querySelectorAll("button")].find(
    (n) => n.textContent === text,
  );
  assert.ok(b, `button ${text}`);
  b.click();
  return b;
};
const input = (node, value) => {
  node.value = value;
  node.dispatchEvent(new w.Event("input", { bubbles: true }));
};
(async () => {
  w.eval(fs.readFileSync(path.join(staticRoot, "select.js"), "utf8"));
  w.eval(fs.readFileSync(path.join(staticRoot, "app.js"), "utf8"));
  await wait();
  assert.equal(
    d.querySelector(".detail-header h2").textContent,
    "First conversation",
  );
  assert.equal(d.querySelectorAll(".item-row").length, 4);
  assert.ok(d.querySelector(".markdown strong"));
  assert.equal(d.querySelector(".trace").textContent, "Progress text");
  assert.ok(
    d
      .querySelector("#session-label")
      .textContent.includes("Current: codex · current-test · medium"),
  );
  assert.ok(
    d
      .querySelector("#session-label")
      .textContent.includes("Next turn: codex · test"),
  );
  assert.equal(
    [...d.querySelectorAll("button")].some(
      (b) => b.textContent === "Start agent",
    ),
    false,
  );
  // Retained progress belongs to its explicitly linked agent reply. Runs with
  // no reply (or a missing target) remain readable standalone entries.
  retainedPartials = [
    {
      id: "linked",
      timestamp: new Date(Date.parse(now) - 2000).toISOString(),
      turn_timestamp: now,
      status: "completed",
      content: "Captured <script>work</script>",
    },
    {
      id: "linked-again",
      timestamp: now,
      turn_timestamp: now,
      status: "completed",
      content: "More captured work",
    },
    {
      id: "interrupted",
      timestamp: now,
      status: "interrupted",
      content: "Unfinished work",
    },
    {
      id: "missing-reply",
      timestamp: now,
      turn_timestamp: "2000-01-01T00:00:00Z",
      status: "completed",
      content: "Reply no longer available",
    },
  ];
  source.change();
  await wait(280);
  const linked = d.querySelector('details[data-key="linked"]');
  assert.equal(
    linked.closest(".message").querySelector(".markdown").textContent,
    "Hello",
  );
  assert.equal(linked.open, false);
  assert.equal(
    linked.querySelector(".trace").textContent,
    "Captured <script>work</script>",
  );
  assert.equal(linked.querySelector("script"), null);
  assert.equal(d.querySelectorAll(".message > details.trace-block").length, 2);
  assert.equal(d.querySelectorAll(".timeline > details.trace-block").length, 2);
  linked.open = true;
  retainedPartials[0].content = "Updated capture";
  source.change();
  await wait(280);
  assert.equal(d.querySelector('details[data-key="linked"]').open, true);
  assert.equal(
    d.querySelector('details[data-key="linked"] .trace').textContent,
    "Updated capture",
  );
  retainedPartials = [];
  source.change();
  await wait(280);
  source.open();
  assert.equal(d.querySelector("#connection").textContent, "Connected");
  // Inbox includes backlog by default; filters retain family context and archive stays separate.
  assert.equal(d.querySelector("a[href='#backlog']"), null);
  assert.ok(
    !d.querySelector("#item-list").textContent.includes("Old conversation"),
  );
  w.location.hash = "archive";
  await wait();
  assert.equal(d.querySelectorAll(".item-row").length, 1);
  assert.equal(d.querySelector("#status-filter").hidden, true);
  w.location.hash = "inbox/root";
  await wait();
  const statuses = d.querySelector("#status-filter");
  statuses.value = "backlog";
  statuses.dispatchEvent(new w.Event("change"));
  assert.equal(d.querySelectorAll(".item-row").length, 1);
  assert.ok(
    d.querySelector("#item-list").textContent.includes("Saved for later"),
  );
  statuses.value = "";
  statuses.dispatchEvent(new w.Event("change"));
  assert.equal(d.querySelectorAll(".item-row").length, 4);
  assert.equal(
    [...d.querySelectorAll("button")].some((b) => b.textContent === "More…"),
    false,
  );
  // Inline rename survives invalidation, with Enter to save and Escape to cancel.
  d.querySelector(".detail-header h2").dispatchEvent(
    new w.MouseEvent("dblclick", { bubbles: true }),
  );
  let titleInput = d.querySelector("[aria-label='Item title']");
  input(titleInput, "Renamed conversation");
  source.change();
  await wait(280);
  assert.equal(d.querySelector("[aria-label='Item title']"), titleInput);
  titleInput.dispatchEvent(
    new w.KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
  );
  await wait(280);
  assert.equal(
    d.querySelector(".detail-header h2").textContent,
    "Renamed conversation",
  );
  d.querySelector(".detail-header h2").dispatchEvent(
    new w.MouseEvent("dblclick", { bubbles: true }),
  );
  titleInput = d.querySelector("[aria-label='Item title']");
  input(titleInput, "Do not save");
  titleInput.dispatchEvent(
    new w.KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
  );
  await wait(280);
  assert.equal(calls.filter((c) => c.op === "rename").length, 1);
  const groupInput = d.querySelector(".inline-picker [name=group]");
  input(groupInput, "next");
  groupInput.closest("form").requestSubmit();
  await wait(280);
  assert.equal(calls.find((c) => c.op === "group").group, "next");
  const compose = d.querySelector("#composer");
  saved.root = "External saved draft";
  source.change();
  await wait(280);
  assert.equal(compose.value, "External saved draft");
  // @ completion inserts text-owned mentions without a separate mention mutation.
  input(compose, "See @Saved");
  assert.ok(!d.querySelector(".mention-menu").hidden);
  compose.dispatchEvent(
    new w.KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
  );
  assert.equal(compose.value, "See @later ");
  input(compose, "Email person@example.com");
  assert.equal(d.querySelector(".mention-menu").hidden, true);
  assert.equal(
    calls.some((c) => c.op === "mention"),
    false,
  );
  const markdownSource =
    "**bold** *italic* ~~gone~~ `code` [link](https://example.com)\n# Heading\n> Quote\n```js\nconst x = '<script>';\n```";
  input(compose, markdownSource);
  const preview = d.querySelector(".composer-preview");
  assert.equal(preview.textContent, markdownSource);
  assert.equal(preview.getAttribute("aria-hidden"), "true");
  assert.equal(preview.querySelector(".md-bold").textContent, "bold");
  assert.equal(preview.querySelector(".md-italic").textContent, "italic");
  assert.equal(preview.querySelector(".md-strike").textContent, "gone");
  assert.equal(preview.querySelector(".md-code").textContent, "code");
  assert.equal(preview.querySelector(".md-link").textContent, "link");
  assert.equal(preview.querySelectorAll("script,a").length, 0);
  assert.ok(preview.querySelector(".md-heading"));
  await wait(220);
  assert.ok(preview.querySelector(".syntax-keyword"));
  assert.equal(preview.textContent, markdownSource);
  input(compose, "Unsent draft");
  source.change();
  await wait(280);
  assert.equal(d.querySelector("#composer"), compose);
  assert.equal(compose.value, "Unsent draft");
  d.querySelector(".composer").requestSubmit();
  await wait(300);
  assert.equal(compose.value, "");
  assert.equal(calls.find((c) => c.op === "turn").content, "Unsent draft");
  // A delayed refresh containing an early draft must not resurrect sent text.
  compose.blur();
  saved.root = "First";
  const staleDetail = gate();
  holdDetail = staleDetail;
  source.change();
  await wait(280);
  assert.equal(staleDetail.started, true);
  input(compose, "First word and the rest of the message");
  d.querySelector(".composer").requestSubmit();
  await wait(60);
  assert.equal(compose.value, "");
  staleDetail.release();
  await wait(60);
  assert.equal(compose.value, "");
  assert.equal(w.localStorage.getItem("ostraka:Demo:draft:root"), null);
  assert.equal(saved.root, "");
  await wait(280);

  // Typing during a slow send stays local until the turn clears its old draft.
  input(compose, "Another message");
  const slowTurn = gate();
  holdTurn = slowTurn;
  d.querySelector(".composer").requestSubmit();
  await wait(60);
  assert.equal(slowTurn.started, true);
  const turnsBefore = calls.filter((c) => c.op === "turn").length;
  d.querySelector(".composer").requestSubmit();
  input(compose, "Next message");
  const draftCalls = calls.filter((c) => c.op === "draft").length;
  await wait(720);
  assert.equal(calls.filter((c) => c.op === "draft").length, draftCalls);
  assert.equal(calls.filter((c) => c.op === "turn").length, turnsBefore);
  slowTurn.release();
  await wait(300);
  assert.equal(compose.value, "Next message");
  assert.equal(saved.root, "Next message");

  // Once autosaved, a focused web composer follows a TUI clear without echoing.
  compose.focus();
  saved.root = "";
  const savesBeforeClear = calls.filter((c) => c.op === "draft").length;
  source.change();
  await wait(280);
  assert.equal(compose.value, "");
  assert.equal(w.localStorage.getItem("ostraka:Demo:draft:root"), null);
  await wait(720);
  assert.equal(calls.filter((c) => c.op === "draft").length, savesBeforeClear);
  // An external change must still leave uncheckpointed local input alone.
  input(compose, "Still typing locally");
  saved.root = "External edit";
  source.change();
  await wait(280);
  assert.equal(compose.value, "Still typing locally");
  compose.blur();

  // Family folding/search and optimistic order payload.
  d.querySelector(".fold").click();
  await wait();
  assert.equal(d.querySelectorAll(".item-row").length, 2);
  input(d.querySelector("#search"), "Child");
  assert.equal(d.querySelectorAll(".item-row").length, 3);
  input(d.querySelector("#search"), "");
  w.location.hash = "inbox/kid";
  await wait();
  clickText(d.querySelector(".toolbar"), "↓");
  await wait(280);
  assert.deepEqual(calls.find((c) => c.op === "child-order").expected, [
    "kid",
    "kid2",
  ]);
  assert.deepEqual(state.child_orders.root, ["kid2", "kid"]);
  const parentInput = d.querySelector(".inline-picker [name=parent]");
  input(parentInput, "later");
  parentInput.closest("form").requestSubmit();
  await wait(280);
  assert.equal(calls.find((c) => c.op === "reparent").parent, "later");
  const rootInput = d.querySelector(".inline-picker [name=parent]");
  input(rootInput, "");
  rootInput.closest("form").requestSubmit();
  await wait(280);
  assert.equal(calls.find((c) => c.op === "unparent").id, "kid");
  // Current diff is visible and refreshes with no active review.
  w.location.hash = "reviews";
  await wait();
  assert.ok(d.querySelector(".diff-file").textContent.includes("app.go"));
  source.change();
  await wait(280);
  assert.ok(d.querySelector(".diff-file"));
  w.location.hash = "inbox/kid";
  await wait();
  // Review creation, diff selection, anchored comment and parent item navigation.
  clickText(d.querySelector(".toolbar"), "Review");
  await wait(300);
  assert.ok(d.querySelector(".review-page"));
  d.querySelectorAll(".diff-line")[1].click();
  d.querySelectorAll(".diff-line")[2].dispatchEvent(
    new w.MouseEvent("click", { bubbles: true, shiftKey: true }),
  );
  const inlineComposer = d.querySelector(".inline-review-compose");
  assert.ok(inlineComposer);
  assert.ok(inlineComposer.previousElementSibling.classList.contains("chosen"));
  input(inlineComposer.querySelector("textarea"), "Review this range");
  inlineComposer.requestSubmit();
  await wait(300);
  const thread = calls.find((c) => c.op === "review-thread");
  assert.equal(thread.anchor.start_line, 1);
  assert.equal(thread.anchor.end_line, 2);
  assert.equal(thread.anchor.excerpt, "first\nsecond");
  assert.equal(d.querySelectorAll(".review-thread").length, 1);
  assert.equal(d.querySelector(".review-card>a").hash, "#inbox/kid");
  // Drafts, attachments, and touch range selection.
  d.querySelectorAll(".diff-line")[1].click();
  d.querySelectorAll(".diff-line")[2].dispatchEvent(
    new w.MouseEvent("click", { bubbles: true, shiftKey: true }),
  );
  clickText(d.querySelector(".review-thread"), "Attach selected code");
  await wait();
  assert.equal(calls.find((c) => c.op === "review-attach").anchor.end_line, 2);
  clickText(d.querySelector(".review-thread"), "Edit draft");
  await wait();
  input(d.querySelector("[name=content]"), "Updated comment");
  d.querySelector("#modal-form").requestSubmit();
  await wait();
  assert.equal(
    calls.find((c) => c.op === "review-draft").content,
    "Updated comment",
  );
  clickText(d.querySelector(".review-thread"), "Delete draft");
  await wait();
  d.querySelector("#modal-form").requestSubmit();
  await wait();
  assert.equal(
    calls.find((c) => c.op === "review-delete-draft").message_id,
    "m1",
  );
  let liveReply = d.querySelector(".review-compose textarea");
  input(liveReply, "Not yet added");
  liveReply.focus();
  liveReply.setSelectionRange(4, 4);
  state.reviews[0].threads[0].messages.push({
    id: "agent-live",
    actor: "agent",
    content: "External reply",
    created_at: now,
  });
  source.change();
  await wait(280);
  liveReply = d.querySelector(".review-compose textarea");
  assert.equal(d.activeElement, liveReply);
  assert.equal(liveReply.selectionStart, 4);
  assert.equal(liveReply.value, "Not yet added");
  assert.ok(
    d.querySelector(".review-page").textContent.includes("External reply"),
  );
  assert.equal(
    w.localStorage.getItem("ostraka:Demo:review:rev1:new"),
    "Not yet added",
  );
  // Project edits survive live refresh.
  w.location.hash = "project";
  await wait();
  state.instructions = "External rules";
  source.change();
  await wait(280);
  assert.equal(d.querySelector(".document-editor").value, "External rules");
  input(d.querySelector(".document-editor"), "Edited rules");
  state.instructions = "Changed elsewhere while editing";
  source.change();
  await wait(280);
  assert.equal(d.querySelector(".document-editor").value, "Edited rules");
  clickText(d.querySelector(".document-page"), "Save changes");
  await wait(280);
  assert.equal(state.instructions, "Edited rules");
  // Modal create and new-session model discovery.
  w.location.hash = "inbox/root";
  await wait();
  clickText(d.querySelector(".composer-footer"), "Change model");
  await wait();
  assert.equal(d.querySelector("#modal-title").textContent, "Change model");
  assert.equal(d.querySelector("[name=model]").tagName, "SELECT");
  assert.equal(d.querySelector("[name=effort]").tagName, "SELECT");
  assert.equal(d.querySelector("[name=model] option").value, "test");
  d.querySelector("[name=model]").value = "test";
  d.querySelector("[name=model]").dispatchEvent(new w.Event("change"));
  d.querySelector("[name=effort]").value = "medium";
  d.querySelector("#modal-form").requestSubmit();
  await wait();
  assert.equal(state.nextSession.model, "test");
  assert.equal(state.nextSession.effort, "medium");
  assert.equal(calls.filter((c) => c.op === "start").length, 0);
  rejectModels = true;
  clickText(d.querySelector(".composer-footer"), "Change model");
  await wait();
  assert.equal(d.querySelector("#modal-submit").disabled, true);
  assert.equal(d.querySelector("[name=model]").disabled, true);
  assert.ok(
    d
      .querySelector("#modal-error")
      .textContent.includes("Discovery unavailable"),
  );
  rejectModels = false;
  clickText(d.querySelector("#modal-fields"), "Retry model discovery");
  await wait();
  assert.equal(d.querySelector("#modal-submit").disabled, false);
  assert.equal(d.querySelector("[name=model]").value, "test");
  d.querySelector("#modal-cancel").click();
  // Attach mode exposes model selection too, and queuing parked work is explicit.
  state.supervised = false;
  source.change();
  await wait(280);
  assert.ok(!d.querySelector("#change-model").hidden);
  w.location.hash = "inbox/later";
  await wait();
  clickText(d.querySelector(".toolbar"), "Activate & queue");
  await wait();
  assert.ok(
    d.querySelector("#modal-fields").textContent.includes("pending-agent"),
  );
  assert.equal(calls.filter((c) => c.op === "start").length, 0);
  d.querySelector("#modal-cancel").click();
  d.querySelector("#new-item").click();
  assert.ok(d.querySelector("[name=title]"));
  d.querySelector("#modal-cancel").click();
  // Item links use a conversation view even from Reviews.
  source.error();
  assert.equal(d.querySelector("#connection").textContent, "Reconnecting…");
  console.log(
    "PASS: navigation, sanitized Markdown, composer identity, drafts/send, folding/search, child order, reviews/range anchors, project editing, model discovery, connection status",
  );
  dom.window.close();
})().catch((e) => {
  console.error(e);
  dom.window.close();
  process.exitCode = 1;
});

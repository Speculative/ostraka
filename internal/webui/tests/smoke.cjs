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
let rejectDraft = false;
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
      partials: [],
      live: "Progress text",
      live_active: true,
      session: { provider: "codex", model: "test" },
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
  else if (path.startsWith("/api/models"))
    value = [
      {
        ID: "test",
        DisplayName: "Test",
        SupportedReasoningEfforts: ["medium"],
      },
    ];
  else if (path === "/api/action") {
    const a = JSON.parse(options.body);
    calls.push(a);
    if (a.op === "draft") {
      if (rejectDraft)
        return { ok: false, json: async () => ({ error: "offline" }) };
      saved[a.id] = a.content;
    }
    if (a.op === "turn") {
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
    if (a.op === "pause") state.paused = !state.paused;
    if (a.op === "instructions") state.instructions = a.content;
    if (a.op === "child-order") state.child_orders[a.id] = a.ids;
  }
  return { ok: true, json: async () => value ?? null };
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
  w.eval(fs.readFileSync(path.join(staticRoot, "app.js"), "utf8"));
  await wait();
  assert.equal(
    d.querySelector(".detail-header h2").textContent,
    "First conversation",
  );
  assert.equal(d.querySelectorAll(".item-row").length, 3);
  assert.ok(d.querySelector(".markdown strong"));
  assert.equal(d.querySelector(".trace").textContent, "Progress text");
  source.open();
  assert.equal(d.querySelector("#connection").textContent, "Connected");
  const compose = d.querySelector("#composer");
  input(compose, "Unsent draft");
  source.change();
  await wait(280);
  assert.equal(d.querySelector("#composer"), compose);
  assert.equal(compose.value, "Unsent draft");
  d.querySelector(".composer").requestSubmit();
  await wait(300);
  assert.equal(compose.value, "");
  assert.equal(calls.find((c) => c.op === "turn").content, "Unsent draft");
  // Family folding/search and optimistic order payload.
  d.querySelector(".fold").click();
  await wait();
  assert.equal(d.querySelectorAll(".item-row").length, 1);
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
  // Review creation, diff selection, anchored comment and parent item navigation.
  clickText(d.querySelector(".toolbar"), "Review");
  await wait(300);
  assert.ok(d.querySelector(".review-page"));
  d.querySelectorAll(".diff-line")[1].click();
  d.querySelectorAll(".diff-line")[2].dispatchEvent(
    new w.MouseEvent("click", { bubbles: true, shiftKey: true }),
  );
  input(d.querySelector(".review-compose textarea"), "Review this range");
  d.querySelector(".review-compose").requestSubmit();
  await wait(300);
  const thread = calls.find((c) => c.op === "review-thread");
  assert.equal(thread.anchor.start_line, 1);
  assert.equal(thread.anchor.end_line, 2);
  assert.equal(thread.anchor.excerpt, "first\nsecond");
  assert.equal(d.querySelectorAll(".review-thread").length, 1);
  assert.equal(d.querySelector(".review-card>a").hash, "#inbox/kid");
  // Drafts, attachments, and touch range selection.
  const range = d.querySelector("#diff input[type=checkbox]");
  range.checked = true;
  range.dispatchEvent(new w.Event("change"));
  d.querySelectorAll(".diff-line")[1].click();
  d.querySelectorAll(".diff-line")[2].click();
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
  input(d.querySelector(".review-compose textarea"), "Not yet added");
  assert.equal(
    w.localStorage.getItem("ostraka:Demo:review:rev1:new"),
    "Not yet added",
  );
  // Project edits survive live refresh.
  w.location.hash = "project";
  await wait();
  input(d.querySelector(".document-editor"), "Edited rules");
  source.change();
  await wait(280);
  assert.equal(d.querySelector(".document-editor").value, "Edited rules");
  clickText(d.querySelector(".document-page"), "Save changes");
  await wait(280);
  assert.equal(state.instructions, "Edited rules");
  // Modal create and new-session model discovery.
  w.location.hash = "inbox/root";
  await wait();
  clickText(d.querySelector(".toolbar"), "More…");
  await wait();
  d.querySelector("[name=operation]").value = "session";
  d.querySelector("#modal-form").requestSubmit();
  await wait();
  assert.equal(
    d.querySelector("#modal-title").textContent,
    "New agent session",
  );
  assert.equal(d.querySelector("#model-options option").value, "test");
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

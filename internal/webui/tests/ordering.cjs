const { JSDOM } = require("jsdom");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const staticRoot = path.join(__dirname, "../static");
const html = fs.readFileSync(path.join(staticRoot, "index.html"), "utf8");
const script = fs.readFileSync(path.join(staticRoot, "app.js"), "utf8");
const time = (day) => `2026-10-${String(day).padStart(2, "0")}T12:00:00Z`;
const item = (ID, Status, created, last, Parent = "") => ({
  ID, Title: ID, Status, Parent, Created: time(created),
  Turns: last ? [{ Timestamp: time(last), Content: "Turn" }] : [],
});
async function check(name, items, expected, options = {}) {
  const dom = new JSDOM(html, {
    url: `http://localhost/#${options.view || "inbox"}`,
    runScripts: "outside-only",
  });
  try {
    const w = dom.window;
    w.EventSource = class { addEventListener() {} };
    w.fetch = async (url) => {
      assert.equal(url, "/api/state");
      return { ok: true, json: async () => ({
        project: "Ordering", items, reviews: [],
        child_orders: options.children || {},
        backlog_order: options.backlog || [],
      }) };
    };
    w.eval(script);
    await new Promise((resolve) => w.setTimeout(resolve, 20));
    assert.deepEqual(
      [...w.document.querySelectorAll("#item-list .item-title")].map((n) => n.textContent),
      expected, name,
    );
    assert.equal(w.document.querySelector("#toast").hidden, true, name);
  } finally {
    dom.window.close();
  }
}
(async () => {
  await check("Archive uses latest turn, falling back to creation", [
    item("old", "archived", 1), item("new", "archived", 3),
    item("recently-active", "archived", 2, 4),
  ], ["recently-active", "new", "old"], { view: "archive" });
  await check("Archive includes child activity and keeps children in creation order", [
    item("root", "archived", 1), item("other", "archived", 5),
    item("second", "archived", 3, 9, "root"),
    item("first", "archived", 2, 4, "root"),
  ], ["root", "first", "second", "other"], {
    view: "archive", children: { root: ["second", "first"] },
  });
  await check("Inbox uses TUI status priority", [
    item("proposed", "proposed", 1), item("user", "pending-user", 1),
    item("active", "active", 1), item("queued", "pending-agent", 1),
    item("working", "agent-acknowledged", 1), item("unknown", "future", 1),
    item("backlog", "backlog", 1),
  ], ["working", "queued", "user", "active", "proposed", "backlog", "unknown"]);
  await check("Inbox uses family priority then newest family activity", [
    item("recent", "active", 2, 7), item("old", "active", 3),
    item("family", "active", 1), item("kid", "pending-user", 1, 4, "family"),
    item("user", "pending-user", 1, 3),
  ], ["family", "kid", "user", "recent", "old"]);
  await check("Archived child activity counts in live family ties, as in TUI", [
    item("family", "active", 1), item("other", "active", 2, 3),
    item("done", "archived", 1, 4, "family"),
  ], ["family", "other"]);
  await check("Backlog roots stay parked and follow saved order", [
    item("unsaved", "backlog", 1, 9), item("a", "backlog", 2),
    item("b", "backlog", 3), item("queued", "pending-agent", 4, 8, "a"),
    item("live", "active", 1),
  ], ["live", "b", "a", "queued", "unsaved"], { backlog: ["b", "a"] });
  await check("Unordered backlog roots use newest activity", [
    item("old", "backlog", 1), item("new", "backlog", 2),
  ], ["new", "old"]);
  await check("Saved child order wins, then creation and ID break ties", [
    item("root", "active", 1), item("b", "active", 3, 9, "root"),
    item("a", "active", 3, 4, "root"), item("saved", "backlog", 4, 5, "root"),
  ], ["root", "saved", "a", "b"], { children: { root: ["saved"] } });
  for (const view of ["inbox", "archive"]) {
    await check("Equal activity uses stable ID order", [
      item("b", view === "archive" ? "archived" : "active", 1),
      item("a", view === "archive" ? "archived" : "active", 1),
    ], ["a", "b"], { view });
  }
  console.log("PASS: Archive recency and TUI-compatible Inbox family ordering");
})().catch((error) => { console.error(error); process.exitCode = 1; });

const { JSDOM } = require("jsdom");
const assert = require("node:assert/strict");
const fs = require("fs");
const path = require("node:path");
const staticRoot = path.join(__dirname, "../static");
const origin = process.env.OSTRAKA_TEST_ORIGIN || "http://127.0.0.1:18973";
const wait = (ms = 100) => new Promise((r) => setTimeout(r, ms));
async function api(op, fields = {}) {
  const r = await fetch(`${origin}/api/action`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ op, ...fields }),
  });
  const v = await r.json();
  if (!r.ok) throw Error(v.error);
  return v;
}
(async () => {
  const root = await api("create", {
    title: "Integration root",
    group: "web",
    body: "## Context\n\n**Safe** Markdown. <script>window.hacked=true</script>",
  });
  const dom = new JSDOM(
    fs.readFileSync(path.join(staticRoot, "index.html"), "utf8"),
    {
      url: `${origin}/#inbox/${root.ID}`,
      runScripts: "outside-only",
      pretendToBeVisual: true,
    },
  );
  const w = dom.window,
    d = w.document;
  w.fetch = (path, opts) => fetch(new URL(path, origin), opts);
  const events = {};
  w.EventSource = class {
    addEventListener(name, fn) {
      events[name] = fn;
    }
  };
  w.HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  w.HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  function btn(root, text) {
    const b = [...root.querySelectorAll("button")].find(
      (b) => b.textContent === text,
    );
    assert.ok(b, text);
    b.click();
    return b;
  }
  try {
    w.eval(fs.readFileSync(path.join(staticRoot, "app.js"), "utf8"));
    await wait(250);
    assert.equal(d.querySelector(".markdown h2").textContent, "Context");
    assert.equal(w.hacked, undefined);
    assert.equal(d.querySelector(".markdown script"), null);
    const compose = d.querySelector("#composer");
    const draftSource = "**Persistent** draft with `code` and ~~strike~~";
    compose.value = draftSource;
    compose.dispatchEvent(new w.Event("input"));
    assert.equal(d.querySelector(".composer-preview").textContent, draftSource);
    await wait(850);
    assert.equal(
      (await fetch(`${origin}/api/items/${root.ID}`).then((r) => r.json()))
        .draft,
      draftSource,
    );
    d.querySelector(".composer").requestSubmit();
    await wait(350);
    assert.equal(d.querySelector("#composer").value, "");
    assert.equal(d.querySelector(".composer-preview").textContent, "");
    assert.equal(
      (
        await fetch(`${origin}/api/items/${root.ID}`).then((r) => r.json())
      ).item.Turns.at(-1).Content,
      draftSource,
    );
    btn(d.querySelector(".toolbar"), "＋ Child");
    await wait();
    assert.equal(d.querySelector("#modal [name=group]"), null);
    d.querySelector("[name=title]").value = "Integration child";
    d.querySelector("#modal-form").requestSubmit();
    await wait(350);
    assert.equal(
      d.querySelector(".detail-header h2").textContent,
      "Integration child",
    );
    const snapshot = await fetch(`${origin}/api/state`).then((r) => r.json()),
      child = snapshot.items.find(
        (i) => i.Title === "Integration child" && i.Parent === root.ID,
      );
    assert.equal(child.Parent, root.ID);
    assert.equal(child.Group, "web");
    btn(d.querySelector(".toolbar"), "＋ Child");
    await wait();
    d.querySelector("[name=title]").value = "Integration sibling";
    d.querySelector("#modal-form").requestSubmit();
    await wait(350);
    const sibling = (
      await fetch(`${origin}/api/state`).then((r) => r.json())
    ).items.find(
      (i) => i.Title === "Integration sibling" && i.Parent === root.ID,
    );
    assert.equal(sibling.Parent, root.ID);
    btn(d.querySelector(".toolbar"), "Review");
    await wait(300);
    const reviewCard = () =>
      [...d.querySelectorAll(".review-card")].find((card) =>
        card.querySelector("a")?.hash.endsWith(`/${sibling.ID}`),
      );
    assert.ok(reviewCard());
    const comment = reviewCard().querySelector(".review-compose textarea");
    comment.value = "Review discussion";
    reviewCard().querySelector(".review-compose").requestSubmit();
    await wait(350);
    assert.equal(
      reviewCard().querySelector(".review-message .plain").textContent,
      "Review discussion",
    );
    // Exercise browser draft editing and deletion against the real review store.
    btn(reviewCard().querySelector(".review-thread"), "Edit draft");
    await wait();
    d.querySelector("[name=content]").value = "Edited review discussion";
    d.querySelector("#modal-form").requestSubmit();
    await wait(350);
    assert.equal(
      reviewCard().querySelector(".review-message .plain").textContent,
      "Edited review discussion",
    );
    const reply = reviewCard().querySelector(".review-reply textarea");
    reply.value = "Second comment";
    reviewCard().querySelector(".review-reply").requestSubmit();
    await wait(350);
    assert.equal(reviewCard().querySelectorAll(".review-message").length, 2);
    btn(reviewCard().querySelector(".review-message"), "Delete draft");
    await wait();
    d.querySelector("#modal-form").requestSubmit();
    await wait(350);
    assert.equal(reviewCard().querySelectorAll(".review-message").length, 1);
    w.location.hash = "project";
    await wait();
    d.querySelector(".document-editor").value = "Integration rules";
    btn(d.querySelector(".document-page"), "Save changes");
    await wait(300);
    assert.equal(
      (await fetch(`${origin}/api/state`).then((r) => r.json())).instructions,
      "Integration rules",
    );
    console.log(
      "PASS: actual Go API shapes, sanitized Markdown, draft persistence and send, grouped child/sibling creation, review creation/comment, project editing",
    );
  } finally {
    dom.window.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});

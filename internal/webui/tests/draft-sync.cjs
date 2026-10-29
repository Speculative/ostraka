// Writes test items. Run only against a disposable project/server.
const { chromium } = require("playwright");
const assert = require("node:assert/strict");
const origin = process.env.OSTRAKA_TEST_ORIGIN || "http://127.0.0.1:18973";
async function action(op, fields) {
  const response = await fetch(`${origin}/api/action`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ op, ...fields }),
  });
  const result = await response.json();
  assert.ok(response.ok, result.error);
  return result;
}
async function value(page, text) {
  await page.waitForFunction(
    (text) => document.querySelector("#composer")?.value === text,
    text,
  );
}
(async () => {
  const item = await action("create", { title: "Draft sync test" });
  const other = await action("create", { title: "Away from draft" });
  const browser = await chromium.launch({ headless: true });
  try {
    // Independent contexts exercise clients without shared localStorage.
    const a = await browser.newPage();
    const b = await browser.newPage();
    const url = `${origin}/#inbox/${item.ID}`;
    await Promise.all([a.goto(url), b.goto(url)]);
    await Promise.all([value(a, ""), value(b, "")]);
    await a.locator("#composer").fill("From first client");
    await value(b, "From first client");
    await b.locator("#composer").fill("From second client");
    await value(a, "From second client");
    const key = await a.evaluate((id) => Object.keys(localStorage).find(
      (key) => key.endsWith(`:draft:${id}`)), item.ID);
    assert.ok(key);
    await a.goto(`${origin}/#inbox/${other.ID}`);
    await b.locator(".composer").evaluate((form) => form.requestSubmit());
    await value(b, "");
    await a.goto(url);
    await value(a, "");
    // Simulate a stale saved cache and stale unsaved recovery from an old tab.
    await a.evaluate((key) => {
      localStorage.setItem(key, "Stale saved draft");
      localStorage.setItem(`${key}:unsaved`, JSON.stringify({
        base: "From second client", content: "Stale recovered draft",
      }));
    }, key);
    await a.reload();
    await value(a, "");
    assert.equal(await a.evaluate((key) => localStorage.getItem(`${key}:unsaved`), key), null);
    // Actual unsaved text recovers when its shared baseline is still current.
    await a.evaluate((key) => localStorage.setItem(`${key}:unsaved`, JSON.stringify({
      base: "", content: "Recover unsaved text",
    })), key);
    await a.reload();
    await value(a, "Recover unsaved text");
    await value(b, "Recover unsaved text");
    await a.locator(".composer").evaluate((form) => form.requestSubmit());
    await Promise.all([value(a, ""), value(b, "")]);
    // No idle client may echo a sent draft back into the project.
    await a.waitForTimeout(1500);
    const data = await fetch(`${origin}/api/items/${item.ID}`).then((r) => r.json());
    assert.equal(data.draft, "");
    assert.equal(data.item.Turns.at(-1).Content, "Recover unsaved text");
    console.log("PASS: two-client draft edits, send/clear, reopening, stale caches, unsaved recovery");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});

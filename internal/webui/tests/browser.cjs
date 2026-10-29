// Writes test data. Run only against a fresh disposable project/server.
const { chromium } = require("playwright");
const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const origin = process.env.OSTRAKA_TEST_ORIGIN || "http://127.0.0.1:18973";
const project = process.env.OSTRAKA_TEST_PROJECT;
const binary = process.env.OSTRAKA_TEST_BINARY;
const screenshots = process.env.OSTRAKA_TEST_SCREENSHOTS;
async function api(op, fields = {}) {
  const response = await fetch(`${origin}/api/action`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ op, ...fields }),
  });
  const value = await response.json();
  assert.ok(response.ok, value?.error);
  return value;
}
async function textIs(locator, text) {
  await locator
    .page()
    .waitForFunction(
      ({ selector, text }) =>
        document.querySelector(selector)?.textContent === text,
      { selector: ".detail-header h2", text },
    );
}
(async () => {
  assert.ok(
    project && binary,
    "Set OSTRAKA_TEST_PROJECT and OSTRAKA_TEST_BINARY to a disposable project and built CLI",
  );
  const initial = await fetch(`${origin}/api/state`).then((r) => r.json());
  assert.equal(
    initial.reviews?.length || 0,
    0,
    "Use a fresh project without active reviews",
  );
  const oldCode =
    "package main\n// Previous implementation\nfunc total() int {\n    old := 1\n    return old\n}\n";
  const newCode =
    "package main\n// New implementation\nfunc total() int {\n    next := 2\n    return next\n}\n";
  fs.writeFileSync(path.join(project, "review-sample.go"), oldCode);
  execFileSync("git", ["add", "review-sample.go"], { cwd: project });
  execFileSync(
    "git",
    [
      "-c",
      "user.name=Browser Test",
      "-c",
      "user.email=test@example.com",
      "-c",
      "core.hooksPath=/dev/null",
      "commit",
      "-m",
      "Browser test fixture",
    ],
    { cwd: project },
  );
  fs.writeFileSync(path.join(project, "review-sample.go"), newCode);
  const root = await api("create", {
    title: "Browser feedback test",
    body: "Shared conversation",
  });
  const parked = await api("create", {
    title: "Backlog browser test",
    status: "backlog",
  });
  await api("create", { title: "Archived browser test", status: "archived" });
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  try {
    const desktop = await browser.newContext({
      viewport: { width: 1440, height: 960 },
    });
    const phone = await browser.newContext({
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    });
    const a = await desktop.newPage(),
      b = await desktop.newPage(),
      mobile = await phone.newPage();
    for (const page of [a, b, mobile])
      page.on("pageerror", (error) => errors.push(error.message));
    await Promise.all([
      a.goto(`${origin}/#inbox/${root.ID}`),
      b.goto(`${origin}/#inbox/${root.ID}`),
    ]);
    await Promise.all([
      textIs(a.locator(".detail-header h2"), root.Title),
      textIs(b.locator(".detail-header h2"), root.Title),
    ]);
    assert.equal(await a.locator("#status-filter").inputValue(), "");
    assert.equal(
      await a
        .locator("#item-list")
        .getByText(parked.Title, { exact: true })
        .count(),
      1,
    );
    assert.equal(
      await a
        .locator("#item-list")
        .getByText("Archived browser test", { exact: true })
        .count(),
      0,
    );
    await a.locator("#status-filter").selectOption("backlog");
    assert.equal(await a.locator(".item-row").count(), 1);
    await a.locator("#status-filter").selectOption("");
    await a.locator(".detail-header h2").dblclick();
    await a
      .getByRole("textbox", { name: "Item title", exact: true })
      .fill("Renamed in browser");
    await a
      .getByRole("textbox", { name: "Item title", exact: true })
      .press("Enter");
    await textIs(b.locator(".detail-header h2"), "Renamed in browser");
    const env = { ...process.env };
    delete env.OSTRAKA_AGENT_ITEM_ID;
    execFileSync(binary, ["item", "rename", root.ID, "Renamed from CLI"], {
      cwd: project,
      env,
    });
    await Promise.all([
      textIs(a.locator(".detail-header h2"), "Renamed from CLI"),
      textIs(b.locator(".detail-header h2"), "Renamed from CLI"),
    ]);
    await a.getByLabel("Group", { exact: true }).fill("feedback");
    await a.getByRole("button", { name: "Set group", exact: true }).click();
    await b.waitForFunction(
      () => document.querySelector("[name=group]")?.value === "feedback",
    );
    assert.equal(
      await a.getByRole("button", { name: "Start agent", exact: true }).count(),
      0,
    );
    await a.getByRole("button", { name: "Change model", exact: true }).click();
    await a.locator("#modal [name=provider]").selectOption("claude");
    await a.locator("#modal [name=model]").selectOption("opus");
    await a.locator("#modal [name=effort]").selectOption("high");
    await a
      .getByRole("button", { name: "Apply to next turn", exact: true })
      .click();
    await b.waitForFunction(() =>
      document
        .querySelector("#session-label")
        ?.textContent.includes("Next turn: claude · opus · high"),
    );
    const afterModel = await fetch(`${origin}/api/items/${root.ID}`).then((r) =>
      r.json(),
    );
    assert.equal(afterModel.item.Status, "active");
    assert.equal(afterModel.session.model, "opus");
    await a.goto(`${origin}/#inbox/${parked.ID}`);
    await a
      .getByRole("button", { name: "Activate & queue", exact: true })
      .click();
    assert.ok(
      (await a.locator("#modal-fields").textContent()).includes(
        "pending-agent",
      ),
    );
    await a.getByRole("button", { name: "Cancel", exact: true }).click();
    assert.equal(
      (await fetch(`${origin}/api/items/${parked.ID}`).then((r) => r.json()))
        .item.Status,
      "backlog",
    );
    await a.goto(`${origin}/#inbox/${root.ID}`);
    await a.locator("#composer").fill("See @Backlog");
    await a
      .getByRole("option", { name: `@${parked.ID} · ${parked.Title}` })
      .click();
    assert.equal(
      await a.locator("#composer").inputValue(),
      `See @${parked.ID} `,
    );
    await b.waitForFunction(
      (value) => document.querySelector("#composer")?.value === value,
      `See @${parked.ID} `,
    );
    await a.goto(`${origin}/#reviews`);
    await a.locator(".diff-file").first().waitFor();
    assert.equal(await a.locator(".review-card").count(), 0);
    const changedFile = path.join(project, "browser-live.txt");
    fs.writeFileSync(changedFile, "live diff changed externally\n");
    await a
      .getByText("browser-live.txt · untracked", { exact: true })
      .waitFor();
    await a
      .locator(".diff-file")
      .filter({ hasText: "browser-live.txt" })
      .locator("summary")
      .click();
    fs.writeFileSync(changedFile, "second external edit\n");
    await a
      .getByText("+ second external edit", { exact: true })
      .waitFor({ state: "attached" });
    // Drag on the deleted side, then comment directly below the range.
    await a
      .getByRole("combobox", { name: "Diff layout", exact: true })
      .selectOption("split");
    const sample = a
      .locator(".diff-file")
      .filter({ has: a.locator("summary", { hasText: "review-sample.go" }) });
    if (!(await sample.evaluate((node) => node.open)))
      await sample.locator("summary").click();
    const codeLine = (page, side, number) =>
      page
        .locator(
          `.diff-line[data-path="review-sample.go"][data-side="${side}"]`,
        )
        .filter({
          has: page.locator(".line-number", {
            hasText: new RegExp(`^${number}$`),
          }),
        });
    const dragLines = async (page, side, first, last) => {
      const start = codeLine(page, side, first).locator(".line-number"),
        end = codeLine(page, side, last).locator(".line-number");
      await start.scrollIntoViewIfNeeded();
      await end.scrollIntoViewIfNeeded();
      const from = await start.boundingBox(),
        to = await end.boundingBox();
      await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
      await page.mouse.down();
      await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, {
        steps: 8,
      });
      await page.mouse.up();
    };
    await dragLines(a, "old", 4, 5);
    const inline = a.locator(".inline-review-compose");
    await inline.waitFor();
    assert.ok(
      (await inline.locator(".anchor").textContent()).includes("old lines 4–5"),
    );
    assert.equal(
      await inline.evaluate((node) =>
        node.previousElementSibling.classList.contains("diff-row"),
      ),
      true,
    );
    await inline
      .getByRole("combobox", { name: "Comment target", exact: true })
      .selectOption(parked.ID);
    await inline
      .getByRole("textbox", { name: "Comment on selected lines", exact: true })
      .fill("Check removed lines");
    await inline
      .getByRole("button", { name: "Add comment", exact: true })
      .click();
    await a.getByText("Check removed lines", { exact: true }).waitFor();
    let reviewState = await fetch(`${origin}/api/state`).then((r) => r.json());
    const removed = reviewState.reviews.find((r) => r.item_id === parked.ID)
      .threads[0].anchor;
    assert.equal(removed.side, "old");
    assert.equal(removed.start_line, 4);
    assert.equal(removed.end_line, 5);
    assert.equal(removed.excerpt, "    old := 1\n    return old");
    // Start another targeted review from the panel, without opening the item.
    await a
      .getByRole("combobox", { name: "Review target", exact: true })
      .selectOption(root.ID);
    await a.getByRole("button", { name: "Start review", exact: true }).click();
    await a.waitForFunction(
      (id) =>
        [...document.querySelectorAll(".review-card>a")].some((link) =>
          link.hash.endsWith(`/${id}`),
        ),
      root.ID,
    );
    await dragLines(a, "new", 4, 5);
    await inline
      .getByRole("textbox", { name: "Comment on selected lines", exact: true })
      .fill("Check added lines");
    await inline
      .getByRole("button", { name: "Add comment", exact: true })
      .click();
    await a.getByText("Check added lines", { exact: true }).waitFor();
    reviewState = await fetch(`${origin}/api/state`).then((r) => r.json());
    const added = reviewState.reviews.find((r) => r.item_id === root.ID)
      .threads[0].anchor;
    assert.equal(added.side, "new");
    assert.equal(added.excerpt, "    next := 2\n    return next");
    // Switch layout without changing source or range attachments.
    await a
      .getByRole("combobox", { name: "Diff layout", exact: true })
      .selectOption("unified");
    assert.equal(await a.locator(".diff-lines.split").count(), 0);
    await mobile.goto(`${origin}/#inbox/${root.ID}`);
    await textIs(mobile.locator(".detail-header h2"), "Renamed from CLI");
    assert.ok(await mobile.locator(".item-placement").isVisible());
    assert.ok(await mobile.locator("#composer").isVisible());
    const markdown =
      "**Bold** *italic* ~~strike~~ `inline code`\n[Link](https://example.com)\n> A quote";
    await mobile.locator("#composer").fill(markdown);
    assert.equal(
      await mobile.locator(".composer-preview").textContent(),
      markdown,
    );
    assert.equal(
      await mobile.locator(".composer-preview .md-bold").textContent(),
      "Bold",
    );
    assert.equal(
      await mobile.locator(".composer-preview .md-italic").textContent(),
      "italic",
    );
    assert.equal(
      await mobile.locator(".composer-preview .md-code").textContent(),
      "inline code",
    );
    assert.equal(await mobile.locator(".composer-preview a").count(), 0);
    const geometry = await mobile.evaluate(() => {
      const input = document.querySelector("#composer"),
        preview = document.querySelector(".composer-preview");
      const a = getComputedStyle(input),
        b = getComputedStyle(preview);
      return {
        inputWidth: input.clientWidth,
        previewWidth: preview.clientWidth,
        inputFont: a.font,
        previewFont: b.font,
        inputPadding: a.padding,
        previewPadding: b.padding,
      };
    });
    assert.equal(geometry.inputWidth, geometry.previewWidth);
    assert.equal(geometry.inputFont, geometry.previewFont);
    assert.equal(geometry.inputPadding, geometry.previewPadding);
    await mobile.locator("#composer").press("ControlOrMeta+End");
    await mobile.locator("#composer").pressSequentially("Z");
    await mobile.locator("#composer").press("ControlOrMeta+z");
    assert.equal(await mobile.locator("#composer").inputValue(), markdown);
    assert.equal(
      await mobile.locator(".composer-preview").textContent(),
      markdown,
    );
    await mobile
      .locator("#composer")
      .fill(
        Array.from({ length: 30 }, (_, n) => `line ${n} **bold**`).join("\n"),
      );
    await mobile.locator("#composer").evaluate((input) => {
      input.scrollTop = 100;
      input.dispatchEvent(new Event("scroll"));
    });
    assert.equal(
      await mobile
        .locator(".composer-preview")
        .evaluate((node) => node.scrollTop),
      await mobile.locator("#composer").evaluate((node) => node.scrollTop),
    );
    await mobile.locator("#composer").fill(markdown);
    await mobile.locator("#composer").evaluate((input) => {
      input.scrollTop = 0;
      input.dispatchEvent(new Event("scroll"));
    });
    assert.equal(
      await mobile.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      false,
      "Mobile layout must not overflow horizontally",
    );
    const codeSource =
      "```go\npackage main\n// highlighted comment\nconst answer = 42\n```";
    await mobile.locator("#composer").fill(codeSource);
    await mobile
      .locator(".composer-preview .syntax-keyword")
      .first()
      .waitFor({ state: "attached" });
    assert.equal(
      await mobile.locator(".composer-preview").textContent(),
      codeSource,
    );
    if (screenshots) {
      fs.mkdirSync(screenshots, { recursive: true });
      await mobile.screenshot({
        path: path.join(screenshots, "composer-code.png"),
      });
    }
    await mobile.goto(`${origin}/#reviews`);
    await mobile
      .getByRole("combobox", { name: "Diff layout", exact: true })
      .selectOption("split");
    const mobileFile = mobile.locator(".diff-file").filter({
      has: mobile.locator("summary", { hasText: "review-sample.go" }),
    });
    if (!(await mobileFile.evaluate((node) => node.open)))
      await mobileFile.locator("summary").click();
    await codeLine(mobile, "old", 4).scrollIntoViewIfNeeded();
    await codeLine(mobile, "old", 5).scrollIntoViewIfNeeded();
    const touchFrom = await codeLine(mobile, "old", 4)
      .locator(".line-number")
      .boundingBox();
    const touchTo = await codeLine(mobile, "old", 5)
      .locator(".line-number")
      .boundingBox();
    const cdp = await phone.newCDPSession(mobile);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [
        {
          x: touchFrom.x + touchFrom.width / 2,
          y: touchFrom.y + touchFrom.height / 2,
        },
      ],
    });
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [
        { x: touchTo.x + touchTo.width / 2, y: touchTo.y + touchTo.height / 2 },
      ],
    });
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await mobile.locator(".inline-review-compose").waitFor();
    assert.ok(
      (
        await mobile.locator(".inline-review-compose .anchor").textContent()
      ).includes("old lines 4–5"),
    );
    assert.equal(
      await mobile.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      false,
      "Split diff stays inside its scroller on mobile",
    );
    if (screenshots) {
      fs.mkdirSync(screenshots, { recursive: true });
      await b.screenshot({ path: path.join(screenshots, "desktop.png") });
      await mobile.screenshot({ path: path.join(screenshots, "mobile.png") });
      await a.screenshot({ path: path.join(screenshots, "reviews.png") });
    }
    assert.deepEqual(errors, []);
    console.log(
      "PASS: Chromium desktop/mobile, two-browser and CLI live sync, inline rename/group, @ completion, combined inbox/status filter, archive isolation, live diff without review, next-model selection, explicit queueing, inline Markdown/source/undo/scroll",
    );
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});

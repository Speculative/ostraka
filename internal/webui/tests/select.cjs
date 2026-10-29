const { chromium } = require("playwright");
const assert = require("node:assert/strict");
const origin = process.env.OSTRAKA_TEST_ORIGIN || "http://127.0.0.1:18973";
(async () => {
  const browser = await chromium.launch();
  try {
    for (const width of [1440, 390, 320]) {
      const page = await browser.newPage({
        viewport: { width, height: 844 },
        hasTouch: width < 640,
      });
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(origin);
      // Isolated fixture exercises the production component without changing items.
      await page.evaluate(() => {
        const form = document.createElement("form");
        form.id = "select-fixture";
        form.style =
          "position:fixed;bottom:20px;left:20px;width:250px;z-index:200;background:white";
        form.innerHTML = `<label>Fixture selector<select name="choice">
          <option value="a">Alpha</option><option value="b" disabled>Beta</option>
          <option value="c">Charlie</option><option value="d">Delta</option>
        </select></label><button type="reset">Reset fixture</button>`;
        document.body.append(form);
        window.fixtureChanges = [];
        form.addEventListener("change", (event) =>
          window.fixtureChanges.push(event.target.value),
        );
      });
      const combo = page.getByRole("combobox", {
        name: "Fixture selector",
        exact: true,
      });
      await combo.waitFor();
      await combo.press("Enter");
      assert.equal(await combo.getAttribute("aria-expanded"), "true");
      const bounds = await page
        .getByRole("listbox", { name: "Fixture selector" })
        .boundingBox();
      const trigger = await combo.boundingBox();
      assert.ok(
        bounds.y + bounds.height < trigger.y,
        "Near the bottom, menu opens upwards",
      );
      assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width);
      await combo.press("ArrowDown");
      await combo.press("Enter");
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "c",
        "Disabled options are skipped",
      );
      assert.deepEqual(await page.evaluate(() => window.fixtureChanges), ["c"]);
      await combo.press("Enter");
      await combo.press("End");
      await combo.press("Escape");
      assert.equal(await combo.getAttribute("aria-expanded"), "false");
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "c",
        "Escape cancels navigation",
      );
      await combo.press("d");
      await combo.press("Enter");
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "d",
      );
      await combo.press("Enter");
      await combo.press("Home");
      await combo.press("Tab");
      assert.equal(await combo.getAttribute("aria-expanded"), "false");
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "d",
        "Tab cancels navigation",
      );
      await page.getByRole("button", { name: "Reset fixture" }).click();
      await page.waitForFunction(
        () =>
          document.querySelector("#select-fixture .select-value")
            .textContent === "Alpha",
      );
      await page.evaluate(() => {
        const select = document.querySelector("#select-fixture select");
        select.replaceChildren(
          new Option("Echo", "e"),
          new Option("Foxtrot", "f"),
        );
        select.value = "f";
      });
      await page.waitForFunction(
        () =>
          document.querySelector("#select-fixture .select-value")
            .textContent === "Foxtrot",
      );
      assert.deepEqual(
        await page.evaluate(() => [
          ...new FormData(document.querySelector("#select-fixture")),
        ]),
        [["choice", "f"]],
      );
      await combo.click();
      await page.getByRole("option", { name: "Echo", exact: true }).click();
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "e",
      );
      await combo.click();
      await page.locator("#search").click();
      assert.equal(await combo.getAttribute("aria-expanded"), "false");
      await page.evaluate(
        () =>
          (document.querySelector("#select-fixture select").disabled = true),
      );
      await page.waitForFunction(
        () =>
          document.querySelector("#select-fixture .select-trigger").disabled,
      );
      if (width < 640) assert.ok((await combo.boundingBox()).height >= 44);
      await page.evaluate(() => {
        const form = document.querySelector("#select-fixture");
        const dialog = document.createElement("dialog");
        form.querySelector("select").disabled = false;
        dialog.append(form);
        document.body.append(dialog);
        dialog.showModal();
      });
      if (width < 640) {
        await combo.tap();
        await page.getByRole("option", { name: "Foxtrot", exact: true }).tap();
      } else {
        await combo.click();
        await page
          .getByRole("option", { name: "Foxtrot", exact: true })
          .click();
      }
      assert.equal(
        await page.locator("#select-fixture select").inputValue(),
        "f",
        "Menu works above a modal dialog, including touch selection",
      );
      assert.deepEqual(errors, []);
      await page.close();
    }
    console.log(
      "PASS: custom selects at desktop/390px/320px, keyboard/cancel/typeahead, disabled options, dynamic values, form reset/submission, pointer selection and viewport placement",
    );
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});

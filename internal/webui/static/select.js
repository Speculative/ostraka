"use strict";
// Enhance native form fields with the shared design-system listbox.
// The select remains the source of truth for values, options and FormData.
(() => {
  let sequence = 0,
    opened = null;
  const controls = new WeakMap();
  function enhance(select) {
    if (controls.has(select) || select.multiple || select.size > 1) return;
    const frame = document.createElement("span"),
      trigger = document.createElement("button"),
      value = document.createElement("span"),
      menu = document.createElement("div");
    frame.className = "select-control";
    trigger.className = "select-trigger";
    trigger.type = "button";
    trigger.setAttribute("role", "combobox");
    trigger.setAttribute("aria-haspopup", "listbox");
    trigger.setAttribute("aria-expanded", "false");
    menu.className = "select-menu";
    menu.id = `select-menu-${++sequence}`;
    menu.setAttribute("role", "listbox");
    menu.setAttribute("popover", "manual");
    menu.hidden = true;
    trigger.setAttribute("aria-controls", menu.id);
    value.className = "select-value";
    trigger.append(value);
    select.before(frame);
    frame.append(select, trigger, menu);
    select.setAttribute("aria-hidden", "true");
    select.tabIndex = -1;
    let active = -1,
      items = [],
      prefix = "",
      typedAt = 0;
    const enabled = (index) => {
      const option = select.options[index];
      return (
        option &&
        !option.disabled &&
        !option.parentElement.disabled &&
        !option.hidden
      );
    };
    function position() {
      if (menu.hidden) return;
      const rect = trigger.getBoundingClientRect();
      const viewport = window.visualViewport;
      const left = viewport?.offsetLeft || 0,
        top = viewport?.offsetTop || 0;
      const width = viewport?.width || innerWidth,
        height = viewport?.height || innerHeight;
      const below = top + height - rect.bottom - 12,
        above = rect.top - top - 12;
      const up = below < Math.min(menu.scrollHeight, 240) && above > below;
      menu.style.width = `${Math.min(Math.max(rect.width, 200), width - 24)}px`;
      menu.style.maxHeight = `${Math.max(44, Math.min(320, up ? above : below))}px`;
      menu.style.left = `${Math.max(left + 8, Math.min(rect.left, left + width - menu.getBoundingClientRect().width - 12))}px`;
      menu.style.top = `${up ? rect.top - menu.getBoundingClientRect().height - 4 : rect.bottom + 4}px`;
    }
    function highlight(index) {
      active = index;
      items.forEach((item, n) => item.classList.toggle("active", n === active));
      if (items[active]) {
        trigger.setAttribute("aria-activedescendant", items[active].id);
        items[active].scrollIntoView({ block: "nearest" });
      } else trigger.removeAttribute("aria-activedescendant");
    }
    function close() {
      if (!menu.hidden && menu.hidePopover) menu.hidePopover();
      menu.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      trigger.removeAttribute("aria-activedescendant");
      if (opened === control) opened = null;
      prefix = "";
    }
    function sync() {
      frame.hidden = select.hidden;
      if (select.hidden) close();
      value.textContent = select.selectedOptions[0]?.label || "Choose…";
      trigger.disabled = select.disabled;
      const label =
        select.getAttribute("aria-label") ||
        [...(select.labels || [])]
          .map((label) =>
            [...label.childNodes]
              .filter((node) => node !== frame)
              .map((node) => node.textContent)
              .join(" ")
              .trim(),
          )
          .join(" ") ||
        select.name ||
        "Choose an option";
      trigger.setAttribute("aria-label", label);
      menu.setAttribute("aria-label", label);
      if (select.hasAttribute("aria-labelledby"))
        trigger.setAttribute(
          "aria-labelledby",
          select.getAttribute("aria-labelledby"),
        );
      trigger.setAttribute("aria-required", String(select.required));
      if (select.disabled) close();
      if (!menu.hidden) render();
    }
    function choose(index) {
      if (!enabled(index)) return;
      const changed = select.selectedIndex !== index;
      close();
      select.selectedIndex = index;
      sync();
      trigger.focus();
      if (changed) {
        select.dispatchEvent(new Event("input", { bubbles: true }));
        select.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    function render() {
      const previous = active;
      items = [...select.options].map((option, index) => {
        const row = document.createElement("div");
        row.className = "select-option";
        row.id = `${menu.id}-${index}`;
        row.setAttribute("role", "option");
        row.setAttribute(
          "aria-selected",
          String(index === select.selectedIndex),
        );
        row.setAttribute("aria-disabled", String(!enabled(index)));
        row.hidden = option.hidden;
        row.textContent = option.label;
        row.addEventListener("pointermove", () => {
          if (enabled(index)) highlight(index);
        });
        row.addEventListener("pointerdown", (event) => event.preventDefault());
        row.addEventListener("click", () => choose(index));
        return row;
      });
      menu.replaceChildren(...items);
      position();
      highlight(
        enabled(previous)
          ? previous
          : enabled(select.selectedIndex)
            ? select.selectedIndex
            : [...select.options].findIndex((_, n) => enabled(n)),
      );
    }
    function open() {
      if (select.disabled) return;
      opened?.close();
      opened = control;
      active = select.selectedIndex;
      menu.hidden = false;
      if (menu.showPopover) menu.showPopover();
      trigger.setAttribute("aria-expanded", "true");
      render();
    }
    const control = { close, trigger, sync };
    controls.set(select, control);
    trigger.addEventListener("click", () => (menu.hidden ? open() : close()));
    trigger.addEventListener("blur", close);
    trigger.addEventListener("keydown", (event) => {
      if (event.ctrlKey || event.metaKey) return;
      const key = event.key;
      if (key === "Tab") {
        close();
        return;
      }
      event.stopPropagation();
      if (key === "Escape") {
        if (!menu.hidden) {
          event.preventDefault();
          close();
        }
        return;
      }
      if (["ArrowDown", "ArrowUp", "Home", "End", "Enter", " "].includes(key)) {
        event.preventDefault();
        if (menu.hidden) {
          open();
          if (!["Home", "End"].includes(key)) return;
        } else if (key === "Enter" || key === " ") {
          choose(active);
          return;
        }
        const choices = [...select.options].map((_, n) => n).filter(enabled);
        const current = choices.indexOf(active);
        const next =
          key === "Home"
            ? choices[0]
            : key === "End"
              ? choices.at(-1)
              : choices[
                  (current + (key === "ArrowUp" ? -1 : 1) + choices.length) %
                    choices.length
                ];
        highlight(next ?? -1);
      } else if (key.length === 1 && !event.altKey) {
        event.preventDefault();
        if (menu.hidden) open();
        const now = Date.now();
        prefix = now - typedAt > 700 ? key : prefix + key;
        typedAt = now;
        const search = [...prefix].every((c) => c === prefix[0])
          ? prefix[0]
          : prefix;
        for (let step = 1; step <= select.options.length; step++) {
          const index = (active + step) % select.options.length;
          if (
            enabled(index) &&
            select.options[index].label
              .toLocaleLowerCase()
              .startsWith(search.toLocaleLowerCase())
          ) {
            highlight(index);
            break;
          }
        }
      }
    });
    select.addEventListener("change", sync);
    select.addEventListener("invalid", () => trigger.focus());
    // Native property writes do not generate mutation records.
    for (const property of ["value", "selectedIndex"]) {
      const descriptor = Object.getOwnPropertyDescriptor(
        HTMLSelectElement.prototype,
        property,
      );
      Object.defineProperty(select, property, {
        configurable: true,
        get() {
          return descriptor.get.call(this);
        },
        set(next) {
          descriptor.set.call(this, next);
          sync();
        },
      });
    }
    select.focus = (...args) => trigger.focus(...args);
    new MutationObserver(sync).observe(select, {
      childList: true,
      subtree: true,
      attributes: true,
      characterData: true,
    });
    select.form?.addEventListener("reset", () => setTimeout(sync, 0));
    sync();
  }
  function scan(node) {
    if (
      node.nodeType !== 1 ||
      !node.isConnected ||
      typeof document === "undefined"
    )
      return;
    if (node.matches("select")) enhance(node);
    node.querySelectorAll("select").forEach(enhance);
  }
  new MutationObserver((records) => {
    for (const record of records) record.addedNodes.forEach(scan);
    if (opened && !opened.trigger.isConnected) opened.close();
  }).observe(document.body, { childList: true, subtree: true });
  document.addEventListener("pointerdown", (event) => {
    if (opened && !event.target.closest(".select-control")) opened.close();
  });
  window.addEventListener("resize", () => opened?.close());
  window.visualViewport?.addEventListener("resize", () => opened?.close());
  document.addEventListener(
    "scroll",
    (event) => {
      if (opened && !event.target.closest?.(".select-menu")) opened.close();
    },
    true,
  );
  document.addEventListener("close", () => opened?.close(), true);
  document.querySelectorAll("select").forEach(enhance);
})();

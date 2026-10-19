"use strict";
(() => {
  const $ = (s, root = document) => root.querySelector(s);
  const $$ = (s, root = document) => [...root.querySelectorAll(s)];
  const el = (tag, cls, text) => {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const append = (parent, ...children) => {
    children.filter(Boolean).forEach((c) => parent.append(c));
    return parent;
  };
  const button = (text, fn, cls = "") => {
    const b = el("button", cls, text);
    b.type = "button";
    b.addEventListener("click", () => Promise.resolve().then(fn).catch(report));
    return b;
  };
  const link = (id, title) => {
    const a = el("a", "", title || id);
    a.href = `#${channel(itemByID(id) || { Status: "active" })}/${encodeURIComponent(id)}`;
    return a;
  };
  const labels = {
    active: "Active",
    "pending-user": "Your turn",
    "pending-agent": "Queued",
    "agent-acknowledged": "Working",
    backlog: "Backlog",
    archived: "Archived",
    proposed: "Proposed",
  };
  const views = {
    inbox: ["◫", "Inbox"],
    backlog: ["▤", "Backlog"],
    archive: ["▧", "Archive"],
    reviews: ["⌘", "Reviews"],
    project: ["◇", "Project"],
  };
  let state = { items: [], reviews: [], child_orders: {}, backlog_order: [] };
  let view = "inbox",
    selected = "",
    detailID = "",
    current = null,
    revision = 0;
  let folded = new Set(),
    refreshTimer,
    toastTimer,
    reviewSignature = "",
    projectTab = "instructions";
  let stateLoaded = false;
  let refreshRunning = false,
    refreshAgain = false;
  const drafts = new Map(),
    draftJobs = new Map(),
    projectDrafts = new Map();
  const storage = {
    get(key) {
      try {
        return localStorage.getItem(key);
      } catch {
        return null;
      }
    },
    set(key, value) {
      try {
        localStorage.setItem(key, value);
      } catch {
        /* Private mode may deny storage. */
      }
    },
    remove(key) {
      try {
        localStorage.removeItem(key);
      } catch {
        /* Best effort. */
      }
    },
  };
  const draftKey = (id) => `ostraka:${state.project}:draft:${id}`;
  const report = (error) => toast(error.message || String(error), true);
  function toast(message, error = false) {
    const node = $("#toast");
    node.textContent = message;
    node.classList.toggle("error", error);
    node.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(
      () => {
        node.hidden = true;
      },
      error ? 8000 : 3500,
    );
  }
  async function request(path, options) {
    const response = await fetch(path, options);
    const data = await response.json();
    if (!response.ok || (path === "/api/action" && data?.error))
      throw new Error(data?.error || `Request failed (${response.status})`);
    return data;
  }
  async function action(op, fields = {}, refreshAfter = true) {
    const data = await request("/api/action", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ op, ...fields }),
    });
    if (refreshAfter) scheduleRefresh();
    return data;
  }
  function scheduleRefresh() {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(refresh, 180);
  }
  function date(value) {
    const d = new Date(value);
    return Number.isNaN(+d)
      ? ""
      : d.toLocaleString([], {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        });
  }
  function relative(value) {
    const seconds = Math.max(0, (Date.now() - new Date(value)) / 1000);
    if (!Number.isFinite(seconds)) return "";
    if (seconds < 60) return "now";
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
    return `${Math.floor(seconds / 86400)}d`;
  }
  const itemByID = (id) => state.items.find((i) => i.ID === id);
  const channel = (item) =>
    item.Status === "archived"
      ? "archive"
      : item.Status === "backlog" ||
          (item.Parent && itemByID(item.Parent)?.Status === "backlog")
        ? "backlog"
        : "inbox";
  function route() {
    const parts = location.hash.slice(1).split("/");
    view = views[parts[0]] ? parts[0] : "inbox";
    try {
      selected = decodeURIComponent(parts[1] || "");
    } catch {
      selected = "";
    }
    revision++;
    if (view === "project" || view === "reviews") selected = "";
    $(".workspace").classList.toggle(
      "detail-open",
      !!selected || view === "project" || view === "reviews",
    );
    $("#view-title").textContent = views[view][1];
    $("#new-item").hidden = view === "project";
    $(".filters").hidden = view === "project";
    renderNavigation();
    renderList();
    if (stateLoaded) renderDetail().catch(report);
  }
  function openItem(id) {
    location.hash = `${channel(itemByID(id) || { Status: "active" })}/${encodeURIComponent(id)}`;
  }
  function renderNavigation() {
    const nav = $("#navigation");
    nav.replaceChildren();
    Object.entries(views).forEach(([key, [icon, title]]) => {
      const a = el("a", `nav-link${view === key ? " active" : ""}`);
      a.href = `#${key}`;
      a.title = title;
      if (view === key) a.setAttribute("aria-current", "page");
      append(a, el("span", "nav-icon", icon), el("span", "nav-label", title));
      const count =
        key === "reviews"
          ? state.reviews.length
          : state.items.filter((i) => channel(i) === key).length;
      if (key !== "project") a.append(el("span", "count", String(count)));
      nav.append(a);
    });
    $("#project-name").textContent = state.project || "Workspace";
    $("#pause").hidden = !state.supervised;
    $("#pause").textContent = state.paused ? "Resume agents" : "Pause agents";
  }
  function matches(item) {
    const q = $("#search").value.trim().toLowerCase(),
      group = $("#group-filter").value;
    return (
      (!group || item.Group === group) &&
      (!q ||
        `${item.ID} ${item.Title} ${item.Body} ${(item.Turns || []).map((t) => t.Content).join(" ")}`
          .toLowerCase()
          .includes(q))
    );
  }
  function childrenOf(id) {
    const children = state.items.filter((i) => i.Parent === id),
      order = state.child_orders?.[id] || [];
    return children.sort((a, b) => {
      const ai = order.indexOf(a.ID),
        bi = order.indexOf(b.ID);
      return (
        (ai < 0 ? 99999 : ai) - (bi < 0 ? 99999 : bi) ||
        a.Created.localeCompare(b.Created)
      );
    });
  }
  function renderList() {
    const list = $("#item-list");
    list.replaceChildren();
    if (view === "project") {
      list.append(
        el(
          "div",
          "empty",
          "Project context is shared across every conversation. Keep instructions actionable and the brief focused on enduring facts.",
        ),
      );
      $("#list-count").textContent = "Shared context";
      return;
    }
    if (view === "reviews") {
      const reviews = state.reviews.filter((r) => {
        const i = itemByID(r.item_id);
        return matches(i || { ID: r.id, Title: r.item_id, Body: "" });
      });
      reviews.forEach((r) => {
        const row = button(
          "",
          () =>
            document
              .getElementById(`review-${r.id}`)
              ?.scrollIntoView({ behavior: "smooth", block: "start" }),
          "item-main",
        );
        append(
          row,
          el("span", "item-title", itemByID(r.item_id)?.Title || r.item_id),
          el(
            "span",
            "item-meta",
            `${r.threads?.length || 0} threads · ${r.commit_requested ? "Commit requested" : "In review"}`,
          ),
        );
        list.append(row);
      });
      if (!reviews.length)
        list.append(
          el(
            "div",
            "empty",
            "No active reviews. Start one from a conversation.",
          ),
        );
      $("#list-count").textContent = `${reviews.length} reviews`;
      return;
    }
    const visible = state.items.filter((i) => channel(i) === view),
      visibleIDs = new Set(visible.map((i) => i.ID));
    let roots = visible.filter((i) => !i.Parent || !visibleIDs.has(i.Parent));
    const priority = {
      "pending-user": 0,
      "agent-acknowledged": 1,
      "pending-agent": 2,
      proposed: 3,
      active: 4,
      backlog: 5,
      archived: 6,
    };
    const rank = (i) =>
      Math.min(
        priority[i.Status] ?? 9,
        ...childrenOf(i.ID)
          .filter((c) => visibleIDs.has(c.ID))
          .map((c) => priority[c.Status] ?? 9),
      );
    roots.sort((a, b) =>
      view === "backlog"
        ? ((state.backlog_order || []).indexOf(a.ID) + 1 || 99999) -
          ((state.backlog_order || []).indexOf(b.ID) + 1 || 99999)
        : view === "archive"
          ? a.Created.localeCompare(b.Created)
          : rank(a) - rank(b) || a.Created.localeCompare(b.Created),
    );
    let count = 0;
    roots.forEach((root) => {
      let children = childrenOf(root.ID).filter((i) => visibleIDs.has(i.ID));
      if (view === "archive")
        children.sort((a, b) => a.Created.localeCompare(b.Created));
      const rootMatches = matches(root),
        matchedChildren = children.filter(matches);
      if (!rootMatches && !matchedChildren.length) return;
      list.append(itemRow(root, false, children.length));
      count++;
      if (
        !folded.has(root.ID) ||
        $("#search").value ||
        $("#group-filter").value
      )
        (rootMatches ? children : matchedChildren).forEach((child) => {
          list.append(itemRow(child, true, 0));
          count++;
        });
    });
    if (!count)
      list.append(
        append(
          el("div", "empty"),
          el("span", "empty-mark", "◌"),
          el("h2", "", "All clear."),
          el("p", "", "No conversations match this view."),
        ),
      );
    $("#list-count").textContent = `${count} conversations`;
  }
  function itemRow(item, child, children) {
    const row = el(
      "div",
      `item-row${child ? " child" : ""}${item.ID === selected ? " selected" : ""}`,
    );
    if (children) {
      const fold = button(
        folded.has(item.ID) ? "▸" : "▾",
        () => {
          folded.has(item.ID) ? folded.delete(item.ID) : folded.add(item.ID);
          renderList();
        },
        "fold",
      );
      fold.setAttribute(
        "aria-label",
        `${folded.has(item.ID) ? "Expand" : "Fold"} family`,
      );
      fold.setAttribute("aria-expanded", String(!folded.has(item.ID)));
      row.append(fold);
    }
    const b = button("", () => openItem(item.ID), "item-main");
    const meta = el("span", "item-meta");
    append(
      meta,
      el("span", `status-dot ${item.Status}`),
      el("span", "", labels[item.Status] || item.Status),
    );
    if (item.Group) meta.append(el("span", "", item.Group));
    if (item.Goal) meta.append(el("span", "", "◉ goal"));
    const last = item.Turns?.at(-1)?.Timestamp || item.Created;
    meta.append(el("span", "", relative(last)));
    append(b, el("span", "item-title", item.Title || item.ID), meta);
    row.append(b);
    return row;
  }
  async function refresh() {
    if (refreshRunning) {
      refreshAgain = true;
      return;
    }
    refreshRunning = true;
    try {
      state = await request("/api/state");
      stateLoaded = true;
      state.items ||= [];
      state.reviews ||= [];
      const group = $("#group-filter"),
        old = group.value;
      group.replaceChildren(
        new Option("All groups", ""),
        ...Array.from(new Set(state.items.map((i) => i.Group).filter(Boolean)))
          .sort()
          .map((g) => new Option(g, g)),
      );
      group.value = old;
      renderNavigation();
      renderList();
      await renderDetail();
    } catch (e) {
      report(e);
    } finally {
      refreshRunning = false;
      if (refreshAgain) {
        refreshAgain = false;
        scheduleRefresh();
      }
    }
  }
  function emptyDetail() {
    detailID = "";
    current = null;
    $("#detail").replaceChildren(
      append(
        el("div", "empty"),
        el("span", "empty-mark", "◈"),
        el("h2", "", "A little space to think."),
        el("p", "", "Select a conversation to pick up where you left off."),
      ),
    );
  }
  async function renderDetail() {
    if (view === "project") {
      renderProject();
      return;
    }
    if (view === "reviews") {
      await renderReviews();
      return;
    }
    reviewSignature = "";
    if (!selected) {
      emptyDetail();
      return;
    }
    const id = selected,
      token = ++revision;
    if (detailID !== id) {
      $("#detail").replaceChildren(
        el("div", "loading", "Opening conversation…"),
      );
      detailID = "";
    }
    const data = await request(`/api/items/${encodeURIComponent(id)}`);
    if (token !== revision || selected !== id) return;
    current = data;
    if (detailID !== id) buildItem(data);
    renderItemHeader(data);
    renderTimeline(data);
  }
  function buildItem(data) {
    detailID = data.item.ID;
    const main = $("#detail"),
      header = el("header", "detail-header"),
      timeline = el("section", "timeline");
    timeline.setAttribute("aria-label", "Conversation");
    const update = button(
      "New updates ↓",
      () => {
        timeline.scrollTop = timeline.scrollHeight;
        update.hidden = true;
      },
      "new-updates",
    );
    update.hidden = true;
    update.id = "new-updates";
    const form = el("form", "composer"),
      textarea = el("textarea");
    textarea.id = "composer";
    textarea.placeholder = "Continue the conversation…";
    textarea.setAttribute("aria-label", "Message");
    const id = data.item.ID,
      local = storage.get(draftKey(id));
    if (!drafts.has(id))
      drafts.set(id, local !== null ? local : data.draft || "");
    textarea.value = drafts.get(id);
    textarea.addEventListener("input", () => {
      drafts.set(id, textarea.value);
      storage.set(draftKey(id), textarea.value);
      queueDraft(id, textarea.value);
      $("#draft-status").textContent = "Saving draft…";
    });
    textarea.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
        e.preventDefault();
        form.requestSubmit();
      }
    });
    const footer = el("div", "composer-footer"),
      info = el("span", "session-label");
    info.id = "session-label";
    const actions = el("div", "composer-actions"),
      draftStatus = el("span", "", "Drafts saved automatically");
    draftStatus.id = "draft-status";
    const send = el("button", "primary", "Send ↑");
    send.type = "submit";
    append(actions, draftStatus, send);
    append(footer, info, actions);
    append(form, textarea, footer);
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const content = textarea.value;
      if (!content.trim()) return;
      send.disabled = true;
      try {
        await flushDraft(id);
        await action("turn", { id, content }, false);
        // Keep text entered while this message was sending.
        if (textarea.value === content) {
          textarea.value = "";
          drafts.set(id, "");
          storage.remove(draftKey(id));
        } else {
          drafts.set(id, textarea.value);
          storage.set(draftKey(id), textarea.value);
          queueDraft(id, textarea.value);
        }
        toast("Message sent");
        scheduleRefresh();
      } catch (error) {
        report(error);
      } finally {
        send.disabled = false;
      }
    });
    main.replaceChildren(header, timeline, update, form);
    timeline.addEventListener("scroll", () => {
      if (
        timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight <
        100
      )
        update.hidden = true;
    });
  }
  function queueDraft(id, content) {
    let job = draftJobs.get(id);
    if (!job) {
      job = { pending: null, timer: null, chain: Promise.resolve() };
      draftJobs.set(id, job);
    }
    job.pending = content;
    clearTimeout(job.timer);
    job.timer = setTimeout(() => flushDraft(id).catch(report), 650);
  }
  async function flushDraft(id) {
    const job = draftJobs.get(id);
    if (!job) return;
    clearTimeout(job.timer);
    if (job.pending !== null) {
      const content = job.pending;
      job.pending = null;
      job.chain = job.chain
        .catch(() => {})
        .then(() => action("draft", { id, content }, false))
        .then(() => {
          if (detailID === id && $("#draft-status"))
            $("#draft-status").textContent = "Draft saved";
        })
        .catch((e) => {
          if (detailID === id && $("#draft-status"))
            $("#draft-status").textContent = "Saved on this device";
          throw e;
        });
    }
    return job.chain;
  }
  function renderItemHeader(data) {
    const i = data.item,
      header = $(".detail-header");
    header.replaceChildren();
    const crumbs = el("div", "breadcrumb");
    append(
      crumbs,
      button(
        "← Back",
        () => {
          location.hash = view;
        },
        "back-button",
      ),
      el("span", "", i.ID),
    );
    if (i.Parent)
      append(
        crumbs,
        el("span", "", "/"),
        link(i.Parent, itemByID(i.Parent)?.Title || i.Parent),
      );
    if (i.Group) crumbs.append(el("span", "badge", i.Group));
    if (i.Goal) crumbs.append(el("span", "badge goal", "Active goal"));
    const toolbar = el("div", "toolbar");
    const status = el("select");
    status.setAttribute("aria-label", "Item status");
    const allowed = ["active", "pending-user", "backlog", "archived"];
    if (!allowed.includes(i.Status)) {
      const opt = new Option(
        labels[i.Status] || i.Status,
        i.Status,
        true,
        true,
      );
      opt.disabled = true;
      status.add(opt);
    }
    allowed.forEach((s) =>
      status.add(new Option(labels[s], s, false, s === i.Status)),
    );
    status.addEventListener("change", () =>
      action("status", { id: i.ID, status: status.value }).catch(report),
    );
    const mode = el("select");
    mode.setAttribute("aria-label", "Execution mode");
    ["chat", "code", "goal"].forEach((m) =>
      mode.add(new Option(m.toUpperCase(), m, false, m === (i.Mode || "code"))),
    );
    mode.addEventListener("change", () =>
      action("mode", { id: i.ID, mode: mode.value }).catch(report),
    );
    append(
      toolbar,
      status,
      mode,
      button("＋ Child", () => newItem(i.Parent || i.ID)),
      button("Review", async () => {
        await action("review-create", { id: i.ID });
        location.hash = "reviews";
      }),
      button("More…", () => itemOptions(i)),
    );
    if (state.supervised && i.Status === "agent-acknowledged")
      toolbar.append(
        button("Stop", () => action("interrupt", { id: i.ID }), "danger"),
      );
    if (!["agent-acknowledged", "pending-agent", "archived"].includes(i.Status))
      toolbar.append(
        button("Start agent", () => action("start", { id: i.ID })),
      );
    if (i.Goal)
      toolbar.append(
        button("End goal", () => action("end-goal", { id: i.ID })),
      );
    if (i.Parent && view !== "archive")
      append(
        toolbar,
        button("↑", () => reorder(i, -1)),
        button("↓", () => reorder(i, 1)),
      );
    if (!i.Parent && view === "backlog")
      append(
        toolbar,
        button("↑", () => action("move-backlog", { id: i.ID, delta: -1 })),
        button("↓", () => action("move-backlog", { id: i.ID, delta: 1 })),
      );
    append(header, crumbs, el("h2", "", i.Title), toolbar);
    $("#session-label").textContent =
      [data.session?.provider, data.session?.model, data.session?.effort]
        .filter(Boolean)
        .join(" · ") ||
      (state.supervised
        ? "Default agent session"
        : "Attached to project · agent controls in the TUI");
    $("#session-label").title = $("#session-label").textContent;
  }
  async function reorder(i, delta) {
    const expected =
        state.child_orders?.[i.Parent] || childrenOf(i.Parent).map((c) => c.ID),
      ids = [...expected],
      at = ids.indexOf(i.ID),
      next = at + delta;
    if (at < 0 || next < 0 || next >= ids.length) return;
    [ids[at], ids[next]] = [ids[next], ids[at]];
    await action("child-order", { id: i.Parent, expected, ids });
  }
  function markdown(html, fallback) {
    const node = el("div", "markdown");
    // Only sanitized HTML returned by this server's Markdown renderer is used.
    if (html) node.innerHTML = html;
    else {
      node.textContent = fallback || "";
      node.classList.add("plain");
    }
    $$("a", node).forEach((a) => {
      a.rel = "noopener noreferrer";
    });
    return node;
  }
  function message(actor, timestamp, content, html) {
    const node = el("article", "message"),
      heading = el("div", "message-heading"),
      time = el("time", "", date(timestamp));
    time.dateTime = timestamp || "";
    append(
      heading,
      el("span", `avatar ${actor}`, actor === "user" ? "Y" : "O"),
      el("strong", "", actor === "user" ? "You" : "Agent"),
      time,
    );
    return append(node, heading, markdown(html, content));
  }
  function renderTimeline(data) {
    const timeline = $(".timeline");
    if (!timeline) return;
    const fingerprint = JSON.stringify([
      data.item.Body,
      data.item.Turns,
      data.activities,
      data.partials,
      data.live,
      data.live_active,
      data.error,
      data.session_warning,
    ]);
    if (timeline.dataset.fingerprint === fingerprint) return;
    const initial = !timeline.dataset.fingerprint,
      following =
        initial ||
        timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight <
          100,
      top = timeline.scrollTop;
    const open = new Set(
      $$("details[open]", timeline).map((n) => n.dataset.key),
    );
    timeline.replaceChildren();
    timeline.dataset.fingerprint = fingerprint;
    if (data.error) timeline.append(el("div", "notice", data.error));
    if (data.session_warning)
      timeline.append(el("div", "notice", data.session_warning));
    if (data.item.Goal?.prompt)
      timeline.append(
        append(
          el("details", "trace-block"),
          el("summary", "", "Goal prompt"),
          el("div", "plain", data.item.Goal.prompt),
        ),
      );
    if (data.item.Body)
      timeline.append(
        message("user", data.item.Created, data.item.Body, data.body_html),
      );
    const events = (data.item.Turns || []).map((t, index) => ({
      time: t.Timestamp,
      type: "turn",
      t,
      index,
    }));
    (data.activities || []).forEach((a) =>
      events.push({ time: a.timestamp, type: "activity", a }),
    );
    (data.partials || []).forEach((p) =>
      events.push({ time: p.timestamp, type: "partial", p }),
    );
    events.sort((a, b) => new Date(a.time) - new Date(b.time));
    events.forEach((event) => {
      if (event.type === "turn")
        timeline.append(
          message(
            event.t.Actor,
            event.t.Timestamp,
            event.t.Content,
            data.turns_html?.[event.index],
          ),
        );
      else if (event.type === "partial") {
        const details = el("details", "trace-block");
        details.dataset.key = event.p.id;
        details.open = open.has(event.p.id);
        append(
          details,
          el(
            "summary",
            "",
            `Retained trace · ${event.p.status || "captured"} · ${date(event.time)}`,
          ),
          el("pre", "trace", event.p.content),
        );
        timeline.append(details);
      } else {
        const a = event.a,
          node = el("div", "activity");
        if (a.child_id)
          node.append(link(a.child_id, a.child_title || a.child_id));
        append(
          node,
          el(
            "span",
            "",
            `${a.type.replaceAll(".", " · ").replaceAll("_", " ")}${a.result ? ` · ${a.result}` : ""}`,
          ),
          el("span", "muted", ` · ${date(a.timestamp)}`),
        );
        timeline.append(node);
      }
    });
    const mentions = [
      ...new Set([
        ...(data.item.Mentions || []),
        ...(itemByID(data.item.ID)?.Backlinks || []),
      ]),
    ];
    if (mentions.length) {
      const refs = el("div", "activity", "Related: ");
      mentions.forEach((id) =>
        append(
          refs,
          link(id, itemByID(id)?.Title || id),
          document.createTextNode(" "),
        ),
      );
      timeline.append(refs);
    }
    if (data.live_active || data.live) {
      const live = el("section", "message");
      append(
        live,
        el(
          "div",
          "live-label",
          data.live_active ? "Agent is working" : "Latest agent progress",
        ),
        el("pre", "trace", data.live || "Waiting for output…"),
      );
      timeline.append(live);
    }
    if (following) {
      timeline.scrollTop = timeline.scrollHeight;
      $("#new-updates").hidden = true;
    } else {
      timeline.scrollTop = top;
      $("#new-updates").hidden = false;
    }
  }
  let modalHandler = null;
  function field(name, label, type = "text", value = "", options = []) {
    return { name, label, type, value, options };
  }
  function showModal(title, fields, onSubmit, submit = "Save", note = "") {
    const dialog = $("#modal"),
      container = $("#modal-fields");
    container.replaceChildren();
    $("#modal-title").textContent = title;
    $("#modal-submit").textContent = submit;
    $("#modal-error").hidden = true;
    if (note) container.append(el("p", "field-note", note));
    fields.forEach((f) => {
      const label = el(
          "label",
          `field${f.type === "checkbox" ? " checkbox" : ""}`,
        ),
        title = el("span", "", f.label);
      let input;
      if (f.type === "textarea") {
        input = el("textarea");
        input.rows = 6;
      } else if (f.type === "select") {
        input = el("select");
        f.options.forEach((o) =>
          input.add(new Option(o.label ?? o, o.value ?? o)),
        );
      } else {
        input = el("input");
        input.type = f.type;
      }
      input.name = f.name;
      if (f.type === "checkbox") input.checked = !!f.value;
      else input.value = f.value || "";
      if (["title", "content"].includes(f.name)) input.required = true;
      append(label, title, input);
      container.append(label);
    });
    modalHandler = onSubmit;
    if (!dialog.open) dialog.showModal();
    setTimeout(() => $("input,textarea,select", container)?.focus(), 0);
  }
  $("#modal-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const values = Object.fromEntries(new FormData(e.target));
    $$("input[type=checkbox]", e.target).forEach(
      (i) => (values[i.name] = i.checked),
    );
    $("#modal-submit").disabled = true;
    try {
      await modalHandler(values);
      $("#modal").close();
    } catch (e) {
      $("#modal-error").textContent = e.message;
      $("#modal-error").hidden = false;
    } finally {
      $("#modal-submit").disabled = false;
    }
  });
  $("#modal-close").onclick = $("#modal-cancel").onclick = () =>
    $("#modal").close();
  function newItem(parent = "") {
    showModal(
      parent ? "New subthread" : "New conversation",
      [
        field("title", "Title"),
        field("body", "Opening context", "textarea"),
        ...(!parent
          ? [field("group", "Group", "text", $("#group-filter").value)]
          : []),
        field(
          "status",
          "Start in",
          "select",
          view === "backlog" ? "backlog" : "active",
          [
            { value: "active", label: "Inbox" },
            { value: "backlog", label: "Backlog" },
          ],
        ),
      ],
      async (values) => {
        const created = await action("create", { ...values, parent });
        if (created?.ID) openItem(created.ID);
      },
      "Create",
    );
  }
  function itemOptions(item) {
    const choices = [
      ["rename", "Rename"],
      ["group", "Change group"],
      ["reparent", "Move under another item"],
      ["unparent", "Make a root item"],
      ["mention", "Mention another item"],
      ["session", "New agent session"],
      ["delete", "Delete item"],
    ];
    showModal(
      "Conversation actions",
      [
        field(
          "operation",
          "Action",
          "select",
          "rename",
          choices
            .filter(([value]) => value !== "session" || state.supervised)
            .map(([value, label]) => ({ value, label })),
        ),
      ],
      async ({ operation }) => {
        // Close this picker before opening the selected form.
        setTimeout(() => {
          if (operation === "rename")
            showModal(
              "Rename conversation",
              [field("title", "Title", "text", item.Title)],
              (v) => action("rename", { id: item.ID, ...v }),
            );
          if (operation === "group")
            showModal(
              "Change group",
              [field("group", "Group (empty clears it)", "text", item.Group)],
              (v) => action("group", { id: item.ID, ...v }),
              "Save",
              "Subthreads inherit the group of their root.",
            );
          if (operation === "unparent")
            action("unparent", { id: item.ID }).catch(report);
          if (operation === "mention" || operation === "reparent") {
            const candidates = state.items.filter(
              (i) => i.ID !== item.ID && (operation === "mention" || !i.Parent),
            );
            showModal(
              operation === "mention" ? "Mention an item" : "Move conversation",
              [
                field(
                  "parent",
                  "Target item",
                  "select",
                  "",
                  candidates.map((i) => ({
                    value: i.ID,
                    label: `${i.ID} · ${i.Title}`,
                  })),
                ),
                ...(operation === "reparent"
                  ? [
                      field(
                        "flatten",
                        "Move this item’s children into the destination family",
                        "checkbox",
                        false,
                      ),
                    ]
                  : []),
              ],
              (v) => action(operation, { id: item.ID, ...v }),
            );
          }
          if (operation === "delete")
            showModal(
              "Delete conversation",
              [],
              async () => {
                await action("delete", { id: item.ID });
                location.hash = view;
              },
              "Delete permanently",
              `Permanently delete “${item.Title}” and its stored conversation? This cannot be undone.`,
            );
          if (operation === "session") sessionModal(item);
        }, 0);
      },
      "Continue",
    );
  }
  function sessionModal(item) {
    const session = current?.session || {};
    showModal(
      "New agent session",
      [
        field("provider", "Provider", "select", session.provider || "codex", [
          "codex",
          "claude",
        ]),
        field(
          "model",
          "Model (empty uses provider default)",
          "text",
          session.model,
        ),
        field(
          "effort",
          "Reasoning effort (empty uses default)",
          "text",
          session.effort,
        ),
      ],
      (v) => action("session", { id: item.ID, ...v }),
      "Start session",
      "Starts a fresh session using the chosen provider and model.",
    );
    const provider = $("[name=provider]", $("#modal")),
      model = $("[name=model]", $("#modal")),
      effort = $("[name=effort]", $("#modal"));
    const options = el("datalist");
    options.id = "model-options";
    $("#modal-fields").append(options);
    model.setAttribute("list", options.id);
    const efforts = el("datalist");
    efforts.id = "effort-options";
    $("#modal-fields").append(efforts);
    effort.setAttribute("list", efforts.id);
    let models = [];
    const updateEfforts = () => {
      const selectedModel = models.find((m) => m.ID === model.value);
      efforts.replaceChildren(
        ...(selectedModel?.SupportedReasoningEfforts || []).map(
          (v) => new Option(v, v),
        ),
      );
    };
    const load = async () => {
      const p = provider.value;
      options.replaceChildren();
      try {
        const result = await request(
          `/api/models?provider=${encodeURIComponent(p)}`,
        );
        if (provider.value !== p) return;
        models = Array.isArray(result) ? result : [];
        options.replaceChildren(
          ...models.map(
            (m) =>
              new Option(m.DisplayName || m.ID || "Provider default", m.ID),
          ),
        );
        updateEfforts();
      } catch (e) {
        toast(
          `Model discovery: ${e.message}. You can enter a model manually.`,
          true,
        );
      }
    };
    provider.addEventListener("change", () => {
      model.value = "";
      effort.value = "";
      load();
    });
    model.addEventListener("input", updateEfforts);
    load();
  }
  function renderProject() {
    const main = $("#detail");
    if (main.dataset.page === "project" && $(".document-editor")) {
      const pause = $("[data-project-pause]", main);
      if (pause)
        pause.textContent = state.paused ? "Resume agents" : "Pause agents";
      return;
    }
    detailID = "";
    main.dataset.page = "project";
    const page = el("section", "document-page");
    append(
      page,
      el("span", "eyebrow", "SHARED CONTEXT"),
      el("h2", "", "Project workspace"),
      el("p", "", state.project || "Ostraka"),
    );
    const tabs = el("div", "document-tabs"),
      editor = el("textarea", "document-editor");
    editor.spellcheck = false;
    const help = el("p", ""),
      save = button(
        "Save changes",
        async () => {
          const content = editor.value;
          save.disabled = true;
          try {
            await action(projectTab, { content });
            state[projectTab] = content;
            projectDrafts.delete(projectTab);
            toast("Project context saved");
          } finally {
            save.disabled = false;
          }
        },
        "primary",
      );
    function selectTab(tab) {
      if (editor.dataset.tab)
        projectDrafts.set(editor.dataset.tab, editor.value);
      projectTab = tab;
      editor.dataset.tab = tab;
      editor.value = projectDrafts.get(tab) ?? state[tab] ?? "";
      editor.setAttribute(
        "aria-label",
        tab === "instructions" ? "Project instructions" : "Project brief",
      );
      help.textContent =
        tab === "instructions"
          ? "Rules for how agents work in this project. Changes apply to future dispatches."
          : "Stable project facts and enduring design choices. Keep the brief within 6,000 characters.";
      $$("button", tabs).forEach((b) =>
        b.classList.toggle("active", b.dataset.tab === tab),
      );
    }
    ["instructions", "brief"].forEach((tab) => {
      const b = button(
        tab === "instructions" ? "Instructions" : "Project brief",
        () => selectTab(tab),
      );
      b.dataset.tab = tab;
      tabs.append(b);
    });
    editor.addEventListener("input", () =>
      projectDrafts.set(projectTab, editor.value),
    );
    append(page, tabs, help, editor, save);
    if (state.supervised) {
      const pause = button(
        state.paused ? "Resume agents" : "Pause agents",
        () => action("pause"),
      );
      pause.dataset.projectPause = "";
      page.append(pause);
    }
    main.replaceChildren(page);
    selectTab(projectTab);
  }
  let diffCache = null,
    diffLoading = null,
    anchor = null,
    diffStart = null,
    extendSelection = false,
    fullSource = false;
  const reviewDraftKey = (key) => `ostraka:${state.project}:review:${key}`;
  function reviewDraft(text, key, unsent) {
    text.dataset.draftKey = key;
    text.value = unsent.get(key) ?? storage.get(reviewDraftKey(key)) ?? "";
    text.addEventListener("input", () =>
      storage.set(reviewDraftKey(key), text.value),
    );
  }
  async function renderReviews(force = false) {
    const main = $("#detail"),
      signature = JSON.stringify(state.reviews);
    if (
      !force &&
      main.dataset.page === "reviews" &&
      (signature === reviewSignature ||
        (main.contains(document.activeElement) &&
          document.activeElement.matches("textarea")))
    )
      return;
    // Preserve unfinished thread replies across server refreshes.
    const unsent = new Map(
      $$("textarea", main)
        .filter((n) => n.dataset.draftKey)
        .map((n) => [n.dataset.draftKey, n.value]),
    );
    detailID = "";
    main.dataset.page = "reviews";
    reviewSignature = signature;
    const page = el("section", "review-page");
    append(
      page,
      el("span", "eyebrow", "WORKING TREE"),
      el("h2", "", "Code reviews"),
      el(
        "p",
        "",
        "Discuss changes, select code context, and send a batch back to the agent.",
      ),
    );
    if (!state.reviews.length)
      page.append(
        el(
          "div",
          "empty",
          "Start a review from a conversation to discuss the current working tree.",
        ),
      );
    state.reviews.forEach((review) => {
      const card = el("article", "review-card");
      card.id = `review-${review.id}`;
      append(
        card,
        link(review.item_id, itemByID(review.item_id)?.Title || review.item_id),
        el("p", "muted", review.id),
      );
      const controls = el("div", "toolbar");
      append(
        controls,
        button(
          "Send batch",
          () => action("review-submit", { review_id: review.id }),
          "primary",
        ),
        button(
          review.commit_requested ? "Commit requested" : "Request commit",
          () => {
            showModal(
              "Request reviewed commit",
              [],
              () => action("review-commit", { review_id: review.id }),
              "Request commit",
              "Ask the agent to run checks and commit the reviewed changes.",
            );
          },
        ),
        button("Dismiss", () =>
          showModal(
            "Dismiss review",
            [],
            () => action("review-dismiss", { review_id: review.id }),
            "Dismiss",
            "Dismiss this review and preserve it in the dismissed reviews archive.",
          ),
        ),
      );
      card.append(controls);
      (review.threads || []).forEach((thread) => {
        const node = el("section", "review-thread");
        node.append(el("h3", "", `Thread ${thread.number}`));
        if (thread.anchor) {
          node.append(
            el(
              "p",
              "anchor",
              `${thread.anchor.file} · ${thread.anchor.side} lines ${thread.anchor.start_line}–${thread.anchor.end_line}`,
            ),
          );
          if (thread.anchor.excerpt)
            node.append(
              append(
                el("details", "trace-block"),
                el("summary", "", "Code context"),
                el("pre", "trace", thread.anchor.excerpt),
              ),
            );
        }
        const attachments = el("div", "toolbar");
        append(
          attachments,
          button("Attach selected code", async () => {
            if (!anchor) throw new Error("Select code in the diff first.");
            await action("review-attach", {
              review_id: review.id,
              thread: thread.number,
              anchor,
            });
          }),
          thread.anchor &&
            button("Detach code", () =>
              action("review-detach", {
                review_id: review.id,
                thread: thread.number,
              }),
            ),
        );
        node.append(attachments);
        (thread.messages || []).forEach((m) => {
          const msg = el("div", "review-message"),
            heading = el("div", "message-heading");
          append(
            heading,
            el("strong", "", m.actor === "agent" ? "Agent" : "You"),
            el(
              "span",
              "badge",
              m.actor === "agent"
                ? m.recorded
                  ? "Recorded"
                  : "Reply"
                : m.submitted
                  ? "Sent"
                  : "Draft",
            ),
            el("time", "", date(m.created_at)),
          );
          append(msg, heading, el("div", "plain", m.content));
          if (m.actor === "user" && !m.submitted) {
            const fields = {
              review_id: review.id,
              thread: thread.number,
              message_id: m.id,
            };
            append(
              msg,
              append(
                el("div", "toolbar"),
                button("Edit draft", () =>
                  showModal(
                    "Edit review draft",
                    [field("content", "Comment", "textarea", m.content)],
                    ({ content }) =>
                      action("review-draft", { ...fields, content }),
                  ),
                ),
                button("Delete draft", () =>
                  showModal(
                    "Delete review draft",
                    [],
                    () => action("review-delete-draft", fields),
                    "Delete",
                    "Remove this unsent comment?",
                  ),
                ),
              ),
            );
          }
          node.append(msg);
        });
        const form = el("form", "review-reply"),
          text = el("textarea");
        text.placeholder = "Reply to this thread…";
        text.setAttribute("aria-label", `Reply to thread ${thread.number}`);
        reviewDraft(text, `${review.id}:${thread.number}`, unsent);
        const send = el("button", "", "Add reply");
        send.type = "submit";
        append(form, text, send);
        form.addEventListener("submit", async (e) => {
          e.preventDefault();
          if (!text.value.trim()) return;
          send.disabled = true;
          try {
            await action("review-reply", {
              review_id: review.id,
              thread: thread.number,
              content: text.value,
            });
            text.value = "";
            storage.remove(reviewDraftKey(text.dataset.draftKey));
            text.blur();
          } catch (e) {
            report(e);
          } finally {
            send.disabled = false;
          }
        });
        node.append(form);
        card.append(node);
      });
      const form = el("form", "review-compose"),
        text = el("textarea");
      text.placeholder = "Start a new discussion…";
      text.setAttribute("aria-label", "New review thread");
      reviewDraft(text, `${review.id}:new`, unsent);
      const selection = el("p", "anchor");
      selection.dataset.anchorLabel = "";
      selection.textContent = anchor
        ? `${anchor.file}:${anchor.start_line}–${anchor.end_line}`
        : "General comment · select a diff line below to attach code";
      const actions = el("div", "toolbar"),
        send = el("button", "primary", "Add thread");
      send.type = "submit";
      append(actions, button("Clear selection", clearAnchor), send);
      append(form, selection, text, actions);
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        if (!text.value.trim()) return;
        send.disabled = true;
        try {
          await action("review-thread", {
            review_id: review.id,
            content: text.value,
            ...(anchor ? { anchor } : {}),
          });
          text.value = "";
          storage.remove(reviewDraftKey(text.dataset.draftKey));
          text.blur();
          clearAnchor();
        } catch (e) {
          report(e);
        } finally {
          send.disabled = false;
        }
      });
      append(card, form);
      page.append(card);
    });
    if (state.reviews.length) {
      const diffSection = el("section");
      diffSection.id = "diff";
      const rangeLabel = el("label", "checkbox");
      const rangeInput = el("input");
      rangeInput.type = "checkbox";
      rangeInput.checked = extendSelection;
      rangeInput.addEventListener("change", () => {
        extendSelection = rangeInput.checked;
      });
      append(
        rangeLabel,
        rangeInput,
        document.createTextNode("Extend selection to next tapped line"),
      );
      diffSection.append(rangeLabel);
      const sourceLabel = el("label", "checkbox"),
        sourceInput = el("input");
      sourceInput.type = "checkbox";
      sourceInput.checked = fullSource;
      sourceInput.addEventListener("change", () => {
        fullSource = sourceInput.checked;
        clearAnchor();
        loadDiff(diffSection).catch(report);
      });
      append(
        sourceLabel,
        sourceInput,
        document.createTextNode("Show full files"),
      );
      diffSection.append(sourceLabel);
      const heading = el("div", "toolbar");
      append(
        heading,
        el("h3", "", "Current changes"),
        button("Refresh diff", async () => {
          diffCache = null;
          await loadDiff(diffSection);
        }),
      );
      append(
        diffSection,
        heading,
        el(
          "p",
          "muted",
          "Click a line to attach it to your next comment. Shift-click selects a range on the same side and hunk.",
        ),
      );
      const files = el("div");
      files.id = "diff-files";
      diffSection.append(files);
      page.append(diffSection);
      main.replaceChildren(page);
      await loadDiff(diffSection);
    } else main.replaceChildren(page);
  }
  async function loadDiff(section) {
    const files = $("#diff-files", section);
    if (!files) return;
    if (!diffCache) {
      files.replaceChildren(el("p", "muted", "Reading working tree…"));
      try {
        if (!diffLoading) diffLoading = request("/api/diff");
        diffCache = await diffLoading;
      } catch (e) {
        files.replaceChildren(el("p", "error", e.message));
        return;
      } finally {
        diffLoading = null;
      }
    }
    if (!section.isConnected) return;
    files.replaceChildren();
    if (!diffCache.Files?.length)
      files.append(el("p", "muted", "Working tree is clean."));
    (diffCache.Files || []).forEach((original) => {
      const file = {
        ...original,
        Lines: fullSource
          ? original.FullLines?.length
            ? original.FullLines
            : original.Lines
          : original.Lines,
      };
      const details = el("details", "diff-file");
      details.open = diffCache.Files.length === 1;
      details.append(el("summary", "", `${file.Path} · ${file.Status}`));
      if (file.Binary || file.Notice)
        details.append(
          el("p", "muted", file.Binary ? "Binary file" : file.Notice),
        );
      const lines = el("div", "diff-lines");
      (file.Lines || []).forEach((line, index) => {
        const kind =
          typeof line.Kind === "number"
            ? String.fromCharCode(line.Kind)
            : line.Kind;
        const b = el(
          "button",
          `diff-line${kind === "+" ? " add" : kind === "-" ? " remove" : ""}`,
        );
        b.dataset.index = index;
        b.dataset.path = file.Path;
        append(
          b,
          el("span", "line-number", line.OldLine || ""),
          el("span", "line-number", line.NewLine || ""),
          el("span", "code", `${kind} ${line.Text}`),
        );
        b.title =
          line.NewLine || line.OldLine
            ? "Select this line as review context"
            : "Hunk metadata";
        b.disabled = !(line.NewLine || line.OldLine);
        b.type = "button";
        b.addEventListener("click", (e) =>
          selectDiff(file, index, e.shiftKey || extendSelection),
        );
        lines.append(b);
      });
      details.append(lines);
      files.append(details);
    });
  }
  function clearAnchor() {
    anchor = null;
    diffStart = null;
    $$(".diff-line.chosen").forEach((n) => n.classList.remove("chosen"));
    $$("[data-anchor-label]").forEach(
      (n) =>
        (n.textContent =
          "General comment · select a diff line below to attach code"),
    );
  }
  function selectDiff(file, index, extend) {
    const line = file.Lines[index],
      side = line.NewLine ? "new" : "old";
    const start =
      extend && diffStart?.path === file.Path && diffStart.side === side
        ? diffStart.index
        : index;
    const from = Math.min(start, index),
      to = Math.max(start, index),
      selectedLines = file.Lines.slice(from, to + 1).filter((l) =>
        side === "new" ? l.NewLine : l.OldLine,
      );
    const numbers = selectedLines.map((l) =>
      side === "new" ? l.NewLine : l.OldLine,
    );
    if (numbers.some((n, i) => i > 0 && n !== numbers[i - 1] + 1)) {
      toast("Select a continuous range within one hunk.", true);
      return;
    }
    anchor = {
      file: side === "old" ? file.OldPath || file.Path : file.Path,
      side,
      start_line: numbers[0],
      end_line: numbers.at(-1),
      excerpt: selectedLines.map((l) => l.Text).join("\n"),
    };
    if (!extend || !diffStart) diffStart = { path: file.Path, side, index };
    $$(".diff-line").forEach((n) =>
      n.classList.toggle(
        "chosen",
        n.dataset.path === file.Path &&
          +n.dataset.index >= from &&
          +n.dataset.index <= to,
      ),
    );
    $$("[data-anchor-label]").forEach(
      (n) =>
        (n.textContent = `${anchor.file} · ${side} lines ${anchor.start_line}–${anchor.end_line}`),
    );
  }
  $("#detail").addEventListener("focusout", (e) => {
    if (view === "reviews" && e.target.matches("textarea")) scheduleRefresh();
  });
  $("#search").addEventListener("input", renderList);
  $("#group-filter").addEventListener("change", renderList);
  $("#new-item").addEventListener("click", () => newItem());
  $("#pause").addEventListener("click", () => action("pause").catch(report));
  window.addEventListener("hashchange", () => {
    $("#detail").dataset.page = "";
    route();
  });
  document.addEventListener("keydown", (e) => {
    if (
      $("#modal").open ||
      e.target.closest("input,textarea,select,[contenteditable]") ||
      e.ctrlKey ||
      e.metaKey ||
      e.altKey
    )
      return;
    if (e.key === "/") {
      e.preventDefault();
      $("#search").focus();
    }
    if (e.key === "n" || e.key === "N") {
      e.preventDefault();
      newItem();
    }
    if (e.key === "Escape" && selected) location.hash = view;
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") scheduleRefresh();
    else for (const id of draftJobs.keys()) flushDraft(id).catch(() => {});
  });
  window.addEventListener("pagehide", () => {
    for (const [id, content] of drafts) storage.set(draftKey(id), content);
  });
  const source = new EventSource("/api/events");
  source.addEventListener("open", () => {
    $("#connection").textContent = "Connected";
    $("#connection").classList.add("online");
  });
  source.addEventListener("change", () => {
    diffCache = null;
    scheduleRefresh();
  });
  source.addEventListener("error", () => {
    $("#connection").textContent = "Reconnecting…";
    $("#connection").classList.remove("online");
  });
  route();
  refresh();
})();

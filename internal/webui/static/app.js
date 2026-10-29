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
  const editedDrafts = new Set(),
    sendingDrafts = new Set(),
    draftVersions = new Map();
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
  const channel = (item) => (item.Status === "archived" ? "archive" : "inbox");
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
    $("#status-filter").hidden = view !== "inbox";
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
      (view !== "inbox" ||
        !$("#status-filter").value ||
        item.Status === $("#status-filter").value) &&
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
            "No active reviews. Current changes are shown in the reading pane.",
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
      i.Status === "backlog"
        ? priority.backlog
        : Math.min(
            priority[i.Status] ?? 9,
            ...childrenOf(i.ID)
              .filter((c) => visibleIDs.has(c.ID))
              .map((c) => priority[c.Status] ?? 9),
          );
    roots.sort((a, b) => {
      if (view === "archive") return a.Created.localeCompare(b.Created);
      const attention = rank(a) - rank(b);
      if (attention) return attention;
      if (a.Status === "backlog" && b.Status === "backlog") {
        const order = state.backlog_order || [];
        return (
          (order.indexOf(a.ID) < 0 ? 99999 : order.indexOf(a.ID)) -
            (order.indexOf(b.ID) < 0 ? 99999 : order.indexOf(b.ID)) ||
          a.Created.localeCompare(b.Created)
        );
      }
      return a.Created.localeCompare(b.Created);
    });
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
        $("#group-filter").value ||
        (view === "inbox" && $("#status-filter").value)
      )
        (rootMatches && !(view === "inbox" && $("#status-filter").value)
          ? children
          : matchedChildren
        ).forEach((child) => {
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
    const draftVersion = draftVersions.get(id);
    const data = await request(`/api/items/${encodeURIComponent(id)}`);
    if (token !== revision || selected !== id) return;
    current = data;
    if (detailID !== id) buildItem(data);
    const composer = $("#composer");
    if (
      draftVersion === draftVersions.get(id) &&
      !sendingDrafts.has(id) &&
      !editedDrafts.has(id)
    ) {
      const draft = data.draft || "";
      if (composer.value !== draft) {
        composer.value = draft;
        composer.renderPreview();
      }
      drafts.set(id, draft);
      if (draft) storage.set(draftKey(id), draft);
      else storage.remove(draftKey(id));
    }
    const header = $(".detail-header");
    if (
      !header.contains(document.activeElement) ||
      !document.activeElement.matches("input,select")
    )
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
    mentionCompletion(textarea);
    const id = data.item.ID,
      local = storage.get(draftKey(id));
    if (!drafts.has(id)) {
      drafts.set(id, local !== null ? local : data.draft || "");
      if (local !== null && local !== (data.draft || "")) editedDrafts.add(id);
    }
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
    const changeModel = button("Change model", () =>
      sessionModal(current.item),
    );
    changeModel.id = "change-model";
    append(footer, info, changeModel, actions);
    append(form, inlineMarkdownEditor(textarea), footer);
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const content = textarea.value;
      if (!content.trim() || sendingDrafts.has(id)) return;
      sendingDrafts.add(id);
      send.disabled = true;
      try {
        await flushDraft(id, true);
        await action("turn", { id, content }, false);
        // Ignore draft reads started before this send completed.
        draftVersions.set(id, (draftVersions.get(id) || 0) + 1);
        // Keep text entered while this message was sending.
        if (textarea.value === content) {
          textarea.value = "";
          drafts.set(id, "");
          editedDrafts.delete(id);
          const job = draftJobs.get(id);
          if (job) job.pending = null;
          storage.remove(draftKey(id));
        } else {
          drafts.set(id, textarea.value);
          storage.set(draftKey(id), textarea.value);
          queueDraft(id, textarea.value);
        }
        textarea.renderPreview();
        toast("Message sent");
        scheduleRefresh();
      } catch (error) {
        report(error);
      } finally {
        sendingDrafts.delete(id);
        flushDraft(id).catch(report);
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
    draftVersions.set(id, (draftVersions.get(id) || 0) + 1);
    editedDrafts.add(id);
    let job = draftJobs.get(id);
    if (!job) {
      job = { pending: null, timer: null, chain: Promise.resolve() };
      draftJobs.set(id, job);
    }
    job.pending = content;
    clearTimeout(job.timer);
    job.timer = setTimeout(() => flushDraft(id).catch(report), 650);
  }
  async function flushDraft(id, beforeSend = false) {
    const job = draftJobs.get(id);
    if (!job) return;
    if (sendingDrafts.has(id) && !beforeSend) return job.chain;
    clearTimeout(job.timer);
    if (job.pending !== null) {
      const content = job.pending,
        version = draftVersions.get(id);
      job.pending = null;
      job.chain = job.chain
        .catch(() => {})
        .then(() => action("draft", { id, content }, false))
        .then(() => {
          if (version === draftVersions.get(id)) {
            draftVersions.set(id, (version || 0) + 1);
            if (!sendingDrafts.has(id)) editedDrafts.delete(id);
          }
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
    const signature = JSON.stringify([
      i.ID,
      i.Title,
      i.Parent,
      i.Group,
      i.Status,
      i.Mode,
      i.Goal,
      data.session,
      data.error,
      state.supervised,
      state.items.map((item) => [item.ID, item.Title, item.Group, item.Parent]),
    ]);
    if (header.dataset.signature === signature) return;
    header.dataset.signature = signature;
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
        reviewTarget = i.ID;
        await targetReview(i.ID);
        location.hash = "reviews";
      }),
    );
    if (state.supervised && i.Status === "agent-acknowledged")
      toolbar.append(
        button("Stop", () => action("interrupt", { id: i.ID }), "danger"),
      );
    if (!["agent-acknowledged", "pending-agent", "archived"].includes(i.Status))
      if (["backlog", "proposed"].includes(i.Status) || data.error)
        toolbar.append(
          button(
            i.Status === "proposed"
              ? "Accept & queue"
              : i.Status === "backlog"
                ? "Activate & queue"
                : "Retry agent",
            () => queueAgent(i),
          ),
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
    if (!i.Parent && i.Status === "backlog")
      append(
        toolbar,
        button("↑", () => action("move-backlog", { id: i.ID, delta: -1 })),
        button("↓", () => action("move-backlog", { id: i.ID, delta: 1 })),
      );
    const title = el("h2", "editable-title", i.Title);
    title.tabIndex = 0;
    title.title = "Double-click to rename (or press Enter)";
    title.setAttribute(
      "aria-label",
      `${i.Title}. Double-click or press Enter to rename`,
    );
    title.addEventListener("dblclick", () => renameTitle(title, i));
    title.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && e.target === title) {
        e.preventDefault();
        renameTitle(title, i);
      }
    });
    append(header, crumbs, title, toolbar, itemPlacement(i));
    toolbar.append(button("Delete", () => deleteItem(i), "danger"));
    const sessionLabel = $("#session-label"),
      session = data.session || {};
    const describe = (selection) =>
      [
        selection.provider,
        selection.model || "Provider default",
        selection.effort,
      ]
        .filter(Boolean)
        .join(" · ");
    sessionLabel.replaceChildren(
      el(
        "span",
        "",
        `Current: ${session.current ? describe(session.current) : "Not yet reported"}${i.Status === "agent-acknowledged" ? " (running)" : ""}`,
      ),
      el("span", "", `Next turn: ${describe(session)}`),
    );
    sessionLabel.title = sessionLabel.textContent;
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
      if (f.type === "textarea") mentionCompletion(input);
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
  function renameTitle(title, item) {
    if ($("input", title)) return;
    const input = el("input", "title-input");
    input.value = item.Title;
    input.setAttribute("aria-label", "Item title");
    title.replaceChildren(input);
    input.focus();
    input.select();
    let done = false;
    const finish = async (save) => {
      if (done) return;
      done = true;
      try {
        if (save && input.value.trim() && input.value !== item.Title)
          await action("rename", { id: item.ID, title: input.value.trim() });
      } catch (e) {
        report(e);
      }
      title.textContent = itemByID(item.ID)?.Title || item.Title;
      scheduleRefresh();
    };
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        finish(e.key === "Enter");
      }
    });
    input.addEventListener("blur", () => finish(true));
  }
  function deleteItem(item) {
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
  }
  function itemPlacement(item) {
    const placement = el("div", "item-placement");
    function picker(label, name, value, options, submit) {
      const form = el("form", "inline-picker"),
        wrapper = el("label");
      const input = el("input"),
        list = el("datalist");
      list.id = `${name}-options`;
      input.name = name;
      input.value = value;
      input.setAttribute("list", list.id);
      input.setAttribute("aria-label", label);
      input.placeholder = name === "group" ? "No group" : "Root item";
      options.forEach(([value, title]) =>
        list.append(new Option(title, value)),
      );
      append(wrapper, el("span", "", label), input);
      append(form, wrapper, list);
      if (name === "parent" && childrenOf(item.ID).length) {
        const flatten = el("label", "checkbox"),
          check = el("input");
        check.type = "checkbox";
        check.name = "flatten";
        append(flatten, check, document.createTextNode("Move children too"));
        form.append(flatten);
      }
      const save = el("button", "", name === "group" ? "Set group" : "Move");
      save.type = "submit";
      form.append(save);
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        save.disabled = true;
        try {
          await submit(input.value.trim(), form);
          input.blur();
          scheduleRefresh();
        } catch (error) {
          report(error);
        } finally {
          save.disabled = false;
        }
      });
      placement.append(form);
    }
    picker(
      "Group",
      "group",
      item.Group || "",
      [...new Set(state.items.map((i) => i.Group).filter(Boolean))]
        .sort()
        .map((g) => [g, g]),
      (group) => action("group", { id: item.ID, group }),
    );
    picker(
      "Move under",
      "parent",
      item.Parent || "",
      state.items
        .filter((i) => i.ID !== item.ID && !i.Parent)
        .map((i) => [i.ID, i.Title]),
      (parent, form) => {
        if (!parent) return action("unparent", { id: item.ID });
        const target =
          itemByID(parent) ||
          state.items.find((i) => i.Title === parent && !i.Parent);
        if (!target || target.ID === item.ID || target.Parent)
          throw new Error("Choose a root item from the picker.");
        return action("reparent", {
          id: item.ID,
          parent: target.ID,
          flatten: !!$("[name=flatten]", form)?.checked,
        });
      },
    );
    return placement;
  }
  function mentionCompletion(input) {
    const menu = el("div", "mention-menu");
    menu.hidden = true;
    menu.setAttribute("role", "listbox");
    menu.id = `mentions-${++mentionSequence}`;
    input.setAttribute("aria-controls", menu.id);
    input.setAttribute("aria-autocomplete", "list");
    let start = 0,
      end = 0,
      active = 0,
      candidates = [];
    const close = () => {
      menu.hidden = true;
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
    };
    const choose = (item) => {
      input.setRangeText(`@${item.ID} `, start, end, "end");
      close();
      input.focus();
      input.dispatchEvent(new Event("input", { bubbles: true }));
    };
    const highlight = () => {
      $$("button", menu).forEach((b, n) =>
        b.setAttribute("aria-selected", String(n === active)),
      );
      input.setAttribute("aria-activedescendant", `${menu.id}-${active}`);
    };
    const update = () => {
      if (!input.isConnected) return;
      const caret = input.selectionStart,
        before = input.value.slice(0, caret);
      const match = before.match(/(?:^|\s)@([^\s@]*)$/);
      if (!match || input.selectionStart !== input.selectionEnd) {
        close();
        return;
      }
      start = caret - match[1].length - 1;
      end = caret;
      const query = match[1].toLowerCase();
      candidates = state.items
        .filter(
          (i) =>
            i.ID !== selected &&
            `${i.ID} ${i.Title}`.toLowerCase().includes(query),
        )
        .slice(0, 8);
      if (!candidates.length) {
        close();
        return;
      }
      active = 0;
      menu.replaceChildren();
      candidates.forEach((item, n) => {
        const option = button(`@${item.ID} · ${item.Title}`, () =>
          choose(item),
        );
        option.id = `${menu.id}-${n}`;
        option.setAttribute("role", "option");
        option.addEventListener("mousedown", (e) => e.preventDefault());
        menu.append(option);
      });
      if (!menu.isConnected)
        (input.closest(".markdown-editor") || input).after(menu);
      menu.hidden = false;
      input.setAttribute("aria-expanded", "true");
      highlight();
    };
    input.addEventListener("input", update);
    input.addEventListener("click", update);
    input.addEventListener("keydown", (e) => {
      if (menu.hidden) return;
      if (
        ["ArrowDown", "ArrowUp", "Enter", "Tab", "Escape"].includes(e.key) &&
        !e.ctrlKey &&
        !e.metaKey
      ) {
        e.preventDefault();
        e.stopImmediatePropagation();
        if (e.key === "Escape") close();
        else if (e.key === "Enter" || e.key === "Tab")
          choose(candidates[active]);
        else {
          active =
            (active + (e.key === "ArrowDown" ? 1 : -1) + candidates.length) %
            candidates.length;
          highlight();
        }
      }
    });
    input.addEventListener("blur", () => setTimeout(close, 180));
  }
  let mentionSequence = 0;
  function queueAgent(item) {
    showModal(
      "Queue agent without a new message?",
      [],
      () => action("start", { id: item.ID }),
      "Queue agent",
      `The agent will continue using this conversation's existing messages. “${item.Title}” will become Queued (pending-agent)${["backlog", "proposed"].includes(item.Status) ? " and leave its parked state" : ""}. To give new instructions, cancel and send a message instead.`,
    );
  }

  // Keep a native textarea for selection, undo, IME, paste and accessibility.
  // A matching, noninteractive layer styles the source without consuming any
  // Markdown characters or inserting HTML from the message.
  const syntaxCache = new Map();
  async function syntaxTokens(source, language) {
    const key = JSON.stringify([source, language]);
    if (!syntaxCache.has(key)) {
      if (syntaxCache.size >= 64)
        syntaxCache.delete(syntaxCache.keys().next().value);
      const pending = request("/api/highlight", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source, language }),
      });
      syntaxCache.set(key, pending);
      pending.catch(() => syntaxCache.delete(key));
    }
    return syntaxCache.get(key);
  }
  function applySyntaxLines(nodes, tokens) {
    const fragments = nodes.map(() => document.createDocumentFragment());
    let row = 0;
    tokens.forEach((token) =>
      token.text.split("\n").forEach((part, i) => {
        if (i) row++;
        if (part && fragments[row])
          fragments[row].append(el("span", token.class, part));
      }),
    );
    nodes.forEach((node, i) => {
      if (fragments[i].textContent === node.textContent)
        node.replaceChildren(fragments[i]);
    });
  }
  function inlineMarkdownEditor(textarea) {
    const editor = el("div", "markdown-editor"),
      preview = el("pre", "composer-preview");
    preview.setAttribute("aria-hidden", "true");
    const marker = (parent, text) =>
      parent.append(el("span", "md-marker", text));
    function inline(parent, text, depth = 0) {
      if (depth > 12) {
        parent.append(document.createTextNode(text));
        return;
      }
      let at = 0,
        plain = "";
      const flush = () => {
        if (plain) parent.append(document.createTextNode(plain));
        plain = "";
      };
      while (at < text.length) {
        if (text[at] === "\\" && at + 1 < text.length) {
          plain += text.slice(at, at + 2);
          at += 2;
          continue;
        }
        const rest = text.slice(at);
        const link = rest.match(/^\[([^\]\n]+)\]\(([^)\n]+)\)/);
        if (link) {
          flush();
          marker(parent, "[");
          const label = el("span", "md-link");
          inline(label, link[1], depth + 1);
          parent.append(label);
          marker(parent, `](${link[2]})`);
          at += link[0].length;
          continue;
        }
        const token = rest.match(/^(`+|\*\*\*|___|\*\*|__|~~|\*|_)/)?.[0];
        let end = token ? text.indexOf(token, at + token.length) : -1;
        if (token && !token.startsWith("`")) {
          if (
            /\s/.test(text[at + token.length] || " ") ||
            (token.includes("_") && /[\p{L}\p{N}]/u.test(text[at - 1] || ""))
          )
            end = -1;
          while (
            end >= 0 &&
            (text[end - 1] === "\\" || /\s/.test(text[end - 1]))
          )
            end = text.indexOf(token, end + token.length);
        }
        if (end > at + (token?.length || 0)) {
          flush();
          marker(parent, token);
          const cls = token.startsWith("`")
            ? "md-code"
            : token === "~~"
              ? "md-strike"
              : token.length === 3
                ? "md-bold md-italic"
                : token.length === 2
                  ? "md-bold"
                  : "md-italic";
          const content = el("span", cls);
          if (token.startsWith("`"))
            content.textContent = text.slice(at + token.length, end);
          else inline(content, text.slice(at + token.length, end), depth + 1);
          parent.append(content);
          marker(parent, token);
          at = end + token.length;
          continue;
        }
        plain += text[at++];
      }
      flush();
    }
    let highlightTimer,
      previewGeneration = 0;
    textarea.renderPreview = () => {
      const version = ++previewGeneration;
      clearTimeout(highlightTimer);
      const codeBlocks = [];
      let block = null;
      const fragment = document.createDocumentFragment();
      let fence = "";
      const lines = textarea.value.split("\n");
      lines.forEach((line, index) => {
        const node = el("span", "md-line"),
          delimiter = line.match(/^ {0,3}(`{3,}|~{3,})/);
        if (
          delimiter &&
          (!fence ||
            (delimiter[1][0] === fence[0] &&
              delimiter[1].length >= fence.length))
        ) {
          marker(node, line);
          if (!fence) {
            block = {
              language: line.slice(delimiter[0].length).trim().split(/\s/)[0],
              nodes: [],
            };
            codeBlocks.push(block);
          }
          fence = fence ? "" : delimiter[1];
        } else if (fence) {
          const code = el("span", "md-code", line);
          block.nodes.push(code);
          node.append(code);
        } else {
          const prefix = line.match(
            /^( {0,3}(?:#{1,6} |(?:> ?)+|[-+*] |\d+\. ))/,
          );
          if (prefix) {
            marker(node, prefix[0]);
            if (prefix[0].includes("#")) node.classList.add("md-heading");
            if (prefix[0].includes(">")) node.classList.add("md-quote");
          }
          inline(node, line.slice(prefix?.[0].length || 0));
        }
        fragment.append(node);
        if (index < lines.length - 1)
          fragment.append(document.createTextNode("\n"));
      });
      // A final line keeps scroll and caret geometry identical after Enter.
      if (textarea.value.endsWith("\n"))
        fragment.append(document.createTextNode("\u200b"));
      preview.replaceChildren(fragment);
      highlightTimer = setTimeout(() => {
        codeBlocks.forEach(async (block) => {
          if (!block.nodes.length) return;
          try {
            const tokens = await syntaxTokens(
              block.nodes.map((n) => n.textContent).join("\n"),
              block.language,
            );
            if (version === previewGeneration && preview.isConnected)
              applySyntaxLines(block.nodes, tokens);
          } catch {
            /* Keep exact source readable while offline. */
          }
        });
      }, 160);
      preview.scrollTop = textarea.scrollTop;
      preview.scrollLeft = textarea.scrollLeft;
    };
    textarea.addEventListener("input", textarea.renderPreview);
    textarea.addEventListener("scroll", () => {
      preview.scrollTop = textarea.scrollTop;
      preview.scrollLeft = textarea.scrollLeft;
    });
    textarea.renderPreview();
    append(editor, preview, textarea);
    return editor;
  }

  function sessionModal(item) {
    const session = current?.session || {};
    showModal(
      "Change model",
      [
        field("provider", "Provider", "select", session.provider || "codex", [
          "codex",
          "claude",
        ]),
        field("model", "Model", "select"),
        field("effort", "Reasoning effort", "select"),
      ],
      async (values) => {
        if (loading || !models.some((m) => m.ID === values.model))
          throw new Error("Choose an available model first.");
        await action("session", { id: item.ID, ...values });
        toast("Model saved for the next turn");
      },
      "Apply to next turn",
      "Applies on the next turn in a fresh provider session. The running turn continues with its current model. This does not queue an agent.",
    );
    const provider = $("#modal [name=provider]"),
      model = $("#modal [name=model]"),
      effort = $("#modal [name=effort]");
    let models = [],
      loading = true,
      generation = 0;
    function updateEfforts(preferred = "") {
      const option = models.find((m) => m.ID === model.value);
      const choices = option?.SupportedReasoningEfforts || [];
      effort.replaceChildren(
        new Option("Provider default", ""),
        ...choices.map((v) => new Option(v, v)),
      );
      effort.value = choices.includes(preferred)
        ? preferred
        : option?.DefaultReasoningEffort || "";
      if (!choices.includes(effort.value)) effort.value = "";
    }
    async function load() {
      const token = ++generation,
        selectedProvider = provider.value;
      loading = true;
      model.disabled = effort.disabled = true;
      $("#modal-submit").disabled = true;
      model.replaceChildren(new Option("Loading models…", ""));
      effort.replaceChildren(new Option("Provider default", ""));
      $("#modal-error").hidden = true;
      try {
        const result = await request(
          `/api/models?provider=${encodeURIComponent(selectedProvider)}`,
        );
        if (token !== generation || !$("#modal").open || !model.isConnected)
          return;
        models = Array.isArray(result) ? result : [];
        if (!models.length)
          throw new Error("No models were returned. Retry model discovery.");
        model.replaceChildren(
          ...models.map(
            (m) =>
              new Option(m.DisplayName || m.ID || "Provider default", m.ID),
          ),
        );
        model.value =
          selectedProvider === session.provider &&
          models.some((m) => m.ID === session.model)
            ? session.model
            : (models.find((m) => m.Default) || models[0]).ID;
        updateEfforts(
          selectedProvider === session.provider ? session.effort : "",
        );
        loading = false;
        model.disabled = effort.disabled = false;
        $("#modal-submit").disabled = false;
      } catch (error) {
        if (token !== generation || !model.isConnected) return;
        model.replaceChildren(new Option("Models unavailable", ""));
        $("#modal-error").textContent = error.message;
        $("#modal-error").hidden = false;
      }
    }
    provider.addEventListener("change", load);
    model.addEventListener("change", () => updateEfforts(effort.value));
    $("#modal-fields").append(button("Retry model discovery", load));
    load();
  }
  function renderProject() {
    const main = $("#detail");
    if (main.dataset.page === "project" && $(".document-editor")) {
      const pause = $("[data-project-pause]", main);
      if (pause)
        pause.textContent = state.paused ? "Resume agents" : "Pause agents";
      const editor = $(".document-editor");
      if (!projectDrafts.has(projectTab))
        editor.value = state[projectTab] || "";
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
  let diffRevision = 0,
    lastDiffSignature = "";
  let diffCache = null,
    diffLoading = null,
    anchor = null,
    diffStart = null,
    fullSource = false,
    diffMode = storage.get("ostraka:diff-mode") || "unified",
    reviewTarget = "",
    selectedReviewID = "",
    diffDrag = null,
    suppressDiffClick = false;
  const reviewDraftKey = (key) => `ostraka:${state.project}:review:${key}`;
  function reviewDraft(text, key, unsent) {
    text.dataset.draftKey = key;
    text.value = unsent.get(key) ?? storage.get(reviewDraftKey(key)) ?? "";
    text.addEventListener("input", () =>
      storage.set(reviewDraftKey(key), text.value),
    );
    mentionCompletion(text);
  }
  async function renderReviews(force = false) {
    if (diffDrag) return;
    if (!reviewTarget) {
      const suggested =
        current?.item.Status !== "archived" ? current?.item.ID : "";
      reviewTarget = suggested || state.reviews[0]?.item_id || "";
    }
    const main = $("#detail"),
      signature = JSON.stringify([
        state.reviews,
        state.items.map((i) => [i.ID, i.Title, i.Status]),
        reviewTarget,
        selectedReviewID,
      ]);
    if (
      !force &&
      main.dataset.page === "reviews" &&
      signature === reviewSignature
    ) {
      if (!diffCache && $("#diff", main)) await loadDiff($("#diff", main));
      return;
    }
    // Preserve unfinished thread replies and caret across server refreshes.
    const focused = document.activeElement;
    const focusKey = focused?.dataset.draftKey;
    const caret = focusKey
      ? [focused.selectionStart, focused.selectionEnd]
      : null;
    const scrollTop = main.scrollTop;
    const diffOpen = new Map(
      $$(".diff-file", main).map((node) => [node.dataset.path, node.open]),
    );
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
    const targetForm = el("form", "review-target-form"),
      targetLabel = el("label"),
      target = reviewTargetSelect("Review target");
    target.id = "review-target";
    append(targetLabel, el("span", "", "Review target"), target);
    const startReview = el("button", "primary", "Start review");
    startReview.type = "submit";
    append(targetForm, targetLabel, startReview);
    targetForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      startReview.disabled = true;
      try {
        await targetReview(target.value);
        await renderReviews(true);
        $("#diff")?.scrollIntoView({ block: "start" });
      } catch (error) {
        report(error);
      } finally {
        startReview.disabled = false;
      }
    });
    page.append(targetForm);
    if (!state.reviews.length)
      page.append(
        el(
          "div",
          "empty",
          "Choose a target above to start a review, or select code below to add your first comment.",
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
    {
      const diffSection = el("section");
      diffSection.id = "diff";
      const layoutLabel = el("label", "diff-layout"),
        layout = el("select");
      layout.setAttribute("aria-label", "Diff layout");
      layout.add(new Option("Unified", "unified"));
      layout.add(new Option("Side by side", "split"));
      layout.value = diffMode;
      layout.addEventListener("change", () => {
        diffMode = layout.value;
        storage.set("ostraka:diff-mode", diffMode);
        loadDiff(diffSection).catch(report);
      });
      append(layoutLabel, document.createTextNode("Layout "), layout);
      diffSection.append(layoutLabel);
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
          diffRevision++;
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
          "Drag across lines to select a range and comment underneath it. On touch screens, drag the line numbers; swipe the code to scroll.",
        ),
      );
      const files = el("div");
      files.id = "diff-files";
      diffSection.append(files);
      const firstReview = $(".review-card", page);
      if (firstReview) page.insertBefore(diffSection, firstReview);
      else page.append(diffSection);
      main.replaceChildren(page);
      if (focusKey) {
        const replacement = $$("textarea", page).find(
          (n) => n.dataset.draftKey === focusKey,
        );
        if (replacement) {
          replacement.focus({ preventScroll: true });
          replacement.setSelectionRange(...caret);
        }
      }
      main.scrollTop = scrollTop;
      await loadDiff(diffSection, diffOpen);
      if (focusKey) {
        const replacement = $$("textarea", page).find(
          (n) => n.dataset.draftKey === focusKey,
        );
        if (replacement && document.activeElement !== replacement) {
          replacement.focus({ preventScroll: true });
          replacement.setSelectionRange(...caret);
        }
      }
      main.scrollTop = scrollTop;
    }
  }
  async function loadDiff(section, savedOpen = null) {
    const files = $("#diff-files", section);
    if (!files) return;
    if (!diffCache) {
      if (!files.children.length)
        files.append(el("p", "muted", "Reading working tree…"));
      const version = diffRevision;
      try {
        if (!diffLoading) diffLoading = request("/api/diff");
        const result = await diffLoading;
        if (version !== diffRevision) {
          scheduleRefresh();
          return;
        }
        diffCache = result;
      } catch (e) {
        files.replaceChildren(el("p", "error", e.message));
        return;
      } finally {
        diffLoading = null;
      }
    }
    if (!section.isConnected) return;
    const signature = JSON.stringify([diffCache, fullSource, diffMode]);
    if (signature === files.dataset.signature) return;
    const opened =
      savedOpen ||
      new Map($$(".diff-file", files).map((n) => [n.dataset.path, n.open]));
    files.dataset.signature = signature;
    if (lastDiffSignature && lastDiffSignature !== JSON.stringify(diffCache))
      clearAnchor();
    lastDiffSignature = JSON.stringify(diffCache);
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
      details.dataset.path = file.Path;
      details.open = opened.get(file.Path) ?? diffCache.Files.length === 1;
      details.append(el("summary", "", `${file.Path} · ${file.Status}`));
      if (file.Binary || file.Notice)
        details.append(
          el("p", "muted", file.Binary ? "Binary file" : file.Notice),
        );
      const lines = el(
        "div",
        `diff-lines${diffMode === "split" ? " split" : ""}`,
      );
      if (diffMode === "split") {
        const heading = el("div", "diff-row diff-columns");
        append(heading, el("span", "", "Before"), el("span", "", "After"));
        lines.append(heading);
        let removed = [],
          added = [];
        const flush = () => {
          for (let n = 0; n < Math.max(removed.length, added.length); n++) {
            const row = el("div", "diff-row");
            row.append(
              removed[n] === undefined
                ? el("span", "diff-empty")
                : diffLine(file, removed[n], "old"),
            );
            row.append(
              added[n] === undefined
                ? el("span", "diff-empty")
                : diffLine(file, added[n], "new"),
            );
            lines.append(row);
          }
          removed = [];
          added = [];
        };
        file.Lines.forEach((line, index) => {
          const kind = lineKind(line);
          if (kind === "-") removed.push(index);
          else if (kind === "+") added.push(index);
          else {
            flush();
            const row = el(
              "div",
              `diff-row${!line.OldLine && !line.NewLine ? " diff-hunk" : ""}`,
            );
            if (!line.OldLine && !line.NewLine)
              row.append(el("span", "", line.Text));
            else
              append(
                row,
                line.OldLine
                  ? diffLine(file, index, "old")
                  : el("span", "diff-empty"),
                line.NewLine
                  ? diffLine(file, index, "new")
                  : el("span", "diff-empty"),
              );
            lines.append(row);
          }
        });
        flush();
      } else
        file.Lines.forEach((_, index) => lines.append(diffLine(file, index)));
      details.append(lines);
      files.append(details);
    });
    markDiffSelection();
    if (anchor) showInlineReviewComposer();
  }
  function lineKind(line) {
    return typeof line.Kind === "number"
      ? String.fromCharCode(line.Kind)
      : line.Kind;
  }
  function diffLine(file, index, side) {
    const line = file.Lines[index],
      kind = lineKind(line);
    const b = el(
      "button",
      `diff-line${kind === "+" ? " add" : kind === "-" ? " remove" : ""}`,
    );
    b.type = "button";
    b.dataset.index = index;
    b.dataset.path = file.Path;
    b.dataset.side = side || (line.NewLine ? "new" : "old");
    if (side)
      append(
        b,
        el(
          "span",
          "line-number",
          (side === "old" ? line.OldLine : line.NewLine) || "",
        ),
      );
    else
      append(
        b,
        el("span", "line-number", line.OldLine || ""),
        el("span", "line-number", line.NewLine || ""),
      );
    b.append(el("span", "code", `${kind} ${line.Text}`));
    b.disabled = !(line.NewLine || line.OldLine);
    b.title = "Drag to select lines and add a comment";
    b.addEventListener("pointerdown", (e) => {
      if (
        e.button !== 0 ||
        (e.pointerType === "touch" && !e.target.closest(".line-number"))
      )
        return;
      e.preventDefault();
      selectDiff(file, index, false, side);
      diffDrag = {
        file,
        pointerID: e.pointerId,
        side: side || (line.NewLine ? "new" : "old"),
        button: b,
      };
      b.setPointerCapture?.(e.pointerId);
      suppressDiffClick = true;
      $(".inline-review-compose")?.remove();
    });
    b.addEventListener("click", (e) => {
      if (suppressDiffClick && e.detail !== 0) {
        suppressDiffClick = false;
        return;
      }
      selectDiff(file, index, e.shiftKey, side);
      showInlineReviewComposer(true);
    });
    return b;
  }
  document.addEventListener("pointermove", (e) => {
    if (!diffDrag || e.pointerId !== diffDrag.pointerID) return;
    const hit = document
      .elementFromPoint(e.clientX, e.clientY)
      ?.closest(".diff-line");
    if (
      !hit ||
      hit.dataset.path !== diffDrag.file.Path ||
      hit.disabled ||
      (diffMode === "split" && hit.dataset.side !== diffDrag.side)
    )
      return;
    const line = diffDrag.file.Lines[+hit.dataset.index];
    if (!(diffDrag.side === "old" ? line.OldLine : line.NewLine)) return;
    selectDiff(diffDrag.file, +hit.dataset.index, true, diffDrag.side);
    const scroller = hit.closest(".diff-lines"),
      bounds = scroller.getBoundingClientRect();
    if (e.clientY < bounds.top + 24) scroller.scrollTop -= 20;
    else if (e.clientY > bounds.bottom - 24) scroller.scrollTop += 20;
  });
  document.addEventListener("pointerup", (e) => {
    if (!diffDrag || e.pointerId !== diffDrag.pointerID) return;
    const drag = diffDrag;
    diffDrag = null;
    drag.button.releasePointerCapture?.(e.pointerId);
    showInlineReviewComposer(true);
    scheduleRefresh();
    setTimeout(() => {
      suppressDiffClick = false;
    }, 0);
  });
  document.addEventListener("pointercancel", () => {
    diffDrag = null;
    suppressDiffClick = false;
  });
  function reviewTargetSelect(label) {
    const select = el("select");
    select.setAttribute("aria-label", label);
    select.dataset.reviewTarget = "";
    select.add(new Option("Choose an item…", ""));
    state.items
      .filter((i) => i.Status !== "archived")
      .forEach((i) => select.add(new Option(`${i.ID} · ${i.Title}`, i.ID)));
    select.value = reviewTarget;
    select.addEventListener("change", () => {
      reviewTarget = select.value;
      selectedReviewID = "";
      $$("[data-review-target]").forEach((n) => (n.value = reviewTarget));
    });
    return select;
  }
  async function targetReview(itemID) {
    if (!itemID || !itemByID(itemID) || itemByID(itemID).Status === "archived")
      throw new Error("Choose a live item as the review target.");
    reviewTarget = itemID;
    let review =
      state.reviews.find(
        (r) => r.id === selectedReviewID && r.item_id === itemID,
      ) || state.reviews.find((r) => r.item_id === itemID);
    if (!review) {
      review = await action("review-create", { id: itemID }, false);
      state.reviews.push(review);
    }
    selectedReviewID = review.id;
    return review;
  }
  function markDiffSelection() {
    $$(".diff-line").forEach((n) => {
      const from = Math.min(diffStart?.index ?? -1, diffStart?.end ?? -1),
        to = Math.max(diffStart?.index ?? -1, diffStart?.end ?? -1);
      n.classList.toggle(
        "chosen",
        !!anchor &&
          n.dataset.path === diffStart?.path &&
          +n.dataset.index >= from &&
          +n.dataset.index <= to &&
          (diffMode !== "split" || n.dataset.side === anchor.side),
      );
    });
  }
  function showInlineReviewComposer(reveal = false) {
    if (!anchor || diffDrag) return;
    const selected = $$(".diff-line.chosen").at(-1);
    if (!selected) return;
    const key = JSON.stringify(anchor),
      existing = $(".inline-review-compose");
    if (existing?.dataset.selection === key) return;
    existing?.remove();
    const form = el("form", "inline-review-compose");
    form.dataset.selection = key;
    const selection = { ...anchor },
      text = el("textarea"),
      target = reviewTargetSelect("Comment target");
    text.setAttribute("aria-label", "Comment on selected lines");
    text.placeholder = "Comment on these lines…";
    reviewDraft(text, "inline-code", new Map());
    const controls = el("div", "toolbar"),
      send = el("button", "primary", "Add comment");
    send.type = "submit";
    append(
      controls,
      target,
      button("Cancel", () => {
        storage.remove(reviewDraftKey("inline-code"));
        clearAnchor();
      }),
      send,
    );
    append(
      form,
      el(
        "p",
        "anchor",
        `${selection.file} · ${selection.side} lines ${selection.start_line}–${selection.end_line}`,
      ),
      text,
      controls,
    );
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (!text.value.trim()) return;
      send.disabled = true;
      try {
        const review = await targetReview(target.value);
        await action("review-thread", {
          review_id: review.id,
          content: text.value,
          anchor: selection,
        });
        storage.remove(reviewDraftKey("inline-code"));
        text.value = "";
        clearAnchor();
        toast("Review comment added");
      } catch (error) {
        report(error);
      } finally {
        send.disabled = false;
      }
    });
    (selected.closest(".diff-row") || selected).after(form);
    if (reveal) form.scrollIntoView({ block: "nearest", inline: "nearest" });
  }
  function clearAnchor() {
    anchor = null;
    diffStart = null;
    $(".inline-review-compose")?.remove();
    $$(".diff-line.chosen").forEach((n) => n.classList.remove("chosen"));
    $$("[data-anchor-label]").forEach(
      (n) =>
        (n.textContent =
          "General comment · select a diff line below to attach code"),
    );
  }
  function selectDiff(file, index, extend, requestedSide) {
    const line = file.Lines[index],
      side = requestedSide || (line.NewLine ? "new" : "old");
    if (!(side === "old" ? line.OldLine : line.NewLine)) return;
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
    diffStart = { path: file.Path, side, index: start, end: index };
    markDiffSelection();
    $$("[data-anchor-label]").forEach(
      (n) =>
        (n.textContent = `${anchor.file} · ${side} lines ${anchor.start_line}–${anchor.end_line}`),
    );
  }
  $("#detail").addEventListener("focusout", (e) => {
    if (
      (view === "reviews" && e.target.matches("textarea")) ||
      e.target.id === "composer"
    )
      scheduleRefresh();
  });
  $("#search").addEventListener("input", renderList);
  $("#group-filter").addEventListener("change", renderList);
  $("#status-filter").addEventListener("change", renderList);
  $("#detail").addEventListener("focusout", (e) => {
    if (e.target.closest(".detail-header")) scheduleRefresh();
  });
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
    diffRevision++;
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

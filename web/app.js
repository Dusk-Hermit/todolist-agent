/* TodoList Agent — front-end. No external dependencies. */
(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const S = {
    data: null,
    filter: "all",
    viewMode: "cards",
    dismissed: false,
    editingId: null,
    newParentId: null,
    tokenVisible: false,
    lastTodosSig: "",
    panelOpen: false,
    panelTab: "journal",
    settingsOpen: false,
    taskOpen: false,
    localSentAt: 0,
    runningTimer: null,
    lastRunSecond: -1,
    journalEntries: [],
    journalBeforeAt: 0,
    journalHasMore: false,
    journalQuery: "",
    journalTotal: 0,
    journalLoading: false,
    lastMessageTotal: -1,
    collapsedGroups: {},
    dragId: null,
    dropTarget: null,
    suppressClick: false,
  };

  const PRIORITY = {
    pinned: { label: "长期", cls: "pinned", rank: 0 },
    high: { label: "高", cls: "high", rank: 1 },
    normal: { label: "中", cls: "", rank: 2 },
    low: { label: "低", cls: "low", rank: 3 },
  };
  const PRIORITY_ORDER = ["pinned", "high", "normal", "low"];
  const PRIORITY_BAND_LABEL = { pinned: "置顶 · 长期", high: "高优先级", normal: "中优先级", low: "低优先级" };
  const STATUS = {
    todo: { label: "待办", rank: 1 },
    doing: { label: "进行中", rank: 0 },
    done: { label: "已完成", rank: 2 },
    dropped: { label: "已丢弃", rank: 3 },
  };
  const FILTERS = ["all", "open", "doing", "done", "dropped"];

  // ---------------------------------------------------------------- helpers

  function esc(v) {
    return String(v == null ? "" : v).replace(/[&<>"']/g, (c) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[c]));
  }

  function errText(e) {
    if (!e) return "未知错误";
    if (typeof e === "string") return e;
    return e.message || String(e);
  }

  function clamp(s, n) {
    s = String(s == null ? "" : s);
    return s.length > n ? s.slice(0, n - 1) + "…" : s;
  }

  function plainText(s) {
    return String(s == null ? "" : s)
      .replace(/```[\s\S]*?```/g, " ")
      .replace(/`([^`]*)`/g, "$1")
      .replace(/\*\*([^*]+)\*\*/g, "$1")
      .replace(/^\s*\|.*\|\s*$/gm, " ")
      .replace(/^\s*[-:| ]{3,}\s*$/gm, " ")
      .replace(/^#{1,6}\s*/gm, "")
      .replace(/\s+/g, " ")
      .trim();
  }

  function fmtClock(ts) {
    if (!ts) return "";
    try {
      return new Intl.DateTimeFormat("zh-CN", {
        hour: "2-digit", minute: "2-digit", hour12: false,
      }).format(new Date(ts));
    } catch (_) { return ""; }
  }

  function fmtDate(ts) {
    try {
      return new Intl.DateTimeFormat("zh-CN", {
        month: "short", day: "numeric",
      }).format(new Date(ts));
    } catch (_) { return ""; }
  }

  function dayKey(ts) {
    const d = new Date(ts);
    return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
  }

  function dayLabel(ts) {
    const now = new Date();
    const d = new Date(ts);
    const today = dayKey(now.getTime());
    const key = dayKey(ts);
    if (key === today) return "今天";
    const y = new Date(now.getTime() - 86400000);
    if (key === dayKey(y.getTime())) return "昨天";
    return fmtDate(ts);
  }

  function relTime(ts) {
    if (!ts) return "";
    const diff = Date.now() - ts;
    if (diff < 45 * 1000) return "刚刚";
    if (diff < 60 * 60 * 1000) return `${Math.max(1, Math.floor(diff / 60000))} 分钟前`;
    const d = new Date(ts);
    const now = new Date();
    if (dayKey(ts) === dayKey(now.getTime())) return fmtClock(ts);
    if (dayKey(ts) === dayKey(now.getTime() - 86400000)) return "昨天 " + fmtClock(ts);
    return fmtDate(ts) + " " + fmtClock(ts);
  }

  function fmtDuration(ms) {
    if (!ms || ms < 0) ms = 0;
    const total = Math.floor(ms / 1000);
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    if (h > 0) return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
    return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  }

  function cwdShort(p) {
    if (!p) return "";
    const parts = String(p).replace(/\\/g, "/").split("/").filter(Boolean);
    if (parts.length <= 2) return p;
    return "…/" + parts.slice(-2).join("/");
  }

  async function call(name, ...args) {
    const fn = window[name];
    if (typeof fn !== "function") throw new Error("界面接口未就绪：" + name);
    return await fn(...args);
  }

  function connected() {
    return !!(S.data && S.data.dsh && S.data.dsh.connected && S.data.dsh.sessionId);
  }

  function currentSession() {
    return (S.data && S.data.dsh && S.data.dsh.sessionId) || "";
  }

  // ---------------------------------------------------------------- toast

  const TOAST_ICON = {
    success: "i-check",
    error: "i-close",
    agent: "i-spark",
    info: "i-dot",
  };

  function showToast(kind, text) {
    const root = $("toastRoot");
    if (!root || !text) return;
    while (root.children.length >= 4) root.removeChild(root.firstChild);
    const el = document.createElement("div");
    el.className = "toast " + (kind || "info");
    el.innerHTML = `<svg class="icon"><use href="#${TOAST_ICON[kind] || "i-dot"}"/></svg><span>${esc(text)}</span>`;
    root.appendChild(el);
    const ttl = kind === "error" ? 5200 : 3200;
    setTimeout(() => {
      el.classList.add("out");
      setTimeout(() => el.remove(), 220);
    }, ttl);
  }

  // ---------------------------------------------------------------- state

  function applyState(s) {
    if (!s) return;
    if (!s.todos) s.todos = [];
    if (!s.events) s.events = [];
    if (!s.messages) s.messages = [];
    if (!s.sessions) s.sessions = [];
    if (!s.prefs) s.prefs = {};
    if (!s.prefs.onboardedSessions) s.prefs.onboardedSessions = {};
    S.data = s;
    if (FILTERS.includes(s.prefs.filter)) S.filter = s.prefs.filter;
    if (s.prefs.viewMode === "compact" || s.prefs.viewMode === "cards") {
      S.viewMode = s.prefs.viewMode;
    }

    updatePinButton();
    updateFilterButtons();
    renderAll();
    updateSetupBanner();
    if (S.settingsOpen) updateSettings(false);
    const totalChanged = s.messageTotal !== S.lastMessageTotal;
    S.lastMessageTotal = s.messageTotal;
    if (S.panelOpen && S.panelTab === "history") renderHistory();
    if (S.panelOpen && S.panelTab === "journal" && !S.journalLoading && totalChanged) {
      loadJournal(true);
    }
    updateRunningTimer();
  }

  function applyStatus(st) {
    if (!S.data || !st) return;
    const wasRunning = !!(S.data.dsh && S.data.dsh.running);
    S.data.dsh = st;
    renderStatus();
    renderRunning();
    updateSetupBanner();
    if (S.settingsOpen) updateSettings(false);
    if (!wasRunning && st.running) {
      // Scroll the list to the top so the running task is visible.
      const list = $("list");
      if (list) list.scrollTop = 0;
    }
  }

  function renderAll() {
    renderStatus();
    renderTodos(true);
    renderRunning();
    renderSessionChip();
    renderSendState();
    updatePinButton();
    updateViewButton();
  }

  // ---------------------------------------------------------------- status

  function renderStatus() {
    const d = (S.data && S.data.dsh) || {};
    const card = $("statusCard");
    const title = $("statusTitle");
    const sub = $("statusSub");
    const stat = $("statusStat");
    const brandSub = $("brandSub");

    let state = "connecting";
    let t = "正在连接 DSH…";
    let s = "读取本地会话";
    let m = "—";

    if (d.connected) {
      state = "connected";
      t = d.sessionTitle ? `已连接 · ${d.sessionTitle}` : "已连接 DeepSeek Harness";
      s = [cwdShort(d.cwd), d.running ? "正在处理" : d.sending ? "发送中" : "空闲"].filter(Boolean).join(" · ");
      m = d.turns ? `${d.turns} 轮` : "在线";
      if (d.running) m = "处理中";
    } else if (d.connecting) {
      state = "connecting";
      t = "正在连接 DSH…";
      s = "扫描本地端口并校验会话";
      m = "连接中";
    } else if (d.error) {
      state = "error";
      t = "未连接 DSH";
      s = clamp(d.error, 70);
      m = "重试";
    } else {
      state = "error";
      t = "未连接 DSH";
      s = "请确认 DeepSeek Harness 正在运行";
      m = "重试";
    }

    card.dataset.state = state;
    title.textContent = t;
    title.title = t;
    sub.textContent = s;
    sub.title = s;
    stat.textContent = m;
    brandSub.textContent = d.connected
      ? (d.running ? "DSH 正在处理当前任务" : "与当前 DSH 会话同步")
      : (d.connecting ? "正在查找本地会话…" : "等待 DeepSeek Harness");
  }

  function renderSessionChip() {
    const chip = $("sessionChip");
    if (!chip) return;
    const d = (S.data && S.data.dsh) || {};
    if (d.connected && d.sessionTitle) {
      chip.textContent = "会话：" + d.sessionTitle;
      chip.title = (d.cwd ? d.cwd + "\n" : "") + "点击切换会话";
    } else if (d.connected) {
      chip.textContent = "已连接 · 选择会话";
      chip.title = "点击选择会话";
    } else {
      chip.textContent = "未连接 DSH";
      chip.title = "点击打开设置";
    }
  }

  // ---------------------------------------------------------------- running

  function renderRunning() {
    const el = $("running");
    const d = (S.data && S.data.dsh) || {};
    const show = !!(d.running || d.sending);
    el.classList.toggle("hidden", !show);
    if (!show) {
      S.lastRunSecond = -1;
      return;
    }
    const sub = $("runningSub");
    if (d.sending && !d.running) {
      sub.textContent = "正在把消息交给 DSH…";
    } else if (d.lastPrompt) {
      sub.textContent = clamp(d.lastPrompt, 60);
    } else {
      sub.textContent = "等待 DSH 响应";
    }
    updateRunningTime();
  }

  function updateRunningTime() {
    const d = (S.data && S.data.dsh) || {};
    const el = $("runningTime");
    if (!el) return;
    if (!(d.running || d.sending)) return;
    let start = d.runningSince || S.localSentAt;
    if (!start) start = Date.now();
    const sec = Math.floor((Date.now() - start) / 1000);
    if (sec === S.lastRunSecond) return;
    S.lastRunSecond = sec;
    el.textContent = fmtDuration(sec * 1000);
  }

  function updateRunningTimer() {
    if (S.runningTimer) return;
    S.runningTimer = setInterval(updateRunningTime, 500);
  }

  // ---------------------------------------------------------------- todos

  function allTodos() {
    return (S.data && S.data.todos) || [];
  }

  function childrenOf(id) {
    const all = allTodos();
    const rank = { doing: 0, todo: 1, done: 2, dropped: 3 };
    return all.filter((t) => t.parentId === id).sort((a, b) => {
      const ra = rank[a.status] == null ? 1 : rank[a.status];
      const rb = rank[b.status] == null ? 1 : rank[b.status];
      if (ra !== rb) return ra - rb;
      const pa = typeof a.position === "number" ? a.position : 0;
      const pb = typeof b.position === "number" ? b.position : 0;
      if (pa !== pb) return pa - pb;
      return (a.createdAt || 0) - (b.createdAt || 0);
    });
  }

  function priorityKey(t) {
    return t && PRIORITY[t.priority] ? t.priority : "normal";
  }

  function priorityDividerHTML(key, count) {
    const label = PRIORITY_BAND_LABEL[key] || PRIORITY_BAND_LABEL.normal;
    return `<div class="prio-divider prio-${key}" data-priority="${key}">
      <span class="prio-name">${label}</span>
      <span class="prio-line"></span>
      <span class="prio-count">${count}</span>
    </div>`;
  }

  function parentOf(t) {
    if (!t || !t.parentId) return null;
    return allTodos().find((p) => p.id === t.parentId) || null;
  }

  function rootsOf(list) {
    return list.filter((t) => {
      if (!t.parentId) return true;
      // Orphaned children behave as roots rather than disappearing.
      return !list.some((p) => p.id === t.parentId);
    });
  }

  function visibleTodos() {
    const all = allTodos();
    let list = rootsOf(all);
    if (S.filter === "open") list = list.filter((t) => t.status === "todo" || t.status === "doing");
    else if (S.filter === "doing") list = list.filter((t) => t.status === "doing");
    else if (S.filter === "done") list = list.filter((t) => t.status === "done");
    else if (S.filter === "dropped") list = list.filter((t) => t.status === "dropped");
    const rank = { doing: 0, todo: 1, done: 2, dropped: 3 };
    return list.slice().sort((a, b) => {
      const ra = rank[a.status] == null ? 1 : rank[a.status];
      const rb = rank[b.status] == null ? 1 : rank[b.status];
      if (ra !== rb) return ra - rb;
      if (a.status === "done" && b.status === "done") {
        return (b.completedAt || b.updatedAt || 0) - (a.completedAt || a.updatedAt || 0);
      }
      if (a.status === "dropped" && b.status === "dropped") {
        return (b.droppedAt || b.updatedAt || 0) - (a.droppedAt || a.updatedAt || 0);
      }
      const pa = (PRIORITY[a.priority] || PRIORITY.normal).rank;
      const pb = (PRIORITY[b.priority] || PRIORITY.normal).rank;
      if (pa !== pb) return pa - pb;
      const oa = typeof a.position === "number" ? a.position : 0;
      const ob = typeof b.position === "number" ? b.position : 0;
      if (oa !== ob) return oa - ob;
      return (b.createdAt || 0) - (a.createdAt || 0);
    });
  }

  function subtaskRowHTML(t) {
    const p = PRIORITY[t.priority] || PRIORITY.normal;
    const done = t.status === "done";
    const dropped = t.status === "dropped";
    const doing = t.status === "doing";
    return `<div class="subtask prio-${priorityKey(t)} ${done ? "done" : ""} ${doing ? "doing" : ""} ${dropped ? "dropped" : ""}" data-id="${esc(t.id)}">
      <button class="scheck" data-act="toggle" aria-label="${done || dropped ? "重新打开" : "完成子任务"}" title="${done || dropped ? "重新打开" : "完成子任务"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      <span class="subtask-num">${esc(t.number || "")}</span>
      <div class="subtask-main">
        <div class="stitle" data-act="edit">${esc(t.title)}</div>
        ${t.notes ? `<div class="snotes">${esc(t.notes)}</div>` : ""}
      </div>
      <div class="smeta">
        <span class="pri-${p.cls || "normal"}">${p.label}</span>
        <span>${esc(relTime(t.updatedAt || t.createdAt))}</span>
      </div>
      <div class="sactions">
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        ${dropped ? "" : '<button class="icon-btn" data-act="drop" title="丢弃" aria-label="丢弃"><svg class="icon"><use href="#i-drop"/></svg></button>'}
        <button class="icon-btn del" data-act="del" title="删除" aria-label="删除"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </div>`;
  }

  function todoHTML(t) {
    const p = PRIORITY[t.priority] || PRIORITY.normal;
    const done = t.status === "done";
    const doing = t.status === "doing";
    const dropped = t.status === "dropped";
    const children = (done || dropped) ? [] : childrenOf(t.id);
    const openChildren = children.filter((c) => c.status !== "done" && c.status !== "dropped").length;
    const statusPill = doing
      ? '<span class="pill doing">进行中</span>'
      : dropped
        ? '<span class="pill dropped">已丢弃</span>'
        : "";
    const sourcePill = t.source === "agent" ? '<span class="pill agent">DSH</span>' : "";
    const priorityPill = `<span class="pill ${p.cls}">${p.label}</span>`;
    const subPill = children.length ? `<span class="pill subcount">${children.length} 子任务</span>` : "";
    const notes = t.notes ? `<div class="todo-notes">${esc(t.notes)}</div>` : "";
    const reason = dropped && t.dropReason ? `<div class="todo-notes">丢弃原因：${esc(t.dropReason)}</div>` : "";
    const time = relTime(t.updatedAt || t.createdAt);
    const subtasks = children.length ? `<div class="subtasks">
      <div class="subtask-top">
        <span>子任务</span>
        <span class="subtask-progress">${children.length - openChildren}/${children.length}</span>
      </div>
      ${children.map(subtaskRowHTML).join("")}
      <button class="sub-add" data-act="sub-add" data-parent-id="${esc(t.id)}">+ 添加子任务</button>
    </div>` : "";
    return `<article class="todo prio-${priorityKey(t)} ${done ? "done" : ""} ${doing ? "doing" : ""} ${dropped ? "dropped" : ""}" data-id="${esc(t.id)}" draggable="${t.status === "todo" ? "true" : "false"}">
      <button class="check" data-act="toggle" aria-label="${done ? "标记为待办" : "标记为完成"}" title="${done ? "标记为待办" : "标记为完成"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      <span class="todo-num">${esc(t.number || "")}</span>
      <div class="todo-main">
        <div class="todo-title" data-act="edit" title="点击编辑">${esc(t.title)}</div>
        ${notes}${reason}
        <div class="todo-meta">
          ${priorityPill}${statusPill}${sourcePill}${subPill}
          <span>${esc(time)}</span>
        </div>
        ${subtasks}
      </div>
      <div class="todo-actions">
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        ${done || dropped ? "" : `<button class="icon-btn" data-act="sub-add" data-parent-id="${esc(t.id)}" title="添加子任务" aria-label="添加子任务"><svg class="icon"><use href="#i-plus"/></svg></button>`}
        ${dropped ? "" : '<button class="icon-btn" data-act="drop" title="丢弃" aria-label="丢弃"><svg class="icon"><use href="#i-drop"/></svg></button>'}
        <button class="icon-btn del" data-act="del" title="删除" aria-label="删除"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </article>`;
  }

  function compactRowHTML(t, isChild) {
    const p = PRIORITY[t.priority] || PRIORITY.normal;
    const done = t.status === "done";
    const dropped = t.status === "dropped";
    const doing = t.status === "doing";
    const checked = done || dropped;
    const children = isChild ? [] : childrenOf(t.id);
    const sub = children.length ? `<span>· ${children.filter((c) => c.status === "done" || c.status === "dropped").length}/${children.length}</span>` : "";
    return `<div class="crow prio-${priorityKey(t)} ${isChild ? "child" : ""} ${done ? "done" : ""} ${doing ? "doing" : ""} ${dropped ? "dropped" : ""}" data-id="${esc(t.id)}" draggable="${!isChild && t.status === "todo" ? "true" : "false"}">
      <button class="ccheck" data-act="toggle" aria-label="${checked ? "重新打开" : "标记完成"}" title="${checked ? "重新打开" : "标记完成"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      <span class="crow-num">${esc(t.number || "")}</span>
      <div class="crow-main">
        <div class="crow-title" data-act="edit" title="${esc(t.notes || t.title)}">${esc(t.title)}</div>
        <div class="crow-meta">
          <span class="pri-${p.cls || "normal"}">${p.label}</span>
          ${t.source === "agent" ? '<span class="agent">DSH</span>' : ""}
          ${sub}
          <span>${esc(relTime(t.updatedAt || t.createdAt))}</span>
        </div>
      </div>
      <div class="crow-actions">
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        ${isChild || done || dropped ? "" : `<button class="icon-btn" data-act="sub-add" data-parent-id="${esc(t.id)}" title="添加子任务" aria-label="添加子任务"><svg class="icon"><use href="#i-plus"/></svg></button>`}
        ${dropped ? "" : '<button class="icon-btn" data-act="drop" title="丢弃" aria-label="丢弃"><svg class="icon"><use href="#i-drop"/></svg></button>'}
        <button class="icon-btn del" data-act="del" title="删除" aria-label="删除"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </div>`;
  }

  function cardsHTML(items) {
    let html = "";
    let band = "";
    for (const t of items) {
      if (t.status === "todo") {
        const key = priorityKey(t);
        if (key !== band) {
          const count = items.filter((x) => x.status === "todo" && priorityKey(x) === key).length;
          html += priorityDividerHTML(key, count);
          band = key;
        }
      } else {
        band = "";
      }
      html += todoHTML(t);
    }
    return html;
  }

  function compactGroups(items) {
    const order = ["doing", "todo", "done", "dropped"];
    const allowed = S.filter === "all" || S.filter === "open"
      ? order
      : S.filter === "doing" ? ["doing"]
        : S.filter === "done" ? ["done"]
          : S.filter === "dropped" ? ["dropped"]
            : ["todo", "doing"];
    return allowed
      .map((status) => ({ status, items: items.filter((t) => t.status === status) }))
      .filter((g) => g.items.length > 0);
  }

  function groupCollapsed(status) {
    if (S.filter !== "all" && S.filter !== "open") return false;
    if (status === "done" || status === "dropped") {
      return S.collapsedGroups[status] !== false;
    }
    return S.collapsedGroups[status] === true;
  }

  function compactHTML(items) {
    const groups = compactGroups(items);
    return groups.map((g) => {
      const collapsed = groupCollapsed(g.status);
      const rowFor = (t) => {
        const children = (t.status === "done" || t.status === "dropped") ? [] : childrenOf(t.id);
        return compactRowHTML(t, false) + children.map((c) => compactRowHTML(c, true)).join("");
      };
      let rows;
      if (g.status === "todo") {
        let band = "";
        rows = g.items.map((t) => {
          const key = priorityKey(t);
          let prefix = "";
          if (key !== band) {
            const count = g.items.filter((x) => priorityKey(x) === key).length;
            prefix = priorityDividerHTML(key, count);
            band = key;
          }
          return prefix + rowFor(t);
        }).join("");
      } else {
        rows = g.items.map(rowFor).join("");
      }
      return `<section class="cgroup ${collapsed ? "collapsed" : ""}" data-group="${g.status}">
        <div class="cgroup-head" data-act="group" data-group="${g.status}">
          <svg class="icon chevron"><use href="#i-chevron"/></svg>
          <span>${STATUS[g.status].label}</span>
          <span class="cgroup-count">${g.items.length}</span>
        </div>
        <div class="cgroup-body">${rows}</div>
      </section>`;
    }).join("");
  }

  function dragHost(target) {
    if (!target || target.closest(".subtask, .crow.child")) return null;
    return target.closest("article.todo, .crow");
  }

  function dropTargetAt(target) {
    const divider = target && target.closest(".prio-divider");
    if (divider) {
      return { kind: "divider", priority: divider.dataset.priority, el: divider };
    }
    const host = dragHost(target);
    if (!host) return null;
    const t = findTodo(host.dataset.id);
    if (!t || t.status !== "todo") return null;
    return { kind: "task", id: t.id, priority: priorityKey(t), el: host };
  }

  function clearDropHints() {
    document.querySelectorAll(".drop-before, .drop-after, .drop-over").forEach((el) => {
      el.classList.remove("drop-before", "drop-after", "drop-over");
    });
  }

  function onDragStart(e) {
    const host = dragHost(e.target);
    if (!host) {
      e.preventDefault();
      return;
    }
    S.dragId = host.dataset.id;
    S.dropTarget = null;
    try {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", S.dragId);
    } catch (_) {}
    setTimeout(() => host.classList.add("dragging"), 0);
  }

  function onDragOver(e) {
    if (!S.dragId) return;
    const target = dropTargetAt(e.target);
    if (!target) return;
    if (target.kind === "task" && target.id === S.dragId) {
      clearDropHints();
      S.dropTarget = null;
      return;
    }
    e.preventDefault();
    try {
      e.dataTransfer.dropEffect = "move";
    } catch (_) {}
    clearDropHints();
    if (target.kind === "divider") {
      target.el.classList.add("drop-over");
      S.dropTarget = { kind: "divider", priority: target.priority };
    } else {
      const r = target.el.getBoundingClientRect();
      const after = e.clientY > r.top + r.height / 2;
      target.el.classList.add(after ? "drop-after" : "drop-before");
      S.dropTarget = { kind: "task", id: target.id, priority: target.priority, after };
    }
  }

  function onDragLeave(e) {
    if (!S.dragId) return;
    if (e.relatedTarget && e.currentTarget.contains(e.relatedTarget)) return;
    clearDropHints();
    S.dropTarget = null;
  }

  function onDragEnd() {
    clearDropHints();
    document.querySelectorAll(".dragging").forEach((el) => el.classList.remove("dragging"));
    S.dragId = null;
    S.dropTarget = null;
  }

  function onDrop(e) {
    if (!S.dragId || !S.dropTarget) return;
    e.preventDefault();
    const id = S.dragId;
    const target = S.dropTarget;
    clearDropHints();
    document.querySelectorAll(".dragging").forEach((el) => el.classList.remove("dragging"));
    S.dragId = null;
    S.dropTarget = null;
    if (target.kind === "task" && (!target.id || target.id === id)) return;
    S.suppressClick = true;
    setTimeout(() => {
      S.suppressClick = false;
    }, 260);
    const beforeId = target.kind === "task" ? target.id : "";
    call("todo_moveTodo", id, target.priority, beforeId, !!target.after).catch((err) => {
      showToast("error", errText(err));
    });
  }

  function emptyCopy() {
    const all = (S.data && S.data.todos) || [];
    let title = "清单还是空的";
    let sub = "在下面写下想法，DSH 会把它变成可执行的任务。";
    if (all.length > 0 && S.filter === "open") {
      title = "没有未完成的任务";
      sub = "所有任务都完成了。可以在「完成」里回看。";
    } else if (all.length > 0 && S.filter === "doing") {
      title = "没有进行中的任务";
      sub = "DSH 开始处理任务时会自动把它移到这里。";
    } else if (all.length > 0 && S.filter === "done") {
      title = "还没有完成的任务";
      sub = "完成任务后，它会带着完成时间出现在这里。";
    } else if (all.length > 0 && S.filter === "dropped") {
      title = "没有丢弃的任务";
      sub = "说「不做了 / 算了 / 丢弃」时，任务会归档到这里，而不是消失。";
    }
    return { title, sub };
  }

  function renderTodos(force) {
    const list = $("list");
    const empty = $("empty");
    const items = visibleTodos();
    const compact = S.viewMode === "compact";
    const sig = [
      compact ? "compact" : "cards",
      S.filter,
      JSON.stringify(S.collapsedGroups),
      allTodos().map((t) => [t.id, t.parentId, t.status, t.title, t.notes, t.priority, t.updatedAt, t.completedAt, t.droppedAt, t.dropReason, t.position, t.source].join("\u0001")).join("\u0002"),
    ].join("\u0003");
    if (!force && sig === S.lastTodosSig) {
      renderCounts();
      return;
    }
    S.lastTodosSig = sig;
    list.classList.toggle("compact", compact);
    list.innerHTML = compact ? compactHTML(items) : cardsHTML(items);

    const isEmpty = items.length === 0;
    list.classList.toggle("hidden", isEmpty);
    empty.classList.toggle("hidden", !isEmpty);
    if (isEmpty) {
      const copy = emptyCopy();
      $("emptyTitle").textContent = copy.title;
      $("emptySub").textContent = copy.sub;
      $("emptyAdd").textContent = (S.data.todos || []).length > 0 && S.filter !== "all" ? "查看全部任务" : "手动添加第一个任务";
    }
    renderCounts();
  }

  function renderCounts() {
    // Filters count top-level items; children are represented by the parent's
    // progress (e.g. 1/4) instead of inflating every status bucket.
    const all = rootsOf(allTodos());
    const open = all.filter((t) => t.status === "todo" || t.status === "doing").length;
    const doing = all.filter((t) => t.status === "doing").length;
    const done = all.filter((t) => t.status === "done").length;
    const dropped = all.filter((t) => t.status === "dropped").length;
    $("countAll").textContent = all.length;
    $("countOpen").textContent = open;
    $("countDoing").textContent = doing;
    $("countDone").textContent = done;
    $("countDropped").textContent = dropped;
  }

  function updateFilterButtons() {
    document.querySelectorAll(".filter").forEach((b) => {
      b.classList.toggle("active", b.dataset.filter === S.filter);
    });
  }

  function updateViewButton() {
    const compact = S.viewMode === "compact";
    const btn = $("viewBtn");
    btn.classList.toggle("active", compact);
    btn.setAttribute("aria-pressed", compact ? "true" : "false");
    btn.title = compact ? "切换到卡片模式" : "切换到紧凑模式";
    const use = $("viewIcon").querySelector("use");
    use.setAttribute("href", compact ? "#i-grid" : "#i-list");
  }

  function setViewMode(mode) {
    S.viewMode = mode === "compact" ? "compact" : "cards";
    updateViewButton();
    const cp = $("compactPref");
    if (cp) cp.checked = S.viewMode === "compact";
    renderTodos(true);
    savePrefs();
  }

  function findTodo(id) {
    return ((S.data && S.data.todos) || []).find((t) => t.id === id);
  }

  async function toggleTodo(id) {
    const t = findTodo(id);
    if (!t) return;
    const next = (t.status === "done" || t.status === "dropped") ? "todo" : "done";
    try {
      await call("todo_setTodoStatus", id, next);
      showToast("success", next === "done" ? "已完成：" + clamp(t.title, 30) : "已重新打开");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function dropTodoAction(id) {
    const t = findTodo(id);
    if (!t) return;
    const reason = window.prompt(`丢弃「${t.title}」\n\n可以写一句原因（可留空）：`, "");
    if (reason === null) return;
    try {
      await call("todo_dropTodo", id, reason.trim());
      showToast("info", "已丢弃，可在「丢弃」里找回");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function deleteTodo(id) {
    const t = findTodo(id);
    if (!t) return;
    const n = childrenOf(t.id).length;
    const suffix = n ? `\n\n它还有 ${n} 个子任务，会一起删除。` : "";
    if (!window.confirm(`删除「${t.title}」？\n\n如果只是不想做了，建议用「丢弃」保留记录。${suffix}`)) return;
    try {
      await call("todo_deleteTodo", id);
      showToast("info", "已删除任务");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  // ---------------------------------------------------------------- review panel

  function openPanel(tab) {
    S.panelOpen = true;
    S.panelTab = tab === "history" ? "history" : "journal";
    $("historyPanel").classList.add("open");
    $("historyPanel").setAttribute("aria-hidden", "false");
    $("scrim").classList.remove("hidden");
    switchPanelTab(S.panelTab);
  }

  function closePanel() {
    S.panelOpen = false;
    $("historyPanel").classList.remove("open");
    $("historyPanel").setAttribute("aria-hidden", "true");
    if (!S.settingsOpen && !S.taskOpen) $("scrim").classList.add("hidden");
  }

  function switchPanelTab(tab) {
    S.panelTab = tab === "history" ? "history" : "journal";
    document.querySelectorAll(".panel-tab").forEach((b) => {
      b.classList.toggle("active", b.dataset.panel === S.panelTab);
    });
    const journal = S.panelTab === "journal";
    $("journalView").classList.toggle("hidden", !journal);
    $("historyView").classList.toggle("hidden", journal);
    $("panelKicker").textContent = journal ? "Journal" : "Activity";
    $("panelTitle").textContent = journal ? "想法回顾" : "操作记录";
    if (journal) loadJournal(true);
    else renderHistory();
  }

  async function loadJournal(reset) {
    if (S.journalLoading) return;
    S.journalLoading = true;
    try {
      if (reset) {
        S.journalEntries = [];
        S.journalBeforeAt = 0;
        S.journalHasMore = false;
      }
      const page = await call("todo_journal", S.journalBeforeAt, 40, S.journalQuery);
      if (!page) return;
      const items = Array.isArray(page.items) ? page.items : [];
      S.journalEntries = reset ? items : S.journalEntries.concat(items);
      S.journalHasMore = !!page.hasMore;
      S.journalTotal = page.total || S.journalEntries.length;
      if (items.length) S.journalBeforeAt = items[items.length - 1].at;
      renderJournal();
    } catch (e) {
      showToast("error", "读取想法回顾失败：" + errText(e));
    } finally {
      S.journalLoading = false;
    }
  }

  function journalDate(ts) {
    try {
      return new Intl.DateTimeFormat("zh-CN", {
        year: "numeric", month: "2-digit", day: "2-digit",
        hour: "2-digit", minute: "2-digit", hour12: false,
      }).format(new Date(ts));
    } catch (_) { return relTime(ts); }
  }

  function renderJournal() {
    const root = $("journalList");
    if (!root) return;
    const q = S.journalQuery;
    $("journalSummary").textContent = S.journalTotal
      ? `${S.journalTotal} 条想法${q ? " · 搜索「" + q + "」" : ""}`
      : (q ? "没有匹配的想法" : "还没有想法");
    $("journalMore").classList.toggle("hidden", !S.journalHasMore);
    if (!S.journalEntries.length) {
      root.innerHTML = `<div class="journal-empty">${q ? "没有找到匹配的想法。试试别的关键词。" : "你发过的每条想法都会保留在这里，包括没有变成任务的感慨。时间越久，它越像一本可搜索的流水账。"}</div>`;
      return;
    }
    root.innerHTML = S.journalEntries.map((e) => {
      const tasks = (e.tasks || []).map((t) =>
        `<button class="journal-task ${esc(t.status)}" data-task-id="${esc(t.id)}">${esc(t.title)}</button>`
      ).join("");
      const status = e.status && e.status !== "sent" ? `<span>· ${esc(e.status)}</span>` : "";
      return `<article class="journal-item" data-message-id="${esc(e.id)}">
        <div class="journal-head"><time>${esc(journalDate(e.at))}</time>${status}</div>
        <div class="journal-text">${esc(e.text)}</div>
        ${tasks ? `<div class="journal-tasks">${tasks}</div>` : ""}
      </article>`;
    }).join("");
  }

  function eventIcon(e) {
    switch (e.kind) {
      case "input": return ["user", "i-send"];
      case "onboarding": return ["user", "i-bolt"];
      case "todo.add": return [e.actor === "agent" ? "agent" : "user", "i-plus"];
      case "todo.complete": return ["done", "i-check"];
      case "todo.drop": return ["", "i-drop"];
      case "todo.move": return ["", "i-list"];
      case "todo.delete": return ["", "i-trash"];
      case "todo.update":
      case "todo.start": return [e.actor === "agent" ? "agent" : "", "i-edit"];
      case "dsh.reply":
      case "dsh.start":
      case "dsh.done": return ["agent", "i-spark"];
      default: return ["", "i-dot"];
    }
  }

  function renderHistory() {
    const root = $("historyList");
    if (!root) return;
    const events = ((S.data && S.data.events) || []).slice().sort((a, b) => (b.at || 0) - (a.at || 0)).slice(0, 300);
    $("historySummary").textContent = events.length ? `${events.length} 条记录` : "还没有记录";
    if (!events.length) {
      root.innerHTML = `<div class="history-empty">发送第一条想法后，这里会留下输入、任务变更和 DSH 的回复记录。</div>`;
      return;
    }
    let html = "";
    let lastDay = "";
    for (const e of events) {
      const dk = dayKey(e.at || Date.now());
      if (dk !== lastDay) {
        lastDay = dk;
        html += `<div class="history-day">${esc(dayLabel(e.at || Date.now()))}</div>`;
      }
      const [kind, icon] = eventIcon(e);
      const title = esc(e.title || "记录");
      const detailText = plainText(e.detail);
      const detail = detailText ? `<div class="h-detail">${esc(detailText)}</div>` : "";
      const original = e.originalText ? `<div class="h-source">原话：${esc(plainText(e.originalText))}</div>` : "";
      html += `<div class="history-item">
        <div class="h-icon ${kind}"><svg class="icon"><use href="#${icon}"/></svg></div>
        <div class="h-body">
          <div class="h-title"><span>${title}</span><time>${esc(fmtClock(e.at))}</time></div>
          ${detail}${original}
        </div>
      </div>`;
    }
    root.innerHTML = html;
  }

  // ---------------------------------------------------------------- setup

  function updateSetupBanner() {
    const el = $("setupBanner");
    if (!el) return;
    const d = (S.data && S.data.dsh) || {};
    const sid = d.sessionId || "";
    const onboarded = !!(S.data && S.data.prefs && S.data.prefs.onboardedSessions && S.data.prefs.onboardedSessions[sid]);
    const show = !!(d.connected && sid && !onboarded && !S.dismissed);
    el.classList.toggle("hidden", !show);
  }

  async function onboard() {
    try {
      await call("todo_sendOnboarding");
      S.dismissed = true;
      updateSetupBanner();
      showToast("success", "已发送接入说明，DSH 会开始维护清单");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  // ---------------------------------------------------------------- task modal

  function openTaskModal(id, parentId) {
    S.editingId = id || null;
    S.newParentId = id ? null : (parentId || null);
    const t = id ? findTodo(id) : null;
    const parent = t ? parentOf(t) : (S.newParentId ? findTodo(S.newParentId) : null);
    const childCount = t ? childrenOf(t.id).length : 0;
    $("taskModalTitle").textContent = t ? "编辑任务" : (parent ? "新建子任务" : "新建任务");
    $("taskTitle").value = t ? t.title : "";
    $("taskNotes").value = t ? (t.notes || "") : "";
    $("taskPriority").value = t ? (t.priority || "normal") : "normal";
    $("taskStatus").value = t ? (t.status || "todo") : "todo";
    $("taskDropReason").value = t ? (t.dropReason || "") : "";
    let hint = t
      ? `创建于 ${relTime(t.createdAt)} · 最近更新 ${relTime(t.updatedAt)}`
      : "任务会保存在本地；如果已接入 DSH，也可以让 DSH 来维护它。";
    if (parent) hint = `父任务：${parent.title} · ` + hint;
    if (childCount) hint = `含 ${childCount} 个子任务 · ` + hint;
    $("taskHint").textContent = hint;
    $("taskDelete").classList.toggle("hidden", !t);
    $("taskDrop").classList.toggle("hidden", !t || (t && t.status === "dropped"));
    syncDropFields();
    const sourceBox = $("taskSourceBox");
    if (t && (t.sourceText || t.sourceMessageID)) {
      sourceBox.classList.remove("hidden");
      $("taskSourceText").textContent = t.sourceText || "这条来源想法较早，可在「想法回顾」中搜索查看。";
      $("taskSourceTime").textContent = t.sourceAt ? journalDate(t.sourceAt) : (t.sourceMessageID ? "已关联" : "");
    } else {
      sourceBox.classList.add("hidden");
      $("taskSourceText").textContent = "";
      $("taskSourceTime").textContent = "";
    }
    $("taskLayer").classList.remove("hidden");
    S.taskOpen = true;
    setTimeout(() => $("taskTitle").focus(), 30);
  }

  function syncDropFields() {
    const dropped = $("taskStatus").value === "dropped";
    $("taskDropReasonField").classList.toggle("hidden", !dropped);
    if (S.editingId) $("taskDrop").classList.toggle("hidden", dropped || !S.editingId);
  }

  function closeTaskModal() {
    $("taskLayer").classList.add("hidden");
    S.taskOpen = false;
    S.editingId = null;
    S.newParentId = null;
    syncScrim();
  }

  async function saveTask() {
    const title = $("taskTitle").value.trim();
    if (!title) {
      showToast("error", "先给任务起个标题");
      $("taskTitle").focus();
      return;
    }
    const payload = {
      title,
      notes: $("taskNotes").value.trim(),
      priority: $("taskPriority").value,
      status: $("taskStatus").value,
    };
    if (!S.editingId && S.newParentId) payload.parentId = S.newParentId;
    if (payload.status === "dropped") {
      payload.dropReason = $("taskDropReason").value.trim();
    }
    try {
      if (S.editingId) {
        await call("todo_updateTodo", S.editingId, payload);
        showToast("success", "任务已更新");
      } else {
        await call("todo_addTodo", payload);
        showToast("success", "任务已添加");
      }
      closeTaskModal();
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function deleteTaskFromModal() {
    if (!S.editingId) return;
    const t = findTodo(S.editingId);
    if (!t) return;
    const n = childrenOf(t.id).length;
    const suffix = n ? `\n\n它还有 ${n} 个子任务，会一起删除。` : "";
    if (!window.confirm(`删除「${t.title}」？\n\n建议用「丢弃」保留记录。${suffix}`)) return;
    try {
      await call("todo_deleteTodo", S.editingId);
      showToast("info", "已删除任务");
      closeTaskModal();
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function dropTaskFromModal() {
    if (!S.editingId) return;
    const reason = $("taskDropReason").value.trim() || "手动丢弃";
    try {
      await call("todo_dropTodo", S.editingId, reason);
      showToast("info", "已丢弃，可在「丢弃」里找回");
      closeTaskModal();
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  function syncScrim() {
    const visible = S.settingsOpen || S.taskOpen || S.panelOpen;
    $("scrim").classList.toggle("hidden", !visible);
  }

  // ---------------------------------------------------------------- settings

  function openSettings() {
    S.settingsOpen = true;
    $("settingsLayer").classList.remove("hidden");
    $("scrim").classList.remove("hidden");
    updateSettings(true);
    setTimeout(() => $("settingsClose").focus(), 30);
  }

  function closeSettings() {
    $("settingsLayer").classList.add("hidden");
    S.settingsOpen = false;
    syncScrim();
  }

  function updateSettings(fillInputs) {
    if (!S.data) return;
    const d = S.data.dsh || {};
    const cfg = S.data.config || {};
    const bridge = S.data.bridge || {};

    const status = $("connStatus");
    const text = $("connStatusText");
    status.classList.remove("ok", "err", "busy");
    if (d.connected) {
      status.classList.add("ok");
      text.textContent = "已连接" + (d.sessionTitle ? " · " + d.sessionTitle : "");
    } else if (d.connecting) {
      status.classList.add("busy");
      text.textContent = "正在连接…";
    } else if (d.error) {
      status.classList.add("err");
      text.textContent = d.error;
    } else {
      status.classList.add("busy");
      text.textContent = "等待连接";
    }
    $("connHint").textContent = d.error
      ? d.error
      : "应用会读取 DSH 的本地签名密钥，直接调用当前会话的输入接口，不会另开进程。";

    if (fillInputs) {
      $("baseUrlInput").value = cfg.baseUrl || d.baseUrl || "";
      $("writeAgents").checked = S.data.prefs.writeAgents !== false;
      $("alwaysOnTop").checked = !!S.data.prefs.alwaysOnTop;
      $("compactPref").checked = S.viewMode === "compact";
    }
    renderSessionSelect();

    $("bridgeUrl").textContent = bridge.url || "不可用";
    $("bridgeUrl").title = bridge.url || "";
    $("bridgeConfig").textContent = bridge.configPath || "不可用";
    $("bridgeConfig").title = bridge.configPath || "";
    const token = bridge.token || "";
    $("bridgeToken").textContent = S.tokenVisible ? (token || "不可用") : (token ? "••••••••••••••••" : "不可用");
    $("bridgeToken").title = S.tokenVisible ? token : "点击右侧按钮显示";
    $("curlExample").textContent = bridge.url && token
      ? `curl.exe -s -X POST ${bridge.url}/api/todos ^\n  -H "Authorization: Bearer ${token}" ^\n  -H "Content-Type: application/json" ^\n  -d "{\\"action\\":\\"add\\",\\"title\\":\\"示例任务\\",\\"priority\\":\\"high\\"}"`
      : "接口尚未启动";

    $("metaTodos").textContent = (S.data.todos || []).length;
    $("metaEvents").textContent = (S.data.events || []).length;
    $("metaMessages").textContent = S.data.messageTotal || (S.data.messages || []).length;
    $("dataPath").textContent = S.data.dataPath || "—";
    $("dataPath").title = S.data.dataPath || "";
    $("aboutVersion").textContent = S.data.version || "0.2.0";
  }

  function renderSessionSelect() {
    const sel = $("sessionSelect");
    if (!sel) return;
    if (sel === document.activeElement) return;
    const sessions = (S.data && S.data.sessions) || [];
    const current = currentSession();
    const prev = sel.value;
    sel.innerHTML = "";
    if (!sessions.length) {
      const o = document.createElement("option");
      o.value = current || "";
      o.textContent = current
        ? ((S.data.dsh && S.data.dsh.sessionTitle) || "当前会话") + " · 当前"
        : "（未发现会话）";
      sel.appendChild(o);
      return;
    }
    for (const s of sessions) {
      const o = document.createElement("option");
      o.value = s.id;
      const title = s.title || (s.blank ? "新会话" : "未命名会话");
      const bits = [title];
      if (s.cwd) bits.push(cwdShort(s.cwd));
      if (s.updatedAt) bits.push(relTime(s.updatedAt));
      if (s.running) bits.unshift("●");
      o.textContent = bits.join(" · ");
      if (s.id === current) o.selected = true;
      sel.appendChild(o);
    }
    if (current && !sessions.some((s) => s.id === current)) {
      const o = document.createElement("option");
      o.value = current;
      o.textContent = ((S.data.dsh && S.data.dsh.sessionTitle) || "当前会话") + " · 当前";
      o.selected = true;
      sel.appendChild(o);
    }
    if (prev && sessions.some((s) => s.id === prev)) sel.value = prev;
    if (current && sessions.some((s) => s.id === current)) sel.value = current;
  }

  async function connectDSH() {
    const base = $("baseUrlInput").value.trim();
    const session = $("sessionSelect").value;
    try {
      await call("todo_connectDSH", base, session);
      showToast("info", "正在重新连接 DSH…");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function refreshSessions() {
    try {
      const items = await call("todo_refreshSessions");
      if (Array.isArray(items)) {
        S.data.sessions = items;
        renderSessionSelect();
      }
      showToast("info", "已请求刷新会话列表");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function selectSession(id) {
    if (!id || id === currentSession()) return;
    try {
      await call("todo_selectSession", id);
      showToast("success", "已切换目标会话");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function onboardFromSettings() {
    try {
      await call("todo_sendOnboarding");
      showToast("success", "接入说明已发送到 DSH");
      closeSettings();
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function writeAgents() {
    try {
      const r = await call("todo_writeAgents");
      if (r && r.ok) showToast("success", "已写入 " + r.path);
      else showToast("error", (r && r.error) || "写入失败");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function copyText(text, label) {
    if (!text) {
      showToast("error", "没有可复制的内容");
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      showToast("success", (label || "已复制"));
    } catch (_) {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy");
        showToast("success", (label || "已复制"));
      } catch (e) {
        showToast("error", "复制失败");
      }
      ta.remove();
    }
  }

  // ---------------------------------------------------------------- composer

  function autoGrow() {
    const ta = $("input");
    ta.style.height = "auto";
    ta.style.height = Math.min(132, Math.max(24, ta.scrollHeight)) + "px";
  }

  async function sendInput() {
    const ta = $("input");
    const text = ta.value.trim();
    if (!text) {
      const c = $("composer");
      c.animate([
        { transform: "translateX(0)" },
        { transform: "translateX(-4px)" },
        { transform: "translateX(4px)" },
        { transform: "translateX(0)" },
      ], { duration: 190, easing: "ease" });
      ta.focus();
      return;
    }
    if (!connected()) {
      showToast("error", "DSH 未连接，先到设置里选择会话");
      return;
    }
    const btn = $("sendBtn");
    btn.classList.add("disabled");
    S.localSentAt = Date.now();
    try {
      await call("todo_sendInput", text);
      ta.value = "";
      autoGrow();
      ta.focus();
      showToast("success", "已发送到 DSH");
      $("sendState").textContent = "已发送 · 等待 DSH 接收";
      $("sendState").className = "send-state ok";
    } catch (e) {
      showToast("error", "发送失败：" + errText(e));
      $("sendState").textContent = "发送失败";
      $("sendState").className = "send-state err";
    } finally {
      setTimeout(() => btn.classList.remove("disabled"), 450);
    }
  }

  function renderSendState() {
    const el = $("sendState");
    if (!el) return;
    const msgs = (S.data && S.data.messages) || [];
    const last = msgs[msgs.length - 1];
    if (!last) {
      el.textContent = "";
      el.className = "send-state";
      return;
    }
    if (last.status === "sending") {
      el.textContent = "正在发送…";
      el.className = "send-state";
    } else if (last.status === "failed") {
      el.textContent = "上次发送失败";
      el.className = "send-state err";
      el.title = last.error || "";
    } else if (Date.now() - (last.at || 0) < 120000) {
      el.textContent = "已发送 · DSH 已接收";
      el.className = "send-state ok";
    } else {
      el.textContent = "";
      el.className = "send-state";
    }
  }

  // ---------------------------------------------------------------- pin / prefs

  function updatePinButton() {
    const on = !!(S.data && S.data.prefs && S.data.prefs.alwaysOnTop);
    const btn = $("pinBtn");
    btn.classList.toggle("active", on);
    btn.setAttribute("aria-pressed", on ? "true" : "false");
    btn.title = on ? "取消窗口置顶" : "窗口置顶";
  }

  async function togglePin() {
    const on = !(S.data && S.data.prefs && S.data.prefs.alwaysOnTop);
    try {
      const r = await call("todo_setAlwaysOnTop", on);
      if (S.data) S.data.prefs.alwaysOnTop = !!(r && r.alwaysOnTop);
      updatePinButton();
      showToast("info", S.data.prefs.alwaysOnTop ? "窗口已置顶" : "已取消置顶");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  function savePrefs() {
    if (!S.data) return;
    const prefs = {
      alwaysOnTop: !!(S.data.prefs && S.data.prefs.alwaysOnTop),
      filter: S.filter,
      tab: "tasks",
      viewMode: S.viewMode,
      writeAgents: !!(S.data.prefs && S.data.prefs.writeAgents),
      onboardedSessions: (S.data.prefs && S.data.prefs.onboardedSessions) || {},
      window: { x: 0, y: 0, w: 0, h: 0 },
    };
    call("todo_savePrefs", prefs).catch(() => {});
  }

  function setFilter(f) {
    if (!FILTERS.includes(f)) return;
    S.filter = f;
    updateFilterButtons();
    renderTodos(true);
    savePrefs();
  }

  // ---------------------------------------------------------------- wiring

  function bindUI() {
    $("pinBtn").addEventListener("click", togglePin);
    $("journalBtn").addEventListener("click", () => openPanel("journal"));
    $("historyBtn").addEventListener("click", () => openPanel("history"));
    $("historyClose").addEventListener("click", closePanel);
    $("historyScrollTop").addEventListener("click", () => { $("historyList").scrollTop = 0; });
    $("settingsBtn").addEventListener("click", openSettings);
    $("settingsClose").addEventListener("click", closeSettings);
    $("sessionChip").addEventListener("click", openSettings);
    $("scrim").addEventListener("click", () => {
      if (S.taskOpen) closeTaskModal();
      if (S.settingsOpen) closeSettings();
      if (S.panelOpen) closePanel();
    });

    $("panelTabs").addEventListener("click", (e) => {
      const b = e.target.closest(".panel-tab");
      if (b) switchPanelTab(b.dataset.panel);
    });
    $("journalMore").addEventListener("click", () => loadJournal(false));
    let journalTimer = null;
    $("journalSearch").addEventListener("input", (e) => {
      clearTimeout(journalTimer);
      journalTimer = setTimeout(() => {
        S.journalQuery = e.target.value.trim();
        loadJournal(true);
      }, 320);
    });
    $("journalList").addEventListener("click", (e) => {
      const task = e.target.closest("[data-task-id]");
      if (task) openTaskModal(task.dataset.taskId);
    });

    $("filters").addEventListener("click", (e) => {
      const b = e.target.closest(".filter");
      if (b) setFilter(b.dataset.filter);
    });
    $("viewBtn").addEventListener("click", () => setViewMode(S.viewMode === "compact" ? "cards" : "compact"));
    $("addBtn").addEventListener("click", () => openTaskModal());
    $("emptyAdd").addEventListener("click", () => {
      const all = (S.data && S.data.todos) || [];
      if (all.length > 0 && S.filter !== "all") setFilter("all");
      else openTaskModal();
    });

    $("list").addEventListener("click", (e) => {
      if (S.suppressClick) return;
      const act = e.target.closest("[data-act]");
      if (!act) return;
      if (act.dataset.act === "group") {
        const group = act.dataset.group;
        const c = act.closest(".cgroup");
        const collapsed = c ? c.classList.contains("collapsed") : false;
        S.collapsedGroups[group] = !collapsed;
        renderTodos(true);
        return;
      }
      if (act.dataset.act === "sub-add") {
        openTaskModal(null, act.dataset.parentId);
        return;
      }
      const card = e.target.closest(".subtask, .todo, .crow");
      if (!card) return;
      const id = card.dataset.id;
      if (act.dataset.act === "toggle") toggleTodo(id);
      else if (act.dataset.act === "edit") openTaskModal(id);
      else if (act.dataset.act === "drop") dropTodoAction(id);
      else if (act.dataset.act === "del") deleteTodo(id);
    });

    const listEl = $("list");
    listEl.addEventListener("dragstart", onDragStart);
    listEl.addEventListener("dragover", onDragOver);
    listEl.addEventListener("dragleave", onDragLeave);
    listEl.addEventListener("drop", onDrop);
    listEl.addEventListener("dragend", onDragEnd);

    $("taskClose").addEventListener("click", closeTaskModal);
    $("taskCancel").addEventListener("click", closeTaskModal);
    $("taskSave").addEventListener("click", saveTask);
    $("taskDelete").addEventListener("click", deleteTaskFromModal);
    $("taskDrop").addEventListener("click", dropTaskFromModal);
    $("taskStatus").addEventListener("change", syncDropFields);
    $("taskLayer").addEventListener("mousedown", (e) => { if (e.target === $("taskLayer")) closeTaskModal(); });
    $("taskTitle").addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.isComposing) {
        e.preventDefault();
        saveTask();
      }
    });

    $("setupBtn").addEventListener("click", onboard);
    $("setupDismiss").addEventListener("click", () => {
      S.dismissed = true;
      updateSetupBanner();
    });

    $("connectBtn").addEventListener("click", connectDSH);
    $("newSessionBtn").addEventListener("click", async () => {
      try {
        await call("todo_newSession");
        showToast("info", "正在创建专用会话…");
      } catch (e) {
        showToast("error", errText(e));
      }
    });
    $("refreshSessions").addEventListener("click", refreshSessions);
    $("sessionSelect").addEventListener("change", (e) => selectSession(e.target.value));
    $("onboardBtn").addEventListener("click", onboardFromSettings);
    $("writeAgents").addEventListener("change", (e) => {
      if (S.data) S.data.prefs.writeAgents = e.target.checked;
      savePrefs();
      if (e.target.checked) writeAgents();
    });
    $("alwaysOnTop").addEventListener("change", async (e) => {
      try {
        await call("todo_setAlwaysOnTop", e.target.checked);
        if (S.data) S.data.prefs.alwaysOnTop = e.target.checked;
        updatePinButton();
      } catch (err) {
        showToast("error", errText(err));
      }
    });
    $("compactPref").addEventListener("change", (e) => {
      setViewMode(e.target.checked ? "compact" : "cards");
    });

    $("copyBridgeUrl").addEventListener("click", () => copyText(($("bridgeUrl").textContent || "").trim(), "地址已复制"));
    $("copyBridgeToken").addEventListener("click", () => copyText((S.data && S.data.bridge && S.data.bridge.token) || "", "Token 已复制"));
    $("copyBridgeConfig").addEventListener("click", () => copyText((S.data && S.data.bridge && S.data.bridge.configPath) || "", "路径已复制"));
    $("copyDataPath").addEventListener("click", () => copyText((S.data && S.data.dataPath) || "", "路径已复制"));
    $("toggleToken").addEventListener("click", () => {
      S.tokenVisible = !S.tokenVisible;
      updateSettings(false);
    });
    $("copyCurl").addEventListener("click", () => copyText(($("curlExample").textContent || "").trim(), "curl 示例已复制"));
    $("diagnosticsBtn").addEventListener("click", async () => {
      try {
        const text = await call("todo_diagnostics");
        await copyText(text, "诊断信息已复制");
      } catch (e) {
        showToast("error", errText(e));
      }
    });
    $("quitBtn").addEventListener("click", async () => {
      if (!window.confirm("退出 TodoList Agent？\n清单和记录都已保存。")) return;
      try { await call("todo_quit"); } catch (_) {}
    });

    const ta = $("input");
    ta.addEventListener("input", autoGrow);
    ta.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
        e.preventDefault();
        sendInput();
      }
    });
    $("sendBtn").addEventListener("click", sendInput);
    $("settingsLayer").addEventListener("mousedown", (e) => { if (e.target === $("settingsLayer")) closeSettings(); });

    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        if (S.taskOpen) closeTaskModal();
        else if (S.settingsOpen) closeSettings();
        else if (S.panelOpen) closePanel();
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        openTaskModal();
      }
      if ((e.ctrlKey || e.metaKey) && e.key === ",") {
        e.preventDefault();
        openSettings();
      }
    });
  }

  // ---------------------------------------------------------------- webview events

  window.__todoAgent = {
    push(evt) {
      if (!evt || !evt.type) return;
      const d = evt.data;
      switch (evt.type) {
        case "state":
          applyState(d);
          break;
        case "status":
          applyStatus(d);
          break;
        case "sessions":
          if (S.data) S.data.sessions = d || [];
          if (S.settingsOpen) renderSessionSelect();
          break;
        case "toast":
          showToast((d && d.kind) || "info", (d && d.text) || "");
          break;
        case "new-task":
          openTaskModal();
          break;
        default:
          break;
      }
    },
  };

  // ---------------------------------------------------------------- boot

  document.addEventListener("DOMContentLoaded", async () => {
    bindUI();
    try {
      const s = await call("todo_ready");
      applyState(s);
      autoGrow();
    } catch (e) {
      showToast("error", "初始化失败：" + errText(e));
      $("statusCard").dataset.state = "error";
      $("statusTitle").textContent = "界面初始化失败";
      $("statusSub").textContent = errText(e);
    }
  });
})();

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
    deleteId: null,
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
    drag: null,
    suppressClick: false,
  };

  const PRIORITY = {
    pinned: { rank: 0 },
    high: { rank: 1 },
    normal: { rank: 2 },
    low: { rank: 3 },
  };
  const PRIORITY_ORDER = ["pinned", "high", "normal", "low"];
  const PRIORITY_BAND_LABEL = { pinned: "置顶 · 长期", high: "高优先级", normal: "中优先级", low: "低优先级" };
  const STATUS = {
    todo: { label: "待办", rank: 1 },
    doing: { label: "进行中", rank: 0 },
    done: { label: "已完成", rank: 2 },
  };
  const FILTERS = ["all", "open", "doing", "done"];

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
    if (S.settingsOpen) updateSettings(false);
    const totalChanged = s.messageTotal !== S.lastMessageTotal;
    S.lastMessageTotal = s.messageTotal;
    if (S.panelOpen && S.panelTab === "history") renderHistory();
    if (S.panelOpen && S.panelTab === "journal" && !S.journalLoading && totalChanged) {
      loadJournal(true);
    } else if (S.panelOpen && S.panelTab === "journal") {
      const messages = new Map(s.messages.map((m) => [m.id, m]));
      for (const entry of S.journalEntries) {
        const message = messages.get(entry.id);
        if (message) {
          entry.status = message.status;
          entry.error = message.error;
        }
      }
      renderJournal();
    }
    updateRunningTimer();
  }

  function applyStatus(st) {
    if (!S.data || !st) return;
    const wasRunning = !!(S.data.dsh && S.data.dsh.running);
    S.data.dsh = st;
    renderStatus();
    renderSessionChip();
    renderRunning();
    renderSendState();
    if (S.settingsOpen) updateSettings(false);
    if (!wasRunning && st.running && !(S.drag && S.drag.ghost)) {
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
      s = isMobile() ? "等待配对的电脑 · 可离线保存消息" : "请确认 DeepSeek Harness 正在运行";
      m = isMobile() ? "离线" : "重试";
    }

    card.dataset.state = state;
    title.textContent = t;
    title.title = t;
    sub.textContent = s;
    sub.title = s;
    stat.textContent = m;
    brandSub.textContent = d.connected
      ? (d.running ? "DSH 正在处理当前任务" : "与当前 DSH 会话同步")
      : (isMobile() ? "等待局域网内的 DSH 电脑" : (d.connecting ? "正在查找本地会话…" : "等待 DeepSeek Harness"));
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
    const rank = { doing: 0, todo: 1, done: 2 };
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
    const rank = { doing: 0, todo: 1, done: 2 };
    return list.slice().sort((a, b) => {
      const ra = rank[a.status] == null ? 1 : rank[a.status];
      const rb = rank[b.status] == null ? 1 : rank[b.status];
      if (ra !== rb) return ra - rb;
      if (a.status === "done" && b.status === "done") {
        return (b.completedAt || b.updatedAt || 0) - (a.completedAt || a.updatedAt || 0);
      }
      const pa = (PRIORITY[a.priority] || PRIORITY.normal).rank;
      const pb = (PRIORITY[b.priority] || PRIORITY.normal).rank;
      if (pa !== pb) return pa - pb;
      const oa = typeof a.position === "number" ? a.position : 0;
      const ob = typeof b.position === "number" ? b.position : 0;
      if (oa !== ob) return oa - ob;
      return (a.createdAt || 0) - (b.createdAt || 0);
    });
  }

  function focusButtonHTML(focused) {
    const label = focused ? "取消专注" : "专注此项";
    return `<button class="icon-btn focus ${focused ? "active" : ""}" data-act="focus" title="${label}" aria-label="${label}" aria-pressed="${focused}"><svg class="icon"><use href="#i-target"/></svg></button>`;
  }

  function taskNumberHTML(t, className) {
    const number = esc(t.number || "");
    if (t.status !== "todo") return `<span class="${className}">${number}</span>`;
    return `<span class="${className} drag-grip" data-drag-grip title="拖动排序" aria-label="任务 ${number}，拖动排序"><span aria-hidden="true">⠿</span>${number}</span>`;
  }

  function subtaskRowHTML(t) {
    const done = t.status === "done";
    const doing = t.status === "doing";
    const focused = !!(S.data && S.data.focusId === t.id);
    return `<div class="subtask prio-${priorityKey(t)} ${focused ? "focused" : ""} ${done ? "done" : ""} ${doing ? "doing" : ""}" data-id="${esc(t.id)}">
      <button class="scheck" data-act="toggle" aria-label="${done ? "重新打开" : "完成子任务"}" title="${done ? "重新打开" : "完成子任务"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      ${taskNumberHTML(t, "subtask-num")}
      <div class="subtask-main">
        <div class="stitle" data-act="edit">${esc(t.title)}</div>
        ${t.notes ? `<div class="snotes">${esc(t.notes)}</div>` : ""}
      </div>
      <div class="sactions">
        ${done ? "" : focusButtonHTML(focused)}
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        <button class="icon-btn del" data-act="del" title="删除" aria-label="删除"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </div>`;
  }

  function todoHTML(t) {
    const done = t.status === "done";
    const doing = t.status === "doing";
    const focused = !!(S.data && S.data.focusId === t.id);
    const children = done ? [] : childrenOf(t.id);
    const openChildren = children.filter((c) => c.status !== "done").length;
    const statusPill = doing ? '<span class="pill doing">进行中</span>' : "";
    const focusPill = focused ? '<span class="pill focus">专注</span>' : "";
    const subPill = children.length ? `<span class="pill subcount">${children.length} 子任务</span>` : "";
    const notes = t.notes ? `<div class="todo-notes">${esc(t.notes)}</div>` : "";
    const meta = statusPill + focusPill + subPill;
    const subtasks = children.length ? `<div class="subtasks">
      <div class="subtask-top">
        <span>子任务</span>
        <span class="subtask-progress">${children.length - openChildren}/${children.length}</span>
      </div>
      ${children.map(subtaskRowHTML).join("")}
      <button class="sub-add" data-act="sub-add" data-parent-id="${esc(t.id)}">+ 添加子任务</button>
    </div>` : "";
    return `<article class="todo prio-${priorityKey(t)} ${focused ? "focused" : ""} ${done ? "done" : ""} ${doing ? "doing" : ""}" data-id="${esc(t.id)}">
      <button class="check" data-act="toggle" aria-label="${done ? "标记为待办" : "标记为完成"}" title="${done ? "标记为待办" : "标记为完成"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      ${taskNumberHTML(t, "todo-num")}
      <div class="todo-main">
        <div class="todo-title" data-act="edit" title="点击编辑">${esc(t.title)}</div>
        ${notes}
        ${meta ? `<div class="todo-meta">${meta}</div>` : ""}
        ${subtasks}
      </div>
      <div class="todo-actions">
        ${done ? "" : focusButtonHTML(focused)}
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        ${done ? "" : `<button class="icon-btn" data-act="sub-add" data-parent-id="${esc(t.id)}" title="添加子任务" aria-label="添加子任务"><svg class="icon"><use href="#i-plus"/></svg></button>`}
        <button class="icon-btn del" data-act="del" title="删除" aria-label="删除"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </article>`;
  }

  function compactRowHTML(t, isChild) {
    const done = t.status === "done";
    const doing = t.status === "doing";
    const focused = !!(S.data && S.data.focusId === t.id);
    const children = isChild ? [] : childrenOf(t.id);
    const sub = children.length ? `<span>${children.filter((c) => c.status === "done").length}/${children.length}</span>` : "";
    const meta = (focused ? '<span class="focus-tag">专注</span>' : "") + sub;
    return `<div class="crow prio-${priorityKey(t)} ${focused ? "focused" : ""} ${isChild ? "child" : ""} ${done ? "done" : ""} ${doing ? "doing" : ""}" data-id="${esc(t.id)}">
      <button class="ccheck" data-act="toggle" aria-label="${done ? "重新打开" : "标记完成"}" title="${done ? "重新打开" : "标记完成"}">
        <svg class="icon"><use href="#i-check"/></svg>
      </button>
      ${taskNumberHTML(t, "crow-num")}
      <div class="crow-main">
        <div class="crow-title" data-act="edit" title="${esc(t.notes || t.title)}">${esc(t.title)}</div>
        ${meta ? `<div class="crow-meta">${meta}</div>` : ""}
      </div>
      <div class="crow-actions">
        ${done ? "" : focusButtonHTML(focused)}
        <button class="icon-btn" data-act="edit" title="编辑" aria-label="编辑"><svg class="icon"><use href="#i-edit"/></svg></button>
        ${isChild || done ? "" : `<button class="icon-btn" data-act="sub-add" data-parent-id="${esc(t.id)}" title="添加子任务" aria-label="添加子任务"><svg class="icon"><use href="#i-plus"/></svg></button>`}
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
    const order = ["doing", "todo", "done"];
    const allowed = S.filter === "all" || S.filter === "open"
      ? order
      : S.filter === "doing" ? ["doing"]
        : S.filter === "done" ? ["done"]
            : ["todo", "doing"];
    return allowed
      .map((status) => ({ status, items: items.filter((t) => t.status === status) }))
      .filter((g) => g.items.length > 0);
  }

  function groupCollapsed(status) {
    if (S.filter !== "all" && S.filter !== "open") return false;
    if (status === "done") {
      return S.collapsedGroups[status] !== false;
    }
    return S.collapsedGroups[status] === true;
  }

  function compactHTML(items) {
    const groups = compactGroups(items);
    return groups.map((g) => {
      const collapsed = groupCollapsed(g.status);
      const rowFor = (t) => {
        const children = t.status === "done" ? [] : childrenOf(t.id);
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

  function dropTargetAt(target, source, y) {
    const divider = !source.parentId && target && target.closest(".prio-divider");
    if (divider) {
      return { el: divider, priority: divider.dataset.priority, hint: "drop-over" };
    }
    let row = target && target.closest(".todo, .crow, .subtask");
    if (!source.parentId && row && row.classList.contains("subtask")) row = row.closest(".todo");
    if (!source.parentId) {
      while (row && row.classList.contains("child")) row = row.previousElementSibling;
    }
    const task = row && findTodo(row.dataset.id);
    if (!task || task.id === source.id || task.status !== "todo" ||
        (task.parentId || "") !== (source.parentId || "")) return null;
    const rect = row.getBoundingClientRect();
    const after = y > rect.top + rect.height / 2;
    return { el: row, id: task.id, priority: priorityKey(task), after, hint: after ? "drop-after" : "drop-before" };
  }

  function insideList(rect, x, y) {
    return x >= rect.left && x < rect.right && y >= rect.top && y < rect.bottom;
  }

  function updateDropHint(drag, rect) {
    const task = findTodo(drag.id);
    let target = null;
    if (task && task.status === "todo" && insideList(rect, drag.x, drag.y)) {
      const hit = document.elementFromPoint(drag.x, drag.y);
      if (hit && drag.list.contains(hit)) target = dropTargetAt(hit, task, drag.y);
    }
    const previous = drag.target || {};
    const current = target || {};
    if (current.el !== previous.el || current.hint !== previous.hint) {
      if (drag.target) drag.target.el.classList.remove(drag.target.hint);
      if (target) target.el.classList.add(target.hint);
    }
    drag.target = target;
  }

  function edgeScrollSpeed(rect, y) {
    const edge = Math.min(72, rect.height / 3);
    if (edge <= 0 || y < rect.top || y >= rect.bottom) return 0;
    const top = Math.max(0, 1 - (y - rect.top) / edge);
    const bottom = Math.max(0, 1 - (rect.bottom - y) / edge);
    return 1100 * (bottom ** 2 - top ** 2);
  }

  function tickDrag(now) {
    const drag = S.drag;
    if (!drag || !drag.ghost) return;
    const rect = drag.list.getBoundingClientRect();
    if (insideList(rect, drag.x, drag.y)) {
      const speed = edgeScrollSpeed(rect, drag.y);
      const elapsed = drag.lastFrame ? Math.min(now - drag.lastFrame, 32) : 16;
      const step = speed * elapsed / 1000;
      if (step) drag.list.scrollTop += Math.sign(step) * Math.max(1, Math.abs(step));
    }
    drag.lastFrame = now;
    updateDropHint(drag, rect);
    drag.frame = requestAnimationFrame(tickDrag);
  }

  function beginDrag(e) {
    if (S.drag || !e.isPrimary || e.button !== 0) return;
    const grip = e.target.closest("[data-drag-grip]");
    if (!grip && (e.pointerType !== "mouse" || e.target.closest("button, [data-act], a"))) return;
    const row = e.target.closest(".todo, .crow, .subtask");
    const task = row && findTodo(row.dataset.id);
    if (!task || task.status !== "todo") return;
    S.drag = { list: e.currentTarget, row, id: task.id, pointer: e.pointerId,
      startX: e.clientX, startY: e.clientY, x: e.clientX, y: e.clientY };
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function moveDrag(e) {
    const drag = S.drag;
    if (!drag || drag.pointer !== e.pointerId) return;
    drag.x = e.clientX;
    drag.y = e.clientY;
    if (!drag.ghost) {
      if (Math.hypot(drag.x - drag.startX, drag.y - drag.startY) < 5) return;
      const task = findTodo(drag.id);
      if (!task || !drag.row.isConnected) {
        finishDrag(false);
        return;
      }
      drag.row.classList.add("dragging");
      drag.ghost = document.createElement("div");
      drag.ghost.className = "drag-ghost";
      drag.ghost.setAttribute("aria-hidden", "true");
      drag.ghost.textContent = task.title;
      document.body.append(drag.ghost);
      drag.frame = requestAnimationFrame(tickDrag);
    }
    e.preventDefault();
    drag.ghost.style.left = Math.max(8, Math.min(drag.x + 12, window.innerWidth - drag.ghost.offsetWidth - 8)) + "px";
    drag.ghost.style.top = Math.max(8, drag.y - 34) + "px";
    updateDropHint(drag, drag.list.getBoundingClientRect());
  }

  function finishDrag(commit) {
    const drag = S.drag;
    if (!drag) return;
    S.drag = null;
    if (drag.list.hasPointerCapture(drag.pointer)) drag.list.releasePointerCapture(drag.pointer);
    if (!drag.ghost) return;
    cancelAnimationFrame(drag.frame);
    drag.ghost.remove();
    drag.row.classList.remove("dragging");
    if (drag.target) drag.target.el.classList.remove(drag.target.hint);
    S.suppressClick = true;
    setTimeout(() => { S.suppressClick = false; }, 200);
    renderTodos(false);
    if (commit && drag.target) {
      const target = drag.target;
      call("todo_moveTodo", drag.id, target.priority, target.id || "", !!target.after)
        .catch((err) => showToast("error", errText(err)));
    }
  }

  function endDrag(e) {
    const drag = S.drag;
    if (!drag || drag.pointer !== e.pointerId) return;
    const commit = e.type === "pointerup";
    if (commit && drag.ghost) {
      drag.x = e.clientX;
      drag.y = e.clientY;
      updateDropHint(drag, drag.list.getBoundingClientRect());
    }
    finishDrag(commit);
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
    }
    return { title, sub };
  }

  function renderTodos(force) {
    if (S.drag && S.drag.ghost) return;
    const list = $("list");
    const empty = $("empty");
    const items = visibleTodos();
    const compact = S.viewMode === "compact";
    const sig = [
      compact ? "compact" : "cards",
      S.filter,
      S.data && S.data.focusId,
      JSON.stringify(S.collapsedGroups),
      allTodos().map((t) => [t.id, t.parentId, t.status, t.title, t.notes, t.priority, t.updatedAt, t.completedAt, t.position].join("\u0001")).join("\u0002"),
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
    $("countAll").textContent = all.length;
    $("countOpen").textContent = open;
    $("countDoing").textContent = doing;
    $("countDone").textContent = done;
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
    const next = t.status === "done" ? "todo" : "done";
    try {
      await call("todo_setTodoStatus", id, next);
      showToast("success", next === "done" ? "已完成：" + clamp(t.title, 30) : "已重新打开");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function toggleFocus(id) {
    const current = S.data && S.data.focusId;
    const next = current === id ? "" : id;
    try {
      await call("todo_setFocus", next);
      showToast("success", next ? "已专注此项" : "已取消专注");
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  function confirmDeleteTodo(id) {
    const t = findTodo(id);
    if (!t) return;
    const dialog = $("deleteDialog");
    if (dialog.open) return;
    S.deleteId = id;
    $("deleteTaskTitle").textContent = t.title;
    const children = childrenOf(id).length;
    $("deleteTaskDetail").textContent = children ? `此任务和它的 ${children} 个子任务会一起删除。` : "此任务会从清单移除。";
    dialog.returnValue = "cancel";
    dialog.showModal();
  }

  async function finishDeleteTodo() {
    const id = S.deleteId;
    S.deleteId = null;
    if (!id || $("deleteDialog").returnValue !== "delete") return;
    try {
      await call("todo_deleteTodo", id);
      if (S.editingId === id) closeTaskModal();
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
      const label = {queued: "等待发送", sending: "等待 DSH 接收确认", accepted: "DSH 已接收",
        uncertain: "执行状态待核对", failed: "未正常完成"}[e.status];
      const status = label ? `<span>· ${label}</span>` : "";
      return `<article class="journal-item" data-message-id="${esc(e.id)}">
        <div class="journal-head"><time>${esc(journalDate(e.at))}</time>${status}</div>
        <div class="journal-text">${esc(e.text)}</div>
        ${e.error ? `<div class="journal-head">${esc(e.error)}</div>` : ""}
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
      case "todo.move": return ["", "i-list"];
      case "todo.drop":
      case "todo.delete": return ["", "i-trash"];
      case "todo.update":
      case "todo.start": return [e.actor === "agent" ? "agent" : "", "i-edit"];
      case "dsh.reply":
      case "dsh.receipt":
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
    const hints = [];
    if (parent) hints.push(`父任务：${parent.title}`);
    if (childCount) hints.push(`含 ${childCount} 个子任务`);
    if (!t) hints.push("任务会保存在本地，也可以由已接入的 DSH 维护。");
    $("taskHint").textContent = hints.join(" · ");
    $("taskHint").classList.toggle("hidden", hints.length === 0);
    $("taskDelete").classList.toggle("hidden", !t);
    $("taskOrder").classList.toggle("hidden", !t || !["todo", "doing"].includes(t.status));
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
    call("todo_lanState").then((view) => applyLAN(view, true)).catch((e) => showToast("error", errText(e)));
    setTimeout(() => $("settingsClose").focus(), 30);
  }

  function closeSettings() {
    $("settingsLayer").classList.add("hidden");
    S.settingsOpen = false;
    syncScrim();
  }

  function isMobile() {
    return document.documentElement.classList.contains("android");
  }

  function applyUpdate(view) {
    const available = view.available;
    const busy = ["checking", "downloading", "installing"].includes(view.phase);
    const status = {
      checking: "正在检查更新…",
      downloading: "正在下载并校验更新…",
      installing: "正在安装，请按系统提示操作…",
      checked: available ? `发现新版本 ${available}` : "已是最新版本",
    };
    const retry = view.retryAt > Date.now() ? `。可在 ${new Date(view.retryAt).toLocaleTimeString()} 后重试` : "";
    $("updateStatus").textContent = view.error ? view.error + retry : status[view.phase] || "";
    $("updateBanner").classList.toggle("hidden", !available);
    $("updateBannerText").textContent = view.error || (busy ? status[view.phase] : `发现新版本 ${available}`);
    $("updateInstall").classList.toggle("hidden", !available);
    $("updateAuto").checked = !!view.auto;
    for (const id of ["updateCheck", "updateInstall", "updateBannerInstall"]) $(id).disabled = busy;
  }

  function installUpdate() {
    call("todo_updateInstall").catch((e) => showToast("error", errText(e)));
  }

  function applyLAN(view, fillInputs) {
    S.lan = view || {};
    const peers = S.lan.peers || [];
    const pending = S.lan.pending || [];
    const paused = peers.some((p) => p.paused);
    const active = peers.find((p) => p.id === S.lan.activeDSH);
    if (fillInputs) {
      $("lanCode").value = S.lan.code || "";
      $("lanAddress").value = S.lan.address || "";
    }
    $("lanStatus").textContent = S.lan.error || (S.lan.code
      ? `已启用 · 发现 ${peers.length} 台设备${active ? " · 当前代发 " + active.name : ""}${paused ? " · DSH 处理完后自动同步" : ""}`
      : "在一台设备生成配对码，再粘贴到同一局域网内的其他设备。");
    $("lanPeers").innerHTML = peers.map((p) =>
      `<li>${esc(p.name)} · ${esc(p.address)}${p.needsUpgrade ? " · 同步需升级" : p.hasDSH ? (p.dispatch ? " · DSH" : " · DSH 代发需升级") : ""}</li>`).join("");
    const choices = peers.filter((p) => !p.needsUpgrade && p.platform === "windows" && p.dispatch && p.hasDSH);
    const select = $("lanDSH");
    const preferred = S.lan.dshDevice || "";
    const offline = preferred && !choices.some((p) => p.id === preferred)
      ? `<option value="${esc(preferred)}">首选电脑暂不可用</option>` : "";
    const options = '<option value="">自动选择</option>' + offline + choices.map((p) =>
      `<option value="${esc(p.id)}">${esc(p.name)} · ${esc(p.address)}</option>`).join("");
    if (select.dataset.options !== options) {
      select.innerHTML = options;
      select.dataset.options = options;
    }
    select.value = preferred;
    $("queueDetails").classList.toggle("hidden", pending.length === 0);
    $("queueTitle").textContent = `待发消息（${pending.length}）`;
    $("queueItems").innerHTML = pending.map((m) =>
      `<li><time>${esc(journalDate(m.at))}</time> ${esc(m.text)}${m.host ? " · 等待原电脑确认" : ""}</li>`).join("");
    renderSendState();
  }

  async function configureLAN(code, address) {
    try {
      const view = await call("todo_lanConfigure", code, address);
      applyLAN(view, true);
      showToast(view.error ? "error" : "success", view.error || (view.code ? "已保存局域网配对" : "局域网同步已停用"));
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  async function refreshLAN() {
    const button = $("lanRefresh");
    if (button.disabled) return;
    button.disabled = true;
    try {
      await call("todo_lanRefresh");
    } catch (e) {
      showToast("info", errText(e));
      button.disabled = false;
    }
  }

  async function moveOnPhone(offset) {
    const task = findTodo(S.editingId);
    if (!task) return;
    const siblings = allTodos().filter((t) => (t.parentId || "") === (task.parentId || "") &&
      t.priority === task.priority && t.status === task.status);
    siblings.sort((a, b) => a.position - b.position || b.createdAt - a.createdAt);
    const target = siblings[siblings.findIndex((t) => t.id === task.id) + offset];
    if (!target) return;
    try {
      await call("todo_moveTodo", task.id, task.priority, target.id, offset > 0);
    } catch (e) {
      showToast("error", errText(e));
    }
  }

  function updateSettings(fillInputs) {
    if (!S.data) return;
    const d = S.data.dsh || {};
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
      : "通过本机 DSH 插件处理消息，会话、初始化提示词和模型均在 DSH 插件页管理。";

    if (fillInputs) {
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
    const el = $("backendSession");
    if (!el) return;
    const dsh = (S.data && S.data.dsh) || {};
    el.textContent = dsh.sessionId ? "插件处理会话 · " + dsh.sessionId : "等待插件分配并初始化处理会话";
  }

  async function connectDSH() {
    try {
      await call("todo_connectDSH");
      showToast("info", "正在重新连接 DSH…");
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
    const btn = $("sendBtn");
    btn.classList.add("disabled");
    S.localSentAt = Date.now();
    try {
      await call("todo_sendInput", text);
      ta.value = "";
      autoGrow();
      ta.focus();
      showToast("success", connected() ? "已加入待发队列" : "已保存在本机，连接后自动发送");
      $("sendState").textContent = "已加入待发队列";
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
    const data = S.data || {};
    const dsh = data.dsh || {};
    const recheck = $("recheckPrompts");
    recheck.classList.toggle("hidden", !data.uncertainPrompts);
    recheck.textContent = isMobile() ? "刷新同步" : "重新核对";
    el.title = "";
    el.className = "send-state";
    if (data.promptStage) {
      if (!dsh.connected) {
        el.textContent = "等待 DSH 重连 · 核对原消息";
      } else if (data.promptStage === "accepted") {
        el.textContent = "DSH 已接收 · 等待执行结果";
      } else {
        el.textContent = "等待 DSH 接收确认";
      }
      return;
    }
    const pending = S.lan && S.lan.pending;
    if (pending && pending.length) {
      const assigned = isMobile() ? pending.filter((p) => p.host).length : 0;
      el.textContent = assigned === pending.length
        ? `${assigned} 条等待原电脑确认`
        : `${pending.length} 条等待发送`;
      el.className = "send-state";
      return;
    }
    if (S.lan && (S.lan.peers || []).some((p) => p.paused)) {
      el.textContent = "DSH 正在处理 · 完成后同步";
      el.className = "send-state";
      return;
    }
    const msgs = data.messages || [];
    const last = msgs[msgs.length - 1];
    if (data.uncertainPrompts) {
      el.textContent = `${data.uncertainPrompts} 条执行状态待核对`;
      el.className = "send-state err";
      el.title = last && last.status === "uncertain" ? (last.error || "") : "请在想法回顾查看各条消息";
      return;
    }
    if (!last) {
      el.textContent = "";
      el.className = "send-state";
      return;
    }
    if (last.status === "sending") {
      el.textContent = "等待 DSH 接收确认";
      el.className = "send-state";
    } else if (last.status === "accepted") {
      el.textContent = "DSH 已接收 · 等待执行结果";
    } else if (last.status === "queued") {
      el.textContent = "已加入 DSH 队列";
      el.className = "send-state";
    } else if (last.status === "uncertain" || last.status === "failed") {
      el.textContent = last.status === "uncertain" ? "执行状态待核对" : "上条消息未正常完成";
      el.className = "send-state err";
      el.title = last.error || "";
    } else if (Date.now() - (last.at || 0) < 120000) {
      el.textContent = "DSH 已完成";
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
    $("updateCheck").addEventListener("click", () => call("todo_updateCheck").catch((e) => showToast("error", errText(e))));
    $("updateReleasePage").addEventListener("click", () => call("todo_openUpdatePage").catch((e) => showToast("error", errText(e))));
    $("updateInstall").addEventListener("click", installUpdate);
    $("updateBannerInstall").addEventListener("click", installUpdate);
    $("updateAuto").addEventListener("change", async (e) => {
      try { await call("todo_updateAuto", e.target.checked); }
      catch (error) {
        e.target.checked = !e.target.checked;
        showToast("error", errText(error));
      }
    });
    $("lanRefresh").addEventListener("click", refreshLAN);
    $("recheckPrompts").addEventListener("click", () => {
      if (isMobile()) {
        refreshLAN();
      } else {
        call("todo_recheckPrompts").then(() => showToast("info", "正在核对原 DSH 会话"))
          .catch((error) => showToast("error", errText(error)));
      }
    });
    $("lanSave").addEventListener("click", () => configureLAN($("lanCode").value, $("lanAddress").value));
    $("lanGenerate").addEventListener("click", () => configureLAN("generate", $("lanAddress").value));
    $("lanDisable").addEventListener("click", () => configureLAN("", ""));
    $("lanCopy").addEventListener("click", () => copyText($("lanCode").value, "配对码已复制"));
    $("lanDSH").addEventListener("change", async (e) => {
      try { applyLAN(await call("todo_lanSelect", e.target.value)); }
      catch (error) { showToast("error", errText(error)); }
    });
    $("taskEarlier").addEventListener("click", () => moveOnPhone(-1));
    $("taskLater").addEventListener("click", () => moveOnPhone(1));
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
      else if (act.dataset.act === "focus") toggleFocus(id);
      else if (act.dataset.act === "edit") openTaskModal(id);
      else if (act.dataset.act === "del") confirmDeleteTodo(id);
    });

    const listEl = $("list");
    listEl.addEventListener("pointerdown", beginDrag);
    listEl.addEventListener("pointermove", moveDrag);
    listEl.addEventListener("pointerup", endDrag);
    listEl.addEventListener("pointercancel", endDrag);
    listEl.addEventListener("lostpointercapture", endDrag);
    window.addEventListener("blur", () => finishDrag(false));
    window.addEventListener("keydown", (e) => { if (e.key === "Escape") finishDrag(false); });

    $("taskClose").addEventListener("click", closeTaskModal);
    $("taskCancel").addEventListener("click", closeTaskModal);
    $("taskSave").addEventListener("click", saveTask);
    $("taskDelete").addEventListener("click", () => confirmDeleteTodo(S.editingId));
    $("deleteDialog").addEventListener("close", finishDeleteTodo);
    $("taskLayer").addEventListener("mousedown", (e) => { if (e.target === $("taskLayer")) closeTaskModal(); });
    $("taskTitle").addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.isComposing) {
        e.preventDefault();
        saveTask();
      }
    });

    $("connectBtn").addEventListener("click", connectDSH);
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
    if (isMobile()) $("composerHint").textContent = "点发送 · 离线会先保存";
    ta.addEventListener("input", autoGrow);
    ta.addEventListener("keydown", (e) => {
      if (!isMobile() && e.key === "Enter" && !e.shiftKey && !e.isComposing) {
        e.preventDefault();
        sendInput();
      }
    });
    $("sendBtn").addEventListener("click", sendInput);
    $("settingsLayer").addEventListener("mousedown", (e) => { if (e.target === $("settingsLayer")) closeSettings(); });

    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        if ($("deleteDialog").open) return;
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
        case "lan":
          applyLAN(d);
          break;
        case "update":
          applyUpdate(d);
          break;
        case "lan-refresh":
          $("lanRefresh").disabled = false;
          showToast(d.count ? "success" : "info", d.count ? `已同步 ${d.count} 台设备` : (d.error || "未发现已配对设备"));
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
      applyLAN(await call("todo_lanState"));
      applyUpdate(await call("todo_updateState"));
      autoGrow();
    } catch (e) {
      showToast("error", "初始化失败：" + errText(e));
      $("statusCard").dataset.state = "error";
      $("statusTitle").textContent = "界面初始化失败";
      $("statusSub").textContent = errText(e);
    }
  });
})();

const state = {
  selectedTraceID: "",
  traceMeta: null,
  projection: null,
  snapshot: null,
  selectedEventIndex: -1,
  selectedObjectID: "",
  filters: {
    calls: false,
    derived: false,
    visible: false,
  },
  requestToken: 0,
};

const els = {
  backBtn: document.getElementById("backBtn"),
  traceStatus: document.getElementById("traceStatus"),
  historyPosition: document.getElementById("historyPosition"),
  historyList: document.getElementById("historyList"),
  traceTitle: document.getElementById("traceTitle"),
  traceSubtitle: document.getElementById("traceSubtitle"),
  graphCanvas: document.getElementById("graphCanvas"),
  graphEmpty: document.getElementById("graphEmpty"),
  filterCalls: document.getElementById("filterCalls"),
  filterDerived: document.getElementById("filterDerived"),
  filterVisible: document.getElementById("filterVisible"),
};

init().catch((err) => {
  console.error(err);
  els.traceSubtitle.textContent = `Initialization failed: ${String(err)}`;
  els.graphEmpty.textContent = "Failed to initialize trace view.";
  els.graphEmpty.style.display = "block";
});

async function init() {
  bindEvents();
  const traceID = traceIDFromPath();
  if (!traceID) {
    clearSelection("No trace id in URL. Open this page from the traces list.");
    return;
  }
  await loadTrace(traceID);
}

function bindEvents() {
  if (els.backBtn) {
    els.backBtn.addEventListener("click", () => {
      window.location.assign("/");
    });
  }

  els.filterCalls.addEventListener("change", () => {
    state.filters.calls = Boolean(els.filterCalls.checked);
    renderHistory();
  });
  els.filterDerived.addEventListener("change", () => {
    state.filters.derived = Boolean(els.filterDerived.checked);
    renderHistory();
  });
  els.filterVisible.addEventListener("change", () => {
    state.filters.visible = Boolean(els.filterVisible.checked);
    renderHistory();
  });
}

async function loadTrace(traceID) {
  state.selectedTraceID = traceID;
  state.selectedObjectID = "";
  document.title = `ODAG ${shortDigest(traceID)}`;

  const token = ++state.requestToken;
  const [metaResp, snapResp] = await Promise.all([
    fetchJSON(`/api/traces/${encodeURIComponent(traceID)}/meta`).catch(() => null),
    fetchJSON(`/api/traces/${encodeURIComponent(traceID)}/snapshot`),
  ]);
  if (token !== state.requestToken) {
    return;
  }

  state.traceMeta = metaResp;
  state.projection = snapResp.projection || null;
  state.snapshot = snapResp.snapshot || null;

  if (state.projection && (state.projection.events || []).length > 0) {
    state.selectedEventIndex = state.projection.events.length - 1;
  } else {
    state.selectedEventIndex = -1;
  }

  renderAll();
}

async function selectEvent(eventIndex) {
  if (!state.selectedTraceID || !state.projection || eventIndex < 0 || eventIndex >= state.projection.events.length) {
    return;
  }

  const event = state.projection.events[eventIndex];
  const unixNano = eventBoundaryUnixNano(event);

  const token = ++state.requestToken;
  const resp = await fetchJSON(`/api/traces/${encodeURIComponent(state.selectedTraceID)}/snapshot?t=${unixNano}`);
  if (token !== state.requestToken) {
    return;
  }

  state.projection = resp.projection || state.projection;
  state.snapshot = resp.snapshot || state.snapshot;
  state.selectedEventIndex = eventIndex;

  renderAll();
}

function clearSelection(msg) {
  state.traceMeta = null;
  state.projection = null;
  state.snapshot = null;
  state.selectedEventIndex = -1;
  state.selectedObjectID = "";
  els.traceStatus.textContent = "idle";
  els.historyPosition.textContent = "0 / 0";
  els.traceTitle.textContent = "No trace selected";
  els.traceSubtitle.textContent = "";
  els.graphCanvas.innerHTML = "";
  els.graphEmpty.textContent = msg;
  els.graphEmpty.style.display = "block";
  els.historyList.innerHTML = "";
}

function renderAll() {
  renderTraceHeader();
  renderHistory();
  renderGraph();
}

function renderTraceHeader() {
  const title = state.projection?.summary?.title || "";
  els.traceTitle.textContent = title || state.selectedTraceID || "Trace";

  const status = state.traceMeta?.status || "unknown";
  els.traceStatus.textContent = status;

  const totalEvents = (state.projection?.events || []).length;
  const current = state.selectedEventIndex >= 0 ? state.selectedEventIndex + 1 : 0;
  els.historyPosition.textContent = `${current} / ${totalEvents}`;

  const selectedEvent =
    state.selectedEventIndex >= 0 && state.projection ? (state.projection.events || [])[state.selectedEventIndex] : null;
  const selectedLabel = selectedEvent ? shortEventLabel(selectedEvent) : "none";
  const objectCount = (state.snapshot?.objects || []).length;
  const warningCount = (state.projection?.warnings || []).length;
  const warnText = warningCount > 0 ? ` | ${warningCount} warnings` : "";
  els.traceSubtitle.textContent = `status: ${status} | objects: ${objectCount} | selected revision: ${selectedLabel}${warnText}`;
}

function renderHistory() {
  if (!state.projection) {
    els.historyList.innerHTML = "";
    return;
  }

  const rows = [];
  const events = state.projection.events || [];
  for (let idx = events.length - 1; idx >= 0; idx--) {
    const event = events[idx];
    if (!eventMatchesFilters(event, state.filters)) {
      continue;
    }
    rows.push({ idx, event });
    if (rows.length >= 600) {
      break;
    }
  }

  if (rows.length === 0) {
    els.historyList.innerHTML = "<div class='history-item'>No events match current filters.</div>";
    return;
  }

  const startUnixNano = state.projection.startUnixNano || 0;
  els.historyList.innerHTML = rows
    .map(({ idx, event }) => {
      const selected = idx === state.selectedEventIndex ? "event-selected" : "";
      const objectMatch = eventMutatesObject(event, state.selectedObjectID) ? "object-match" : "";
      const derived = event.operation ? event.operation.toUpperCase() : event.rawKind.toUpperCase();
      const call = event.rawKind === "call" ? event.name || event.callDigest || event.spanID : event.name || event.spanID;
      const raw = `${event.rawKind} · ${shortDigest(event.spanID)}`;
      const parent = event.parentCallName || (event.parentCallSpanID ? shortDigest(event.parentCallSpanID) : "-");
      const vis = event.objectID ? (event.visible ? "visible" : "hidden") : "-";
      const rel = formatRelTime(eventBoundaryUnixNano(event), startUnixNano);
      return `
      <div class="history-item ${selected} ${objectMatch}" data-event-index="${idx}">
        <div class="history-grid">
          <span><span class="history-pill">${escapeHTML(derived)}</span></span>
          <span class="history-call">${escapeHTML(call)}</span>
          <span class="history-parent">${escapeHTML(parent)}</span>
          <span class="history-vis">${escapeHTML(vis)}</span>
          <span class="history-time">${escapeHTML(rel)}</span>
        </div>
        <div class="history-sub">${escapeHTML(raw)}</div>
      </div>`;
    })
    .join("");

  for (const node of els.historyList.querySelectorAll("[data-event-index]")) {
    node.addEventListener("click", () => {
      const raw = node.getAttribute("data-event-index");
      const idx = Number(raw);
      if (!Number.isInteger(idx) || idx === state.selectedEventIndex) {
        return;
      }
      selectEvent(idx).catch((err) => showError(err));
    });
  }
}

function renderGraph() {
  if (!state.snapshot || !state.projection) {
    els.graphCanvas.innerHTML = "";
    els.graphEmpty.style.display = "block";
    return;
  }

  const objects = state.snapshot.objects || [];
  if (objects.length === 0) {
    els.graphCanvas.innerHTML = "";
    els.graphEmpty.style.display = "block";
    return;
  }

  els.graphEmpty.style.display = "none";

  const cols = Math.max(1, Math.ceil(Math.sqrt(objects.length)));
  const cardW = 300;
  const cardH = 166;
  const gapX = 72;
  const gapY = 84;
  const padding = 52;
  const contentW = padding * 2 + cols * cardW + Math.max(0, cols - 1) * gapX;
  const rows = Math.ceil(objects.length / cols);
  const contentH = padding * 2 + rows * cardH + Math.max(0, rows - 1) * gapY;

  const positions = new Map();
  objects.forEach((obj, idx) => {
    const col = idx % cols;
    const row = Math.floor(idx / cols);
    const x = padding + col * (cardW + gapX);
    const y = padding + row * (cardH + gapY);
    positions.set(obj.id, { x, y });
  });

  const activeSpanIDs = new Set(state.snapshot.activeEventIDs || []);
  const activeObjectIDs = new Set(
    (state.projection.events || [])
      .filter((event) => activeSpanIDs.has(event.spanID) && event.objectID)
      .map((event) => event.objectID),
  );

  const edgeMarkup = (state.snapshot.edges || [])
    .map((edge) => {
      const from = positions.get(edge.fromObjectID);
      const to = positions.get(edge.toObjectID);
      if (!from || !to) {
        return "";
      }
      const x1 = from.x + cardW;
      const y1 = from.y + cardH / 2;
      const x2 = to.x;
      const y2 = to.y + cardH / 2;
      const midX = (x1 + x2) / 2;
      return `<path class="edge-line" d="M ${x1} ${y1} C ${midX} ${y1}, ${midX} ${y2}, ${x2} ${y2}" />`;
    })
    .join("");

  const selectedEvent = state.selectedEventIndex >= 0 ? (state.projection.events || [])[state.selectedEventIndex] : null;
  const selectedEventObjectID = mutationObjectID(selectedEvent);

  const nodeMarkup = objects
    .map((obj) => {
      const pos = positions.get(obj.id);
      const isActive = activeObjectIDs.has(obj.id);
      const selected = obj.id === state.selectedObjectID;
      const eventTarget = obj.id === selectedEventObjectID;
      const latest = obj.stateHistory?.[obj.stateHistory.length - 1] || null;
      const fields = latestStateFields(latest);
      const title = obj.alias || obj.typeName;
      const maxFieldRows = 4;
      const fieldStartY = pos.y + 68;
      const rowHeight = 21;
      const keyX = pos.x + 16;
      const valX = pos.x + 122;
      let rowsMarkup = "";
      if (fields.length > 0) {
        const visibleRows = fields.length > maxFieldRows ? fields.slice(0, maxFieldRows-1) : fields;
        rowsMarkup += visibleRows
          .map((field, idx) => {
            const y = fieldStartY + idx * rowHeight;
            return `<text class="node-prop-key" x="${keyX}" y="${y}">${escapeHTML(field.name)}</text>
        <text class="node-prop-val" x="${valX}" y="${y}">${escapeHTML(field.value)}</text>`;
          })
          .join("");
        if (fields.length > maxFieldRows) {
          const hidden = fields.length - (maxFieldRows - 1);
          const y = fieldStartY + (maxFieldRows - 1) * rowHeight;
          rowsMarkup += `<text class="node-sub" x="${keyX}" y="${y}">+${hidden} more fields</text>`;
        }
      } else if (obj.missingState) {
        rowsMarkup = `<text class="node-warn" x="${keyX}" y="${fieldStartY}">state unavailable</text>`;
      } else {
        rowsMarkup = `<text class="node-sub" x="${keyX}" y="${fieldStartY}">no state fields</text>`;
      }
      const classNames = `node-card${isActive ? " active" : ""}${selected ? " object-selected" : ""}`;
      return `
      <g data-object-id="${escapeHTML(obj.id)}" style="cursor:pointer; animation: fadeIn 220ms ease;">
        <rect class="${classNames}" x="${pos.x}" y="${pos.y}" rx="14" ry="14" width="${cardW}" height="${cardH}" />
        ${eventTarget ? `<rect class="node-event-ring" x="${pos.x - 3}" y="${pos.y - 3}" rx="17" ry="17" width="${cardW + 6}" height="${cardH + 6}" />` : ""}
        <circle class="node-port" cx="${pos.x}" cy="${pos.y + cardH / 2}" r="6" />
        <circle class="node-port" cx="${pos.x + cardW}" cy="${pos.y + cardH / 2}" r="6" />
        ${eventTarget ? `<circle class="node-event-badge" cx="${pos.x + cardW - 16}" cy="${pos.y + 14}" r="7" />` : ""}
        <text class="node-label" x="${pos.x + 16}" y="${pos.y + 38}">${escapeHTML(title)}</text>
        ${rowsMarkup}
      </g>`;
    })
    .join("");

  els.graphCanvas.setAttribute("viewBox", `0 0 ${Math.max(1200, contentW)} ${Math.max(700, contentH)}`);
  els.graphCanvas.innerHTML = `
    <defs>
      <marker id="arrow" viewBox="0 0 12 12" refX="10" refY="6" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path d="M 0 0 L 12 6 L 0 12 z" fill="rgba(140, 162, 192, 0.8)"></path>
      </marker>
    </defs>
    ${edgeMarkup}
    ${nodeMarkup}
  `;

  for (const node of els.graphCanvas.querySelectorAll("[data-object-id]")) {
    node.addEventListener("click", () => {
      state.selectedObjectID = node.getAttribute("data-object-id") || "";
      renderGraph();
      renderHistory();
    });
  }
}

function eventMatchesFilters(event, filters) {
  if (filters.calls && event.rawKind !== "call") {
    return false;
  }
  if (filters.derived && !event.operation) {
    return false;
  }
  if (filters.visible && !event.visible) {
    return false;
  }
  return true;
}

function eventMutatesObject(event, objectID) {
  if (!objectID) {
    return false;
  }
  return mutationObjectID(event) === objectID;
}

function mutationObjectID(event) {
  if (!event || !event.objectID) {
    return "";
  }
  if (event.operation === "create" || event.operation === "mutate") {
    return event.objectID;
  }
  return "";
}

function latestStateFields(latestState) {
  const payload = latestState?.outputState;
  if (!payload || typeof payload !== "object") {
    return [];
  }
  const fields = payload.fields;
  if (!fields || typeof fields !== "object") {
    return [];
  }

  const out = [];
  for (const [fallbackName, raw] of Object.entries(fields)) {
    if (!raw || typeof raw !== "object") {
      continue;
    }
    const name = typeof raw.name === "string" && raw.name ? raw.name : fallbackName;
    const value = formatStateValue(raw.value);
    out.push({ name, value });
  }
  out.sort((a, b) => a.name.localeCompare(b.name));
  return out;
}

function formatStateValue(v) {
  if (v === null) {
    return "null";
  }
  if (v === undefined) {
    return "";
  }
  if (typeof v === "string") {
    if (looksLikeDigest(v)) {
      return shortDigest(v);
    }
    return truncateText(v, 28);
  }
  if (typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  if (Array.isArray(v)) {
    return `[${v.length}]`;
  }
  if (typeof v === "object") {
    if (typeof v.error === "string" && v.error) {
      return `error: ${truncateText(v.error, 18)}`;
    }
    return "{...}";
  }
  return String(v);
}

function looksLikeDigest(v) {
  return (v.startsWith("xxh3:") || v.startsWith("sha256:")) && v.length > 12;
}

function truncateText(v, maxLen) {
  if (!v || v.length <= maxLen) {
    return v;
  }
  if (maxLen <= 1) {
    return v.slice(0, maxLen);
  }
  return `${v.slice(0, maxLen - 1)}…`;
}

function shortEventLabel(event) {
  if (!event) {
    return "none";
  }
  if (event.rawKind === "call") {
    return event.name || event.callDigest || shortDigest(event.spanID);
  }
  return `${event.rawKind}:${event.name || shortDigest(event.spanID)}`;
}

function eventBoundaryUnixNano(event) {
  if (!event) {
    return 0;
  }
  return event.endUnixNano || event.startUnixNano || 0;
}

function showError(err) {
  console.error(err);
  els.traceSubtitle.textContent = `Error: ${String(err)}`;
}

async function fetchJSON(url, init) {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`${resp.status} ${resp.statusText}: ${body}`);
  }
  return await resp.json();
}

function traceIDFromPath() {
  const prefix = "/traces/";
  if (!window.location.pathname.startsWith(prefix)) {
    return "";
  }
  return decodeURIComponent(window.location.pathname.slice(prefix.length));
}

function shortDigest(v) {
  if (!v) {
    return "-";
  }
  return v.length > 12 ? `${v.slice(0, 12)}...` : v;
}

function formatRelTime(unixNano, startUnixNano) {
  if (!unixNano || !startUnixNano) {
    return "0 ms";
  }
  const ms = (unixNano - startUnixNano) / 1e6;
  return `${ms.toFixed(1)} ms`;
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

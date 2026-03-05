const state = {
  selectedTraceID: "",
  traceMeta: null,
  projection: null,
  snapshot: null,
  frameSteps: [],
  frameIndex: 0,
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
  timelineStatus: document.getElementById("timelineStatus"),
  timelineCurrent: document.getElementById("timelineCurrent"),
  timelineEnd: document.getElementById("timelineEnd"),
  firstBtn: document.getElementById("firstBtn"),
  prevBtn: document.getElementById("prevBtn"),
  nextBtn: document.getElementById("nextBtn"),
  lastBtn: document.getElementById("lastBtn"),
  traceStats: document.getElementById("traceStats"),
  traceTitle: document.getElementById("traceTitle"),
  graphCanvas: document.getElementById("graphCanvas"),
  graphEmpty: document.getElementById("graphEmpty"),
  inspector: document.getElementById("inspector"),
  filterCalls: document.getElementById("filterCalls"),
  filterDerived: document.getElementById("filterDerived"),
  filterVisible: document.getElementById("filterVisible"),
  eventList: document.getElementById("eventList"),
};

init().catch((err) => {
  console.error(err);
  els.inspector.innerHTML = renderError(`Initialization failed: ${String(err)}`);
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

  els.firstBtn.addEventListener("click", () => {
    loadFrame(0).catch((err) => showError(err));
  });

  els.prevBtn.addEventListener("click", () => {
    loadFrame(state.frameIndex - 1).catch((err) => showError(err));
  });

  els.nextBtn.addEventListener("click", () => {
    loadFrame(Math.min(state.frameIndex + 1, state.frameSteps.length - 1)).catch((err) => showError(err));
  });

  els.lastBtn.addEventListener("click", () => {
    if (state.frameSteps.length === 0) {
      return;
    }
    loadFrame(state.frameSteps.length - 1).catch((err) => showError(err));
  });

  els.filterCalls.addEventListener("change", () => {
    state.filters.calls = Boolean(els.filterCalls.checked);
    renderEvents();
  });
  els.filterDerived.addEventListener("change", () => {
    state.filters.derived = Boolean(els.filterDerived.checked);
    renderEvents();
  });
  els.filterVisible.addEventListener("change", () => {
    state.filters.visible = Boolean(els.filterVisible.checked);
    renderEvents();
  });
}

async function loadTrace(traceID, opts = {}) {
  const preserveStep = Boolean(opts.preserveStep);
  const targetStep = preserveStep ? state.frameIndex : 0;

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
  state.frameSteps = buildFrameSteps(state.projection);
  state.frameIndex = clampStep(targetStep);

  if (state.frameSteps.length > 0) {
    const stepResp = await fetchJSON(`/api/traces/${encodeURIComponent(traceID)}/snapshot?step=${state.frameIndex}`);
    if (token !== state.requestToken) {
      return;
    }
    state.projection = stepResp.projection || state.projection;
    state.snapshot = stepResp.snapshot || snapResp.snapshot || null;
  } else {
    state.snapshot = snapResp.snapshot || null;
  }

  syncStepControls();
  renderAll();
}

async function loadFrame(frameIndex) {
  if (!state.selectedTraceID || !state.projection || state.frameSteps.length === 0) {
    return;
  }

  const idx = Math.max(0, Math.min(frameIndex, state.frameSteps.length - 1));
  const token = ++state.requestToken;
  const resp = await fetchJSON(`/api/traces/${encodeURIComponent(state.selectedTraceID)}/snapshot?step=${idx}`);
  if (token !== state.requestToken) {
    return;
  }

  state.projection = resp.projection || state.projection;
  state.snapshot = resp.snapshot || state.snapshot;
  state.frameSteps = buildFrameSteps(state.projection);
  state.frameIndex = clampStep(idx);
  syncStepControls();
  renderAll();
}

function buildFrameSteps(projection) {
  if (!projection) {
    return [];
  }
  return (projection.events || []).filter((event) => Boolean(event.objectID) && Boolean(event.visible));
}

function clampStep(step) {
  const max = Math.max(0, state.frameSteps.length - 1);
  if (state.frameSteps.length === 0) {
    return 0;
  }
  return Math.max(0, Math.min(step, max));
}

function syncStepControls() {
  const max = Math.max(0, state.frameSteps.length - 1);
  const hasSteps = state.frameSteps.length > 0;
  els.firstBtn.disabled = !hasSteps || state.frameIndex <= 0;
  els.prevBtn.disabled = !hasSteps || state.frameIndex <= 0;
  els.nextBtn.disabled = !hasSteps || state.frameIndex >= max;
  els.lastBtn.disabled = !hasSteps || state.frameIndex >= max;
}

function clearSelection(msg) {
  state.traceMeta = null;
  state.projection = null;
  state.snapshot = null;
  state.frameSteps = [];
  state.frameIndex = 0;
  syncStepControls();
  els.traceStats.textContent = "";
  els.traceTitle.textContent = "No trace selected";
  els.timelineStatus.textContent = "idle";
  els.timelineCurrent.textContent = "0";
  els.timelineEnd.textContent = "0";
  els.graphCanvas.innerHTML = "";
  els.graphEmpty.textContent = msg;
  els.graphEmpty.style.display = "block";
  els.eventList.innerHTML = "";
  els.inspector.innerHTML = "";
}

function renderAll() {
  renderTimeline();
  renderTraceTitle();
  renderGraph();
  renderEvents();
  renderInspector();
}

function renderTimeline() {
  els.timelineStatus.textContent = state.traceMeta?.status || "unknown";

  if (!state.projection || !state.snapshot || state.frameSteps.length === 0) {
    els.timelineCurrent.textContent = "0";
    els.timelineEnd.textContent = "0";
    els.traceStats.textContent = "";
    syncStepControls();
    return;
  }

  const currentStep = state.frameIndex + 1;
  const totalSteps = state.frameSteps.length;
  els.timelineCurrent.textContent = String(currentStep);
  els.timelineEnd.textContent = String(totalSteps);

  const objectCount = (state.snapshot.objects || []).length;
  const eventCount = (state.snapshot.events || []).length;
  const warningCount = (state.projection.warnings || []).length;
  const warnText = warningCount > 0 ? ` | ${warningCount} warnings` : "";
  els.traceStats.textContent = `${objectCount} objects | ${eventCount} events${warnText}`;
  syncStepControls();
}

function renderTraceTitle() {
  const title = state.projection?.summary?.title || "";
  els.traceTitle.textContent = title || state.selectedTraceID || "Trace";
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
  const cardW = 260;
  const cardH = 92;
  const gapX = 68;
  const gapY = 76;
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

  const nodeMarkup = objects
    .map((obj) => {
      const pos = positions.get(obj.id);
      const isActive = activeObjectIDs.has(obj.id);
      const selected = obj.id === state.selectedObjectID;
      const latest = obj.stateHistory?.[obj.stateHistory.length - 1];
      const subtitle = `${obj.alias} · ${shortDigest(latest?.stateDigest || "")}`;
      const warning = obj.missingState ? `<tspan class="warn-pill">state unavailable</tspan>` : "";
      const classNames = `node-card${isActive ? " active" : ""}${selected ? " active" : ""}`;
      return `
      <g data-object-id="${escapeHTML(obj.id)}" style="cursor:pointer; animation: fadeIn 220ms ease;">
        <rect class="${classNames}" x="${pos.x}" y="${pos.y}" rx="14" ry="14" width="${cardW}" height="${cardH}" />
        <circle class="node-port" cx="${pos.x}" cy="${pos.y + cardH / 2}" r="5" />
        <circle class="node-port" cx="${pos.x + cardW}" cy="${pos.y + cardH / 2}" r="5" />
        <text class="node-label" x="${pos.x + 14}" y="${pos.y + 28}">${escapeHTML(obj.typeName)}</text>
        <text class="node-sub" x="${pos.x + 14}" y="${pos.y + 49}">${escapeHTML(subtitle)}</text>
        <text class="node-sub" x="${pos.x + 14}" y="${pos.y + 69}">${escapeHTML(
          `${obj.stateHistory.length} mutations`,
        )}${warning}</text>
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
      renderInspector();
    });
  }
}

function renderEvents() {
  if (!state.snapshot || !state.projection) {
    els.eventList.innerHTML = "";
    return;
  }
  const active = new Set(state.snapshot.activeEventIDs || []);
  const recentEvents = [...(state.snapshot.events || [])]
    .filter((event) => eventMatchesFilters(event, state.filters))
    .slice(-120)
    .reverse();
  if (recentEvents.length === 0) {
    els.eventList.innerHTML = "<div class='event-item'>No events match current filters.</div>";
    return;
  }

  els.eventList.innerHTML = recentEvents
    .map((event) => {
      const activeClass = active.has(event.spanID) ? "active" : "";
      const callLabel = event.rawKind === "call" ? event.name || event.callDigest || event.spanID : "-";
      const rawLabel = `${event.rawKind || "span"} · ${event.name || "-"} · ${shortDigest(event.spanID)}`;
      const parentLabel = event.parentCallName || (event.parentCallSpanID ? shortDigest(event.parentCallSpanID) : "-");
      const detail = `${formatRelTime(event.endUnixNano, state.projection.startUnixNano)} · ${event.statusCode || "STATUS_CODE_UNSET"} · span ${shortDigest(event.spanID)}`;
      return `
      <div class="event-item ${activeClass}">
        <div class="event-grid">
          <span>${escapeHTML(callLabel)}</span>
          <span>${escapeHTML(parentLabel)}</span>
          <span>${escapeHTML(rawLabel)}</span>
          <span>${escapeHTML(event.operation || "-")}</span>
          <span>${event.topLevel ? "yes" : "no"}</span>
          <span>${Number(event.callDepth || 0)}</span>
          <span>${event.objectID ? (event.visible ? "yes" : "no") : "-"}</span>
          <span>${escapeHTML(shortDigest(event.objectID || ""))}</span>
        </div>
        <div class="event-sub">${escapeHTML(detail)}</div>
      </div>`;
    })
    .join("");
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

function renderInspector() {
  if (!state.snapshot || !state.projection) {
    els.inspector.innerHTML = "";
    return;
  }
  const selectedObject = (state.snapshot.objects || []).find((obj) => obj.id === state.selectedObjectID);
  if (!selectedObject) {
    els.inspector.innerHTML = "";
    return;
  }

  const stateRows = selectedObject.stateHistory
    .slice()
    .reverse()
    .map((stateRow) => {
      return `
        <div class="state-row">
          <div class="inspector-key">State</div>
          <div class="inspector-value">${escapeHTML(stateRow.stateDigest)}</div>
          <div class="inspector-key">Event</div>
          <div class="inspector-value">${escapeHTML(stateRow.spanID)} @ ${escapeHTML(
            formatRelTime(stateRow.endUnixNano, state.projection.startUnixNano),
          )}</div>
        </div>
      `;
    })
    .join("");

  els.inspector.innerHTML = `
    <div class="inspector-block">
      <div class="inspector-key">Object</div>
      <div class="inspector-value">${escapeHTML(selectedObject.alias)} (${escapeHTML(selectedObject.typeName)})</div>
      <div class="inspector-key">Current State</div>
      <div class="inspector-value">${escapeHTML(
        selectedObject.stateHistory[selectedObject.stateHistory.length - 1]?.stateDigest || "",
      )}</div>
      <div class="inspector-key">Referenced By Top-Level</div>
      <div class="inspector-value">${selectedObject.referencedByTop ? "yes" : "no"}</div>
    </div>
    <div class="inspector-block">
      <div class="inspector-key">Mutation History (${selectedObject.stateHistory.length})</div>
      ${stateRows}
    </div>
  `;
}

function showError(err) {
  console.error(err);
  els.inspector.innerHTML = renderError(String(err));
}

function renderError(msg) {
  return `<div class="inspector-block"><div class="inspector-key">Info</div><div class="inspector-value">${escapeHTML(
    msg,
  )}</div></div>`;
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

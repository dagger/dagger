const state = {
  traces: [],
  selectedTraceID: "",
  projection: null,
  snapshot: null,
  frameSteps: [],
  frameIndex: 0,
  selectedObjectID: "",
  playTimer: null,
  requestToken: 0,
};

const els = {
  refreshTracesBtn: document.getElementById("refreshTracesBtn"),
  importCloudBtn: document.getElementById("importCloudBtn"),
  cloudTraceID: document.getElementById("cloudTraceID"),
  cloudOrg: document.getElementById("cloudOrg"),
  traceList: document.getElementById("traceList"),
  timelineSlider: document.getElementById("timelineSlider"),
  timelineCurrent: document.getElementById("timelineCurrent"),
  timelineEnd: document.getElementById("timelineEnd"),
  playPauseBtn: document.getElementById("playPauseBtn"),
  stepBtn: document.getElementById("stepBtn"),
  endBtn: document.getElementById("endBtn"),
  traceStats: document.getElementById("traceStats"),
  graphCanvas: document.getElementById("graphCanvas"),
  graphEmpty: document.getElementById("graphEmpty"),
  inspector: document.getElementById("inspector"),
  eventList: document.getElementById("eventList"),
};

init().catch((err) => {
  console.error(err);
  els.inspector.innerHTML = renderError(`Initialization failed: ${String(err)}`);
});

async function init() {
  bindEvents();
  await refreshTraces();
}

function bindEvents() {
  els.refreshTracesBtn.addEventListener("click", () => {
    refreshTraces().catch((err) => showError(err));
  });

  els.importCloudBtn.addEventListener("click", () => {
    importTraceFromCloud().catch((err) => showError(err));
  });

  els.timelineSlider.addEventListener("input", () => {
    const idx = Number.parseInt(els.timelineSlider.value, 10) || 0;
    loadFrame(idx).catch((err) => showError(err));
  });

  els.playPauseBtn.addEventListener("click", () => {
    if (state.playTimer !== null) {
      stopPlaying();
      return;
    }
    startPlaying();
  });

  els.stepBtn.addEventListener("click", () => {
    stopPlaying();
    loadFrame(Math.min(state.frameIndex + 1, state.frameSteps.length - 1)).catch((err) => showError(err));
  });

  els.endBtn.addEventListener("click", () => {
    stopPlaying();
    if (state.frameSteps.length === 0) {
      return;
    }
    loadFrame(state.frameSteps.length - 1).catch((err) => showError(err));
  });
}

async function refreshTraces() {
  const resp = await fetchJSON("/api/traces?limit=200");
  state.traces = resp.traces || [];
  renderTraceList();

  if (state.traces.length === 0) {
    clearSelection("No traces available yet. Run `odag run dagger call ...` to capture one.");
    return;
  }

  if (!state.selectedTraceID || !state.traces.some((t) => t.traceID === state.selectedTraceID)) {
    await selectTrace(state.traces[0].traceID);
  } else {
    await selectTrace(state.selectedTraceID);
  }
}

async function importTraceFromCloud() {
  const traceID = (els.cloudTraceID.value || "").trim();
  if (!traceID) {
    throw new Error("Cloud trace ID is required");
  }
  const org = (els.cloudOrg.value || "").trim();

  els.importCloudBtn.disabled = true;
  const prevLabel = els.importCloudBtn.textContent;
  els.importCloudBtn.textContent = "Importing...";
  try {
    await fetchJSON("/api/traces/open", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        mode: "cloud",
        traceID,
        org: org || undefined,
      }),
    });
    await refreshTraces();
    await selectTrace(traceID);
  } finally {
    els.importCloudBtn.disabled = false;
    els.importCloudBtn.textContent = prevLabel;
  }
}

async function selectTrace(traceID) {
  state.selectedTraceID = traceID;
  state.selectedObjectID = "";
  stopPlaying();
  renderTraceList();

  const token = ++state.requestToken;
  const resp = await fetchJSON(`/api/traces/${encodeURIComponent(traceID)}/snapshot`);
  if (token !== state.requestToken) {
    return;
  }

  state.projection = resp.projection || null;
  state.frameSteps = buildFrameSteps(state.projection);
  state.frameIndex = Math.max(0, state.frameSteps.length - 1);

  if (state.frameSteps.length > 0) {
    const stepResp = await fetchJSON(
      `/api/traces/${encodeURIComponent(traceID)}/snapshot?step=${state.frameIndex}`,
    );
    if (token !== state.requestToken) {
      return;
    }
    state.projection = stepResp.projection || state.projection;
    state.snapshot = stepResp.snapshot || resp.snapshot || null;
  } else {
    state.snapshot = resp.snapshot || null;
  }

  syncTimelineControl();

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
  state.frameIndex = idx;
  syncTimelineControl();
  renderAll();
}

function buildFrameSteps(projection) {
  if (!projection) {
    return [];
  }
  return (projection.events || []).filter((event) => Boolean(event.objectID));
}

function syncTimelineControl() {
  const max = Math.max(0, state.frameSteps.length - 1);
  els.timelineSlider.max = String(max);
  els.timelineSlider.value = String(Math.min(state.frameIndex, max));
}

function startPlaying() {
  if (state.playTimer !== null || state.frameSteps.length === 0) {
    return;
  }
  els.playPauseBtn.textContent = "Pause";
  state.playTimer = window.setInterval(() => {
    if (state.frameIndex >= state.frameSteps.length - 1) {
      stopPlaying();
      return;
    }
    loadFrame(state.frameIndex + 1).catch((err) => {
      stopPlaying();
      showError(err);
    });
  }, 700);
}

function stopPlaying() {
  if (state.playTimer !== null) {
    window.clearInterval(state.playTimer);
    state.playTimer = null;
  }
  els.playPauseBtn.textContent = "Play";
}

function clearSelection(msg) {
  state.projection = null;
  state.snapshot = null;
  state.frameSteps = [];
  state.frameIndex = 0;
  stopPlaying();
  syncTimelineControl();
  els.traceStats.textContent = "";
  els.timelineCurrent.textContent = "Step 0";
  els.timelineEnd.textContent = "Step 0";
  els.graphCanvas.innerHTML = "";
  els.graphEmpty.textContent = msg;
  els.graphEmpty.style.display = "block";
  els.eventList.innerHTML = "";
  els.inspector.innerHTML = renderError(msg);
}

function renderAll() {
  renderTimeline();
  renderGraph();
  renderEvents();
  renderInspector();
}

function renderTraceList() {
  if (state.traces.length === 0) {
    els.traceList.innerHTML = "<div class='trace-item'>No traces yet.</div>";
    return;
  }

  const rows = state.traces.map((trace) => {
    const selected = trace.traceID === state.selectedTraceID ? "selected" : "";
    return `
      <div class="trace-item ${selected}" data-trace-id="${escapeHTML(trace.traceID)}">
        <div class="trace-id">${escapeHTML(trace.traceID)}</div>
        <div class="trace-meta">
          <span>${escapeHTML(trace.status || "unknown")}</span>
          <span>${Number(trace.spanCount || 0)} spans</span>
        </div>
      </div>
    `;
  });

  els.traceList.innerHTML = rows.join("");
  for (const node of els.traceList.querySelectorAll("[data-trace-id]")) {
    node.addEventListener("click", () => {
      const id = node.getAttribute("data-trace-id");
      if (!id || id === state.selectedTraceID) {
        return;
      }
      selectTrace(id).catch((err) => showError(err));
    });
  }
}

function renderTimeline() {
  if (!state.projection || !state.snapshot || state.frameSteps.length === 0) {
    els.timelineCurrent.textContent = "Step 0";
    els.timelineEnd.textContent = "Step 0";
    els.traceStats.textContent = "";
    return;
  }

  const currentStep = state.frameIndex + 1;
  const totalSteps = state.frameSteps.length;
  const currentEvent = state.frameSteps[state.frameIndex];
  const lastEvent = state.frameSteps[totalSteps - 1];
  els.timelineCurrent.textContent = `Step ${currentStep} · ${formatRelTime(
    currentEvent?.endUnixNano || 0,
    state.projection.startUnixNano,
  )}`;
  els.timelineEnd.textContent = `Step ${totalSteps} · ${formatRelTime(
    lastEvent?.endUnixNano || 0,
    state.projection.startUnixNano,
  )}`;
  const objectCount = (state.snapshot.objects || []).length;
  const eventCount = (state.snapshot.events || []).length;
  const warningCount = (state.projection.warnings || []).length;
  const warnText = warningCount > 0 ? ` | ${warningCount} warnings` : "";
  els.traceStats.textContent = `${objectCount} objects | ${eventCount} events${warnText}`;
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
  const recentEvents = [...(state.snapshot.events || [])].slice(-120).reverse();
  if (recentEvents.length === 0) {
    els.eventList.innerHTML = "<div class='event-item'>No events in this frame.</div>";
    return;
  }

  els.eventList.innerHTML = recentEvents
    .map((event) => {
      const activeClass = active.has(event.spanID) ? "active" : "";
      const title = `${event.kind.toUpperCase()} ${event.name || event.callDigest || event.spanID}`;
      const detail = `${formatRelTime(event.endUnixNano, state.projection.startUnixNano)} · ${event.statusCode || "STATUS_CODE_UNSET"}`;
      return `
      <div class="event-item ${activeClass}">
        <div class="event-top"><span>${escapeHTML(title)}</span><span>${escapeHTML(shortDigest(event.objectID || ""))}</span></div>
        <div class="event-sub">${escapeHTML(detail)}</div>
      </div>`;
    })
    .join("");
}

function renderInspector() {
  if (!state.snapshot || !state.projection) {
    els.inspector.innerHTML = renderError("No trace selected.");
    return;
  }

  const selectedObject = (state.snapshot.objects || []).find((obj) => obj.id === state.selectedObjectID);
  if (selectedObject) {
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
    return;
  }

  const currentEvent = (state.snapshot.events || [])[state.snapshot.events.length - 1];
  if (!currentEvent) {
    els.inspector.innerHTML = renderError("No event available for this frame.");
    return;
  }

  const inputsMarkup =
    (currentEvent.inputs || [])
      .map((input) => `<li>${escapeHTML(input.path || input.name)}: <code>${escapeHTML(shortDigest(input.stateDigest))}</code></li>`)
      .join("") || "<li>No object inputs</li>";

  const warningMarkup = (state.projection.warnings || [])
    .slice(0, 12)
    .map((warning) => `<li>${escapeHTML(warning)}</li>`)
    .join("");

  els.inspector.innerHTML = `
    <div class="inspector-block">
      <div class="inspector-key">Current Event</div>
      <div class="inspector-value">${escapeHTML(currentEvent.name || currentEvent.callDigest || currentEvent.spanID)}</div>
      <div class="inspector-key">Kind</div>
      <div class="inspector-value">${escapeHTML(currentEvent.kind)}</div>
      <div class="inspector-key">Status</div>
      <div class="inspector-value">${escapeHTML(currentEvent.statusCode || "STATUS_CODE_UNSET")}</div>
    </div>
    <div class="inspector-block">
      <div class="inspector-key">Inputs</div>
      <ul>${inputsMarkup}</ul>
    </div>
    <div class="inspector-block">
      <div class="inspector-key">Warnings</div>
      <ul>${warningMarkup || "<li>None</li>"}</ul>
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

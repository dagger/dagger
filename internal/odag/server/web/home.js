const state = {
  traces: [],
};
const createdAtFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

const els = {
  refreshTracesBtn: document.getElementById("refreshTracesBtn"),
  importCloudBtn: document.getElementById("importCloudBtn"),
  cloudTraceID: document.getElementById("cloudTraceID"),
  cloudOrg: document.getElementById("cloudOrg"),
  traceList: document.getElementById("traceList"),
};

init().catch((err) => {
  renderInfo(`Initialization failed: ${String(err)}`);
});

async function init() {
  bindEvents();
  await refreshTraces();
}

function bindEvents() {
  els.refreshTracesBtn.addEventListener("click", () => {
    refreshTraces().catch((err) => renderInfo(String(err)));
  });

  els.importCloudBtn.addEventListener("click", () => {
    importTraceFromCloud().catch((err) => renderInfo(String(err)));
  });
}

async function refreshTraces() {
  const resp = await fetchJSON("/api/traces?limit=200");
  state.traces = resp.traces || [];
  renderTraceList();
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
    window.location.assign(`/traces/${encodeURIComponent(traceID)}`);
  } finally {
    els.importCloudBtn.disabled = false;
    els.importCloudBtn.textContent = prevLabel;
  }
}

function renderTraceList() {
  if (state.traces.length === 0) {
    els.traceList.innerHTML = "<div class='trace-item'>No traces yet. Run `odag run ...` to capture one.</div>";
    return;
  }

  const rows = state.traces.map((trace) => {
    const created = formatCreatedAt(trace);
    return `
      <div class="trace-item" data-trace-id="${escapeHTML(trace.traceID)}">
        <div class="trace-id">${escapeHTML(trace.traceID)}</div>
        <div class="trace-meta">
          <span>${escapeHTML(trace.status || "unknown")}</span>
          <span>${Number(trace.spanCount || 0)} spans</span>
        </div>
        <div class="trace-created">Created ${escapeHTML(created)}</div>
      </div>
    `;
  });

  els.traceList.innerHTML = rows.join("");
  for (const node of els.traceList.querySelectorAll("[data-trace-id]")) {
    node.addEventListener("click", () => {
      const traceID = node.getAttribute("data-trace-id");
      if (!traceID) {
        return;
      }
      window.location.assign(`/traces/${encodeURIComponent(traceID)}`);
    });
  }
}

function renderInfo(msg) {
  els.traceList.innerHTML = `<div class='trace-item'>${escapeHTML(msg)}</div>`;
}

async function fetchJSON(url, init) {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`${resp.status} ${resp.statusText}: ${body}`);
  }
  return await resp.json();
}

function formatCreatedAt(trace) {
  const firstSeen = typeof trace?.firstSeen === "string" ? trace.firstSeen : "";
  if (firstSeen) {
    const dt = new Date(firstSeen);
    if (!Number.isNaN(dt.getTime())) {
      return createdAtFormatter.format(dt);
    }
  }

  const firstSeenUnixNano = Number(trace?.firstSeenUnixNano || 0);
  if (firstSeenUnixNano > 0) {
    const dt = new Date(firstSeenUnixNano / 1e6);
    if (!Number.isNaN(dt.getTime())) {
      return createdAtFormatter.format(dt);
    }
  }

  return "unknown";
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

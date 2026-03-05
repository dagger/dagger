const state = {
  traces: [],
};
const relativeTimeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

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
    els.traceList.innerHTML = "<div class='trace-empty'>No traces yet. Run `odag run ...` to capture one.</div>";
    return;
  }

  const rows = state.traces.map((trace) => {
    const created = formatCreatedRelative(trace);
    const status = String(trace.status || "").toLowerCase();
    const statusClass = statusClassForTrace(status);
    const statusLabel = status || "unknown";
    return `
      <div class="trace-row" data-trace-id="${escapeHTML(trace.traceID)}">
        <span class="trace-cell trace-cell-id">${escapeHTML(trace.traceID)}</span>
        <span class="trace-cell trace-cell-created">${escapeHTML(created)}</span>
        <span class="trace-cell trace-cell-spans">${Number(trace.spanCount || 0)}</span>
        <span class="trace-cell trace-cell-status">
          <span class="status-dot ${statusClass}" title="${escapeHTML(statusLabel)}" aria-label="${escapeHTML(statusLabel)}"></span>
        </span>
      </div>
    `;
  });

  els.traceList.innerHTML = `
    <div class="trace-table">
      <div class="trace-head">
        <span>Trace</span>
        <span>Created</span>
        <span>Spans</span>
        <span>Status</span>
      </div>
      <div class="trace-body">
        ${rows.join("")}
      </div>
    </div>
  `;
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
  els.traceList.innerHTML = `<div class='trace-empty'>${escapeHTML(msg)}</div>`;
}

async function fetchJSON(url, init) {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`${resp.status} ${resp.statusText}: ${body}`);
  }
  return await resp.json();
}

function formatCreatedRelative(trace) {
  const createdAt = traceCreatedDate(trace);
  if (!createdAt) {
    return "unknown";
  }
  const diffSeconds = Math.round((createdAt.getTime() - Date.now()) / 1000);
  const abs = Math.abs(diffSeconds);
  if (abs < 60) {
    return relativeTimeFormatter.format(diffSeconds, "second");
  }
  const diffMinutes = Math.round(diffSeconds / 60);
  if (Math.abs(diffMinutes) < 60) {
    return relativeTimeFormatter.format(diffMinutes, "minute");
  }
  const diffHours = Math.round(diffMinutes / 60);
  if (Math.abs(diffHours) < 24) {
    return relativeTimeFormatter.format(diffHours, "hour");
  }
  const diffDays = Math.round(diffHours / 24);
  if (Math.abs(diffDays) < 30) {
    return relativeTimeFormatter.format(diffDays, "day");
  }
  const diffMonths = Math.round(diffDays / 30);
  if (Math.abs(diffMonths) < 12) {
    return relativeTimeFormatter.format(diffMonths, "month");
  }
  const diffYears = Math.round(diffDays / 365);
  return relativeTimeFormatter.format(diffYears, "year");
}

function traceCreatedDate(trace) {
  const firstSeen = typeof trace?.firstSeen === "string" ? trace.firstSeen : "";
  if (firstSeen) {
    const dt = new Date(firstSeen);
    if (!Number.isNaN(dt.getTime())) {
      return dt;
    }
  }
  const firstSeenUnixNano = Number(trace?.firstSeenUnixNano || 0);
  if (firstSeenUnixNano > 0) {
    const dt = new Date(firstSeenUnixNano / 1e6);
    if (!Number.isNaN(dt.getTime())) {
      return dt;
    }
  }
  return null;
}

function statusClassForTrace(status) {
  switch (status) {
    case "ingesting":
      return "is-ingesting";
    case "failed":
      return "is-failed";
    case "completed":
    case "succeeded":
    case "success":
      return "is-succeeded";
    default:
      return "is-neutral";
  }
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

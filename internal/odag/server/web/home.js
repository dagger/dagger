const state = {
  traces: [],
  spans: { items: [], nextCursor: "" },
  calls: { items: [], nextCursor: "" },
  snapshots: { items: [], nextCursor: "" },
  bindings: { items: [], nextCursor: "" },
  mutations: { items: [], nextCursor: "" },
  sessions: { items: [], nextCursor: "" },
  clients: { items: [], nextCursor: "" },
  render: null,
  options: {
    traceID: "",
    includeInternal: false,
    keepRules: true,
  },
  liveTimer: 0,
  liveBusy: false,
};

const relativeTimeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const liveRefreshIntervalMs = 3000;
const v2Limit = 250;

const els = {
  refreshTracesBtn: document.getElementById("refreshTracesBtn"),
  clearTraceFilterBtn: document.getElementById("clearTraceFilterBtn"),
  importCloudBtn: document.getElementById("importCloudBtn"),
  cloudTraceID: document.getElementById("cloudTraceID"),
  cloudOrg: document.getElementById("cloudOrg"),
  traceFilter: document.getElementById("traceFilter"),
  includeInternal: document.getElementById("includeInternal"),
  renderKeepRules: document.getElementById("renderKeepRules"),
  countSpans: document.getElementById("countSpans"),
  countCalls: document.getElementById("countCalls"),
  countSnapshots: document.getElementById("countSnapshots"),
  countBindings: document.getElementById("countBindings"),
  countMutations: document.getElementById("countMutations"),
  countSessions: document.getElementById("countSessions"),
  countClients: document.getElementById("countClients"),
  countRenderObjects: document.getElementById("countRenderObjects"),
  mutationsMeta: document.getElementById("mutationsMeta"),
  bindingsMeta: document.getElementById("bindingsMeta"),
  callsMeta: document.getElementById("callsMeta"),
  sessionsMeta: document.getElementById("sessionsMeta"),
  mutationTable: document.getElementById("mutationTable"),
  bindingTable: document.getElementById("bindingTable"),
  callTable: document.getElementById("callTable"),
  sessionClientTable: document.getElementById("sessionClientTable"),
  traceList: document.getElementById("traceList"),
};

init().catch((err) => {
  renderInfo(`Initialization failed: ${String(err)}`);
});

async function init() {
  bindEvents();
  await refreshAll();
  startLiveRefresh();
}

function bindEvents() {
  els.refreshTracesBtn.addEventListener("click", () => {
    refreshAll().catch((err) => renderInfo(String(err)));
  });

  els.clearTraceFilterBtn.addEventListener("click", () => {
    els.traceFilter.value = "";
    state.options.traceID = "";
    refreshAll().catch((err) => renderInfo(String(err)));
  });

  els.importCloudBtn.addEventListener("click", () => {
    importTraceFromCloud().catch((err) => renderInfo(String(err)));
  });

  els.traceFilter.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") {
      return;
    }
    state.options.traceID = (els.traceFilter.value || "").trim();
    refreshAll().catch((err) => renderInfo(String(err)));
  });

  els.includeInternal.addEventListener("change", () => {
    state.options.includeInternal = Boolean(els.includeInternal.checked);
    refreshAll().catch((err) => renderInfo(String(err)));
  });

  els.renderKeepRules.addEventListener("change", () => {
    state.options.keepRules = Boolean(els.renderKeepRules.checked);
    refreshAll().catch((err) => renderInfo(String(err)));
  });

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden && !state.liveBusy) {
      state.liveBusy = true;
      refreshAll()
        .catch((err) => {
          console.error("v2 refresh failed", err);
        })
        .finally(() => {
          state.liveBusy = false;
        });
    }
  });

  window.addEventListener("beforeunload", () => {
    stopLiveRefresh();
  });
}

async function refreshAll() {
  state.options.traceID = (els.traceFilter.value || "").trim();
  state.options.includeInternal = Boolean(els.includeInternal.checked);
  state.options.keepRules = Boolean(els.renderKeepRules.checked);

  const query = buildV2Query();
  const renderQuery = buildRenderQuery();

  const [tracesResp, spansResp, callsResp, snapshotsResp, bindingsResp, mutationsResp, sessionsResp, clientsResp, renderResp] =
    await Promise.all([
      safeFetchJSON("/api/traces?limit=200"),
      safeFetchJSON(`/api/v2/spans?${query}`),
      safeFetchJSON(`/api/v2/calls?${query}`),
      safeFetchJSON(`/api/v2/object-snapshots?${query}`),
      safeFetchJSON(`/api/v2/object-bindings?${query}`),
      safeFetchJSON(`/api/v2/mutations?${query}`),
      safeFetchJSON(`/api/v2/sessions?${query}`),
      safeFetchJSON(`/api/v2/clients?${query}`),
      renderQuery ? safeFetchJSON(`/api/v2/render?${renderQuery}`) : Promise.resolve({ items: [], nextCursor: "", data: null, error: "" }),
    ]);

  state.traces = tracesResp.data?.traces || [];
  state.spans = normalizeV2Response(spansResp.data);
  state.calls = normalizeV2Response(callsResp.data);
  state.snapshots = normalizeV2Response(snapshotsResp.data);
  state.bindings = normalizeV2Response(bindingsResp.data);
  state.mutations = normalizeV2Response(mutationsResp.data);
  state.sessions = normalizeV2Response(sessionsResp.data);
  state.clients = normalizeV2Response(clientsResp.data);
  state.render = renderResp.data || null;

  renderSummary(
    tracesResp.error ||
      spansResp.error ||
      callsResp.error ||
      snapshotsResp.error ||
      bindingsResp.error ||
      mutationsResp.error ||
      sessionsResp.error ||
      clientsResp.error ||
      renderResp.error,
  );
  renderMutations();
  renderBindings();
  renderCalls();
  renderSessionsAndClients();
  renderTraceList();
}

function buildV2Query() {
  const q = new URLSearchParams();
  q.set("limit", String(v2Limit));
  if (state.options.traceID) {
    q.set("traceID", state.options.traceID);
  }
  if (state.options.includeInternal) {
    q.set("includeInternal", "true");
  }
  return q.toString();
}

function buildRenderQuery() {
  if (!state.options.traceID) {
    return "";
  }
  const q = new URLSearchParams();
  q.set("traceID", state.options.traceID);
  q.set("mode", "global");
  if (state.options.includeInternal) {
    q.set("includeInternal", "true");
  }
  if (state.options.keepRules) {
    q.set("keepRules", "default");
  }
  return q.toString();
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
    els.traceFilter.value = traceID;
    state.options.traceID = traceID;
    await refreshAll();
  } finally {
    els.importCloudBtn.disabled = false;
    els.importCloudBtn.textContent = prevLabel;
  }
}

function renderSummary(errorMsg) {
  els.countSpans.textContent = countLabel(state.spans);
  els.countCalls.textContent = countLabel(state.calls);
  els.countSnapshots.textContent = countLabel(state.snapshots);
  els.countBindings.textContent = countLabel(state.bindings);
  els.countMutations.textContent = countLabel(state.mutations);
  els.countSessions.textContent = countLabel(state.sessions);
  els.countClients.textContent = countLabel(state.clients);
  if (state.render && Array.isArray(state.render.objects)) {
    els.countRenderObjects.textContent = `${state.render.objects.length}`;
  } else if (state.options.traceID) {
    els.countRenderObjects.textContent = "0";
  } else {
    els.countRenderObjects.textContent = "n/a";
  }

  const baseMeta = state.options.traceID
    ? `trace=${state.options.traceID}`
    : `global (all traces)`;
  const renderMeta =
    state.render && state.render.context
      ? `render@${formatRelTime(state.render.context.unixNano, state.render.context.traceStartUnixNano)}`
      : "no render (set trace filter)";

  els.mutationsMeta.textContent = `${baseMeta} | ${cursorLabel(state.mutations)}${errorMsg ? " | warning" : ""}`;
  els.bindingsMeta.textContent = `${baseMeta} | ${cursorLabel(state.bindings)}`;
  els.callsMeta.textContent = `${baseMeta} | ${cursorLabel(state.calls)}`;
  els.sessionsMeta.textContent = `${baseMeta} | ${renderMeta}`;
}

function renderMutations() {
  const items = state.mutations.items || [];
  if (items.length === 0) {
    els.mutationTable.innerHTML = `<div class="trace-empty">No mutation rows.</div>`;
    return;
  }

  const rows = items.slice(0, 160).map((item) => {
    const visibleText = item.visible ? "yes" : "no";
    const at = formatAbsoluteRelative(item.startUnixNano);
    return `
      <tr>
        <td>${escapeHTML(at)}</td>
        <td>${traceTag(item.traceID)}</td>
        <td>${escapeHTML((item.kind || "-").toUpperCase())}</td>
        <td>${escapeHTML(item.bindingID || "-")}</td>
        <td>${escapeHTML(shortDigest(item.causeCallID || "-"))}</td>
        <td>${escapeHTML(visibleText)}</td>
      </tr>
    `;
  });

  els.mutationTable.innerHTML = `
    <table class="v2-table">
      <thead>
        <tr><th>At</th><th>Trace</th><th>Kind</th><th>Binding</th><th>Call</th><th>Visible</th></tr>
      </thead>
      <tbody>${rows.join("")}</tbody>
    </table>
  `;
  wireTraceLinks(els.mutationTable);
}

function renderBindings() {
  const items = state.bindings.items || [];
  if (items.length === 0) {
    els.bindingTable.innerHTML = `<div class="trace-empty">No object bindings.</div>`;
    return;
  }

  const rows = items.slice(0, 160).map((item) => {
    const archived = item.archived ? "archived" : "open";
    const current = item.currentSnapshotID ? shortDigest(item.currentSnapshotID) : "-";
    return `
      <tr>
        <td>${escapeHTML(item.alias || "-")}</td>
        <td>${escapeHTML(item.typeName || "-")}</td>
        <td>${traceTag(item.traceID)}</td>
        <td>${Number((item.snapshotHistory || []).length)}</td>
        <td>${escapeHTML(current)}</td>
        <td>${escapeHTML(archived)}</td>
      </tr>
    `;
  });

  els.bindingTable.innerHTML = `
    <table class="v2-table">
      <thead>
        <tr><th>Alias</th><th>Type</th><th>Trace</th><th>States</th><th>Current</th><th>Status</th></tr>
      </thead>
      <tbody>${rows.join("")}</tbody>
    </table>
  `;
  wireTraceLinks(els.bindingTable);
}

function renderCalls() {
  const items = state.calls.items || [];
  if (items.length === 0) {
    els.callTable.innerHTML = `<div class="trace-empty">No call rows.</div>`;
    return;
  }

  const rows = items.slice(0, 160).map((item) => {
    const op = item.derivedOperation ? item.derivedOperation.toUpperCase() : "CALL";
    return `
      <tr>
        <td>${escapeHTML(formatAbsoluteRelative(item.startUnixNano))}</td>
        <td>${traceTag(item.traceID)}</td>
        <td>${escapeHTML(item.name || "-")}</td>
        <td>${Number(item.callDepth || 0)}</td>
        <td>${item.topLevel ? "yes" : "no"}</td>
        <td>${escapeHTML(op)}</td>
      </tr>
    `;
  });

  els.callTable.innerHTML = `
    <table class="v2-table">
      <thead>
        <tr><th>At</th><th>Trace</th><th>Name</th><th>Depth</th><th>Top</th><th>Derived</th></tr>
      </thead>
      <tbody>${rows.join("")}</tbody>
    </table>
  `;
  wireTraceLinks(els.callTable);
}

function renderSessionsAndClients() {
  const sessionRows = (state.sessions.items || [])
    .slice(0, 60)
    .map((item) => {
      return `
        <tr>
          <td>${escapeHTML(shortDigest(item.id || "-"))}</td>
          <td>${traceTag(item.traceID)}</td>
          <td>${escapeHTML(item.status || (item.open ? "open" : "closed"))}</td>
        </tr>
      `;
    })
    .join("");
  const clientRows = (state.clients.items || [])
    .slice(0, 80)
    .map((item) => {
      const title = item.name || item.serviceName || shortDigest(item.id || "-");
      return `
        <tr>
          <td>${escapeHTML(title)}</td>
          <td>${traceTag(item.traceID)}</td>
          <td>${escapeHTML(shortDigest(item.sessionID || "-"))}</td>
        </tr>
      `;
    })
    .join("");

  els.sessionClientTable.innerHTML = `
    <div class="v2-subtables">
      <table class="v2-table">
        <thead><tr><th>Session</th><th>Trace</th><th>Status</th></tr></thead>
        <tbody>${sessionRows || `<tr><td colspan="3">No sessions.</td></tr>`}</tbody>
      </table>
      <table class="v2-table">
        <thead><tr><th>Client</th><th>Trace</th><th>Session</th></tr></thead>
        <tbody>${clientRows || `<tr><td colspan="3">No clients.</td></tr>`}</tbody>
      </table>
    </div>
  `;
  wireTraceLinks(els.sessionClientTable);
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
      <div class="trace-row">
        <span class="trace-cell trace-cell-id">${traceTag(trace.traceID)}</span>
        <span class="trace-cell trace-cell-created">${escapeHTML(created)}</span>
        <span class="trace-cell trace-cell-spans">${Number(trace.spanCount || 0)}</span>
        <span class="trace-cell trace-cell-status">
          <span class="status-dot ${statusClass}" title="${escapeHTML(statusLabel)}" aria-label="${escapeHTML(statusLabel)}"></span>
          <button class="btn btn-inline" data-open-trace-id="${escapeHTML(trace.traceID)}">Open</button>
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

  wireTraceLinks(els.traceList);
  for (const node of els.traceList.querySelectorAll("[data-open-trace-id]")) {
    node.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      const traceID = node.getAttribute("data-open-trace-id");
      if (!traceID) {
        return;
      }
      window.location.assign(`/traces/${encodeURIComponent(traceID)}`);
    });
  }
}

function wireTraceLinks(root) {
  for (const node of root.querySelectorAll("[data-trace-id]")) {
    node.addEventListener("click", (event) => {
      event.preventDefault();
      const traceID = node.getAttribute("data-trace-id");
      if (!traceID) {
        return;
      }
      els.traceFilter.value = traceID;
      state.options.traceID = traceID;
      refreshAll().catch((err) => renderInfo(String(err)));
    });
  }
}

function traceTag(traceID) {
  if (!traceID) {
    return "-";
  }
  return `<button class="v2-trace-tag" data-trace-id="${escapeHTML(traceID)}">${escapeHTML(shortDigest(traceID))}</button>`;
}

function renderInfo(msg) {
  els.mutationTable.innerHTML = `<div class='trace-empty'>${escapeHTML(msg)}</div>`;
}

function normalizeV2Response(data) {
  if (!data || typeof data !== "object") {
    return { items: [], nextCursor: "" };
  }
  return {
    items: Array.isArray(data.items) ? data.items : [],
    nextCursor: typeof data.nextCursor === "string" ? data.nextCursor : "",
  };
}

function countLabel(v2resp) {
  const count = Number((v2resp?.items || []).length);
  return `${count}${v2resp?.nextCursor ? "+" : ""}`;
}

function cursorLabel(v2resp) {
  return v2resp?.nextCursor ? `page capped (${v2Limit}+)` : `${(v2resp?.items || []).length} rows`;
}

function startLiveRefresh() {
  stopLiveRefresh();
  state.liveTimer = window.setInterval(() => {
    if (document.hidden || state.liveBusy) {
      return;
    }
    state.liveBusy = true;
    refreshAll()
      .catch((err) => {
        console.error("v2 refresh failed", err);
      })
      .finally(() => {
        state.liveBusy = false;
      });
  }, liveRefreshIntervalMs);
}

function stopLiveRefresh() {
  if (state.liveTimer) {
    window.clearInterval(state.liveTimer);
    state.liveTimer = 0;
  }
}

async function safeFetchJSON(url, init) {
  try {
    const data = await fetchJSON(url, init);
    return { data, error: "" };
  } catch (err) {
    return { data: null, error: String(err) };
  }
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

function formatAbsoluteRelative(unixNano) {
  if (!unixNano) {
    return "0 ms";
  }
  const ms = unixNano / 1e6;
  const dt = new Date(ms);
  if (Number.isNaN(dt.getTime())) {
    return "0 ms";
  }
  return `${dt.toLocaleTimeString()} (${formatRelAge(dt)})`;
}

function formatRelAge(dt) {
  const diffSeconds = Math.round((dt.getTime() - Date.now()) / 1000);
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
  return relativeTimeFormatter.format(diffDays, "day");
}

function formatRelTime(unixNano, startUnixNano) {
  if (!unixNano || !startUnixNano) {
    return "0 ms";
  }
  const ms = (unixNano - startUnixNano) / 1e6;
  return `${ms.toFixed(1)} ms`;
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

function shortDigest(v) {
  if (!v) {
    return "-";
  }
  return v.length > 12 ? `${v.slice(0, 12)}...` : v;
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

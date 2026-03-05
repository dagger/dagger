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
  selectedPage: "overview",
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
  renderObjectsMeta: document.getElementById("renderObjectsMeta"),
  mutationTable: document.getElementById("mutationTable"),
  bindingTable: document.getElementById("bindingTable"),
  callTable: document.getElementById("callTable"),
  sessionClientTable: document.getElementById("sessionClientTable"),
  renderObjectTable: document.getElementById("renderObjectTable"),
  traceList: document.getElementById("traceList"),
  overviewScope: document.getElementById("overviewScope"),
  overviewStatus: document.getElementById("overviewStatus"),
  connectInfo: document.getElementById("connectInfo"),
  tabButtons: Array.from(document.querySelectorAll("[data-page-tab]")),
  pageSections: Array.from(document.querySelectorAll("[data-page]")),
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
  bindTabs();

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

function bindTabs() {
  const fromHash = normalizePageName(window.location.hash.replace(/^#/, ""));
  if (fromHash) {
    state.selectedPage = fromHash;
  }
  setActivePage(state.selectedPage, false);

  for (const button of els.tabButtons) {
    button.addEventListener("click", () => {
      const page = normalizePageName(button.getAttribute("data-page-tab"));
      if (!page) {
        return;
      }
      setActivePage(page, true);
    });
  }

  window.addEventListener("hashchange", () => {
    const page = normalizePageName(window.location.hash.replace(/^#/, ""));
    if (!page || page === state.selectedPage) {
      return;
    }
    setActivePage(page, false);
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
  renderOverview();
  renderMutations();
  renderBindings();
  renderCalls();
  renderRenderObjects();
  renderSessionsAndClients();
  renderConnectInfo();
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
  if (els.renderObjectsMeta) {
    els.renderObjectsMeta.textContent = state.options.traceID
      ? `${baseMeta} | ${state.options.keepRules ? "keep-rules on" : "keep-rules off"}`
      : "set trace filter to load render objects";
  }
}

function renderOverview() {
  const scopeRows = [
    { key: "Scope", value: state.options.traceID ? `trace ${state.options.traceID}` : "global (all traces)" },
    { key: "Internal", value: state.options.includeInternal ? "included" : "excluded" },
    { key: "Keep Rules", value: state.options.keepRules ? "enabled" : "disabled" },
    { key: "Page Size", value: `${v2Limit}` },
  ];
  els.overviewScope.innerHTML = scopeRows.map((row) => overviewKV(row.key, row.value)).join("");

  const statusCounts = countTraceStatuses(state.traces);
  const statusRows = [
    { key: "Traces", value: String(state.traces.length) },
    { key: "Ingesting", value: String(statusCounts.ingesting) },
    { key: "Failed", value: String(statusCounts.failed) },
    { key: "Completed", value: String(statusCounts.completed) },
  ];
  els.overviewStatus.innerHTML = statusRows.map((row) => overviewKV(row.key, row.value)).join("");
}

function renderRenderObjects() {
  if (!els.renderObjectTable) {
    return;
  }
  if (!state.options.traceID) {
    els.renderObjectTable.innerHTML = `<div class="trace-empty">Set trace filter to inspect render objects.</div>`;
    return;
  }
  const objects = state.render?.objects || [];
  if (objects.length === 0) {
    els.renderObjectTable.innerHTML = `<div class="trace-empty">No render objects for current filter.</div>`;
    return;
  }

  const rows = objects.slice(0, 200).map((obj) => {
    const current = obj.currentSnapshotID ? shortDigest(obj.currentSnapshotID) : "-";
    return `
      <tr>
        <td>${escapeHTML(obj.alias || "-")}</td>
        <td>${escapeHTML(obj.typeName || "-")}</td>
        <td>${Number(obj.stateCount || 0)}</td>
        <td>${escapeHTML(current)}</td>
        <td>${obj.missingState ? "yes" : "no"}</td>
      </tr>
    `;
  });

  els.renderObjectTable.innerHTML = `
    <table class="v2-table">
      <thead>
        <tr><th>Alias</th><th>Type</th><th>States</th><th>Current</th><th>Missing State</th></tr>
      </thead>
      <tbody>${rows.join("")}</tbody>
    </table>
  `;
}

function renderConnectInfo() {
  if (!els.connectInfo) {
    return;
  }
  const serverOrigin = window.location.origin;
  const otlpEndpoint = `${serverOrigin}/v1/traces`;
  const rows = [
    { key: "OTLP HTTP/protobuf", value: otlpEndpoint },
    { key: "ODAG server", value: serverOrigin },
    { key: "Cloud import", value: "Use trace ID + optional org on this page" },
    { key: "CLI wrapper", value: "ODAG_SERVER=<server> odag run -- dagger ..." },
  ];
  els.connectInfo.innerHTML = rows.map((row) => overviewKV(row.key, row.value)).join("");
}

function overviewKV(key, value) {
  return `
    <div class="v2-kv-row">
      <span class="v2-kv-key">${escapeHTML(key)}</span>
      <span class="v2-kv-value">${escapeHTML(value)}</span>
    </div>
  `;
}

function countTraceStatuses(traces) {
  const out = { ingesting: 0, failed: 0, completed: 0 };
  for (const trace of traces || []) {
    const status = String(trace?.status || "").toLowerCase();
    if (status === "ingesting") {
      out.ingesting++;
      continue;
    }
    if (status === "failed") {
      out.failed++;
      continue;
    }
    if (status === "completed" || status === "succeeded" || status === "success") {
      out.completed++;
    }
  }
  return out;
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
          <a class="btn btn-inline" href="/traces/${encodeURIComponent(trace.traceID)}">Open</a>
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

function normalizePageName(raw) {
  const page = String(raw || "").toLowerCase();
  switch (page) {
    case "overview":
    case "activity":
    case "objects":
    case "sessions":
    case "traces":
    case "import":
      return page;
    default:
      return "";
  }
}

function setActivePage(page, writeHash) {
  const normalized = normalizePageName(page) || "overview";
  state.selectedPage = normalized;

  for (const button of els.tabButtons) {
    const matches = button.getAttribute("data-page-tab") === normalized;
    button.classList.toggle("is-active", matches);
    button.setAttribute("aria-selected", matches ? "true" : "false");
  }
  for (const section of els.pageSections) {
    const matches = section.getAttribute("data-page") === normalized;
    section.classList.toggle("is-active", matches);
  }

  if (writeHash) {
    const targetHash = `#${normalized}`;
    if (window.location.hash !== targetHash) {
      window.location.hash = targetHash;
    }
  }
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

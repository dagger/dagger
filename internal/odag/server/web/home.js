const state = {
  bindings: { items: [], nextCursor: "" },
  spans: { items: [], nextCursor: "" },
  calls: { items: [], nextCursor: "" },
  mutations: { items: [], nextCursor: "" },
  sessions: { items: [], nextCursor: "" },
  clients: { items: [], nextCursor: "" },
  options: {
    traceID: "",
    sessionID: "",
    clientID: "",
    includeInternal: false,
  },
  filters: {
    objects: {
      search: "",
      status: "all",
      sort: "recent",
    },
    events: {
      search: "",
      kind: "all",
      sort: "newest",
    },
  },
  selectedPage: "objects",
  liveTimer: 0,
  liveBusy: false,
};

const relativeTimeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const liveRefreshIntervalMs = 4000;
const v2Limit = 250;

const els = {
  refreshBtn: document.getElementById("refreshBtn"),
  clearScopeBtn: document.getElementById("clearScopeBtn"),
  importCloudBtn: document.getElementById("importCloudBtn"),
  cloudTraceID: document.getElementById("cloudTraceID"),
  cloudOrg: document.getElementById("cloudOrg"),
  traceFilter: document.getElementById("traceFilter"),
  includeInternal: document.getElementById("includeInternal"),
  scopeSummary: document.getElementById("scopeSummary"),
  statusNote: document.getElementById("statusNote"),
  objectSearch: document.getElementById("objectSearch"),
  objectStatus: document.getElementById("objectStatus"),
  objectSort: document.getElementById("objectSort"),
  objectMeta: document.getElementById("objectMeta"),
  objectTable: document.getElementById("objectTable"),
  eventSearch: document.getElementById("eventSearch"),
  eventKind: document.getElementById("eventKind"),
  eventSort: document.getElementById("eventSort"),
  eventMeta: document.getElementById("eventMeta"),
  eventTable: document.getElementById("eventTable"),
  tabButtons: Array.from(document.querySelectorAll("[data-page-tab]")),
  pageSections: Array.from(document.querySelectorAll("[data-page]")),
};

init().catch((err) => {
  renderStatus(String(err));
  renderEmpty(els.objectTable, "Initialization failed.");
  renderEmpty(els.eventTable, "Initialization failed.");
});

async function init() {
  bindEvents();
  await refreshAll();
  startLiveRefresh();
}

function bindEvents() {
  bindTabs();

  els.refreshBtn.addEventListener("click", () => {
    refreshAll().catch((err) => renderStatus(String(err)));
  });

  els.clearScopeBtn.addEventListener("click", () => {
    clearScope();
    refreshAll().catch((err) => renderStatus(String(err)));
  });

  els.importCloudBtn.addEventListener("click", () => {
    importTraceFromCloud().catch((err) => renderStatus(String(err)));
  });

  els.traceFilter.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") {
      return;
    }
    state.options.traceID = (els.traceFilter.value || "").trim();
    state.options.sessionID = "";
    state.options.clientID = "";
    refreshAll().catch((err) => renderStatus(String(err)));
  });

  els.includeInternal.addEventListener("change", () => {
    state.options.includeInternal = Boolean(els.includeInternal.checked);
    refreshAll().catch((err) => renderStatus(String(err)));
  });

  els.objectSearch.addEventListener("input", () => {
    state.filters.objects.search = els.objectSearch.value || "";
    renderObjects();
  });
  els.objectStatus.addEventListener("change", () => {
    state.filters.objects.status = els.objectStatus.value || "all";
    renderObjects();
  });
  els.objectSort.addEventListener("change", () => {
    state.filters.objects.sort = els.objectSort.value || "recent";
    renderObjects();
  });

  els.eventSearch.addEventListener("input", () => {
    state.filters.events.search = els.eventSearch.value || "";
    renderEvents();
  });
  els.eventKind.addEventListener("change", () => {
    state.filters.events.kind = els.eventKind.value || "all";
    renderEvents();
  });
  els.eventSort.addEventListener("change", () => {
    state.filters.events.sort = els.eventSort.value || "newest";
    renderEvents();
  });

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden && !state.liveBusy) {
      state.liveBusy = true;
      refreshAll()
        .catch((err) => renderStatus(String(err)))
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

  const query = buildV2Query();
  const [bindingsResp, spansResp, callsResp, mutationsResp, sessionsResp, clientsResp] = await Promise.all([
    safeFetchJSON(`/api/v2/object-bindings?${query}`),
    safeFetchJSON(`/api/v2/spans?${query}`),
    safeFetchJSON(`/api/v2/calls?${query}`),
    safeFetchJSON(`/api/v2/mutations?${query}`),
    safeFetchJSON(`/api/v2/sessions?${query}`),
    safeFetchJSON(`/api/v2/clients?${query}`),
  ]);

  state.bindings = normalizeV2Response(bindingsResp.data);
  state.spans = normalizeV2Response(spansResp.data);
  state.calls = normalizeV2Response(callsResp.data);
  state.mutations = normalizeV2Response(mutationsResp.data);
  state.sessions = normalizeV2Response(sessionsResp.data);
  state.clients = normalizeV2Response(clientsResp.data);

  const errorMsg =
    bindingsResp.error ||
    spansResp.error ||
    callsResp.error ||
    mutationsResp.error ||
    sessionsResp.error ||
    clientsResp.error;

  renderStatus(errorMsg);
  renderScopeSummary();
  renderObjects();
  renderEvents();
}

function buildV2Query() {
  const q = new URLSearchParams();
  q.set("limit", String(v2Limit));
  if (state.options.traceID) {
    q.set("traceID", state.options.traceID);
  }
  if (state.options.sessionID) {
    q.set("sessionID", state.options.sessionID);
  }
  if (state.options.clientID) {
    q.set("clientID", state.options.clientID);
  }
  if (state.options.includeInternal) {
    q.set("includeInternal", "true");
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
    clearScope();
    els.traceFilter.value = traceID;
    state.options.traceID = traceID;
    await refreshAll();
  } finally {
    els.importCloudBtn.disabled = false;
    els.importCloudBtn.textContent = prevLabel;
  }
}

function renderStatus(errorMsg) {
  const bindingCount = responseCountLabel(state.bindings);
  const eventCount = eventCountLabel();
  const scopeText = describeScope();
  const base = `${bindingCount} objects, ${eventCount} events, scope: ${scopeText}`;
  els.statusNote.textContent = errorMsg ? `${base}. Warning: ${errorMsg}` : `${base}.`;
}

function renderScopeSummary() {
  const parts = [];

  if (state.options.traceID) {
    parts.push(scopeSummaryItem("Trace", traceTag(state.options.traceID)));
  }
  if (state.options.sessionID) {
    parts.push(scopeSummaryItem("Session", sessionTag(state.options.sessionID, state.options.traceID)));
  }
  if (state.options.clientID) {
    parts.push(scopeSummaryItem("Client", clientTag(state.options.clientID, state.options.traceID, state.options.sessionID)));
  }
  if (parts.length === 0) {
    parts.push(`<span class="data-note">Showing data across all loaded traces.</span>`);
  }

  parts.push(
    `<span class="data-note">${
      state.options.includeInternal ? "Internal spans/events are included." : "Internal spans/events are hidden."
    }</span>`,
  );
  parts.push(
    `<span class="data-note">Run <code>ODAG_SERVER=${escapeHTML(window.location.origin)} odag run -- dagger ...</code> to send more data here.</span>`,
  );

  els.scopeSummary.innerHTML = parts.join("");
  wireScopeLinks(els.scopeSummary);
}

function renderObjects() {
  const allRows = buildObjectRows();
  const search = (state.filters.objects.search || "").trim().toLowerCase();
  const status = state.filters.objects.status || "all";
  const sort = state.filters.objects.sort || "recent";

  let rows = allRows.filter((row) => {
    if (status === "live" && row.archived) {
      return false;
    }
    if (status === "archived" && !row.archived) {
      return false;
    }
    if (!search) {
      return true;
    }
    return row.searchText.includes(search);
  });

  rows.sort((a, b) => compareObjectRows(a, b, sort));
  els.objectMeta.textContent = `${rows.length} shown of ${allRows.length}${state.bindings.nextCursor ? "+" : ""}`;

  if (rows.length === 0) {
    renderEmpty(els.objectTable, "No objects match the current filters.");
    return;
  }

  const tableRows = rows.map((row) => {
    return `
      <tr>
        <td>
          <div class="data-primary">${escapeHTML(row.alias || "-")}</div>
          <div class="data-secondary data-mono">${escapeHTML(shortDigest(row.bindingID || "-"))}</div>
        </td>
        <td>${escapeHTML(row.typeName || "-")}</td>
        <td>${traceTag(row.traceID)}</td>
        <td>${row.sessionHTML}</td>
        <td>${row.clientHTML}</td>
        <td>${row.stateCount}</td>
        <td class="data-mono">${escapeHTML(shortDigest(row.currentDagqlID || "-"))}</td>
        <td>${statusPill(row.archived ? "archived" : "live")}</td>
        <td>${escapeHTML(formatAbsoluteRelative(row.lastSeenUnixNano))}</td>
      </tr>
    `;
  });

  els.objectTable.innerHTML = `
    <table class="data-table">
      <thead>
        <tr>
          <th>Object</th>
          <th>Type</th>
          <th>Trace</th>
          <th>Session</th>
          <th>Client</th>
          <th>States</th>
          <th>Current</th>
          <th>Status</th>
          <th>Updated</th>
        </tr>
      </thead>
      <tbody>${tableRows.join("")}</tbody>
    </table>
  `;
  wireScopeLinks(els.objectTable);
}

function buildObjectRows() {
  const clientsByID = new Map((state.clients.items || []).map((client) => [client.id, client]));

  return (state.bindings.items || []).map((item) => {
    const clientIDs = Array.isArray(item.clientIDs) ? item.clientIDs.filter(Boolean) : [];
    const clientNames = clientIDs.map((clientID) => clientTitle(clientsByID.get(clientID), clientID));
    const sessionHTML = item.sessionID ? sessionTag(item.sessionID, item.traceID) : `<span class="data-placeholder">-</span>`;
    const clientHTML = renderClientList(item.traceID, item.sessionID, clientIDs, clientNames);
    const stateCount = Array.isArray(item.dagqlHistory) ? item.dagqlHistory.length : 0;

    return {
      alias: item.alias || "",
      archived: Boolean(item.archived),
      bindingID: item.bindingID || "",
      clientHTML,
      clientIDs,
      clientNames,
      currentDagqlID: item.currentDagqlID || "",
      lastSeenUnixNano: Number(item.lastSeenUnixNano || 0),
      searchText: [
        item.alias,
        item.typeName,
        item.traceID,
        item.sessionID,
        item.currentDagqlID,
        ...clientIDs,
        ...clientNames,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase(),
      sessionHTML,
      sessionID: item.sessionID || "",
      stateCount,
      traceID: item.traceID || "",
      typeName: item.typeName || "",
    };
  });
}

function compareObjectRows(a, b, sort) {
  switch (sort) {
    case "alias":
      return compareString(a.alias, b.alias) || compareString(a.bindingID, b.bindingID);
    case "type":
      return compareString(a.typeName, b.typeName) || compareString(a.alias, b.alias);
    case "states":
      return compareNumberDesc(a.stateCount, b.stateCount) || compareString(a.alias, b.alias);
    case "trace":
      return compareString(a.traceID, b.traceID) || compareString(a.alias, b.alias);
    case "recent":
    default:
      return compareNumberDesc(a.lastSeenUnixNano, b.lastSeenUnixNano) || compareString(a.alias, b.alias);
  }
}

function renderEvents() {
  const allRows = buildEventRows();
  const search = (state.filters.events.search || "").trim().toLowerCase();
  const kind = state.filters.events.kind || "all";
  const sort = state.filters.events.sort || "newest";

  let rows = allRows.filter((row) => {
    if (kind !== "all" && row.kind !== kind) {
      return false;
    }
    if (!search) {
      return true;
    }
    return row.searchText.includes(search);
  });

  rows.sort((a, b) => compareEventRows(a, b, sort));
  els.eventMeta.textContent = `${rows.length} shown of ${allRows.length}${eventHasMorePages() ? "+" : ""}`;

  if (rows.length === 0) {
    renderEmpty(els.eventTable, "No events match the current filters.");
    return;
  }

  const tableRows = rows.map((row) => {
    return `
      <tr>
        <td>${escapeHTML(formatAbsoluteRelative(row.atUnixNano))}</td>
        <td>${kindPill(row.kind)}</td>
        <td>
          <div class="data-primary">${escapeHTML(row.name)}</div>
          <div class="data-secondary">${escapeHTML(row.subtitle)}</div>
        </td>
        <td>${traceTag(row.traceID)}</td>
        <td>${row.sessionHTML}</td>
        <td>${row.clientHTML}</td>
        <td class="data-mono">${escapeHTML(row.subject)}</td>
        <td>${escapeHTML(row.details)}</td>
      </tr>
    `;
  });

  els.eventTable.innerHTML = `
    <table class="data-table">
      <thead>
        <tr>
          <th>At</th>
          <th>Kind</th>
          <th>Name</th>
          <th>Trace</th>
          <th>Session</th>
          <th>Client</th>
          <th>Subject</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>${tableRows.join("")}</tbody>
    </table>
  `;
  wireScopeLinks(els.eventTable);
}

function buildEventRows() {
  const clientsByID = new Map((state.clients.items || []).map((client) => [client.id, client]));
  const rows = [];

  for (const item of state.calls.items || []) {
    rows.push({
      atUnixNano: Number(item.startUnixNano || 0),
      clientHTML: item.clientID
        ? clientTag(item.clientID, item.traceID, item.sessionID, clientTitle(clientsByID.get(item.clientID), item.clientID))
        : `<span class="data-placeholder">-</span>`,
      details: [item.derivedOperation || "call", item.topLevel ? "top-level" : `depth ${Number(item.callDepth || 0)}`]
        .filter(Boolean)
        .join(" | "),
      kind: "call",
      name: item.name || "Call",
      searchText: [
        "call",
        item.name,
        item.traceID,
        item.sessionID,
        item.clientID,
        item.outputDagqlID,
        item.receiverDagqlID,
        item.derivedOperation,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase(),
      sessionHTML: item.sessionID ? sessionTag(item.sessionID, item.traceID) : `<span class="data-placeholder">-</span>`,
      subject: shortDigest(item.outputDagqlID || item.receiverDagqlID || item.spanID || "-"),
      subtitle: item.returnType || "",
      traceID: item.traceID || "",
    });
  }

  for (const item of state.mutations.items || []) {
    rows.push({
      atUnixNano: Number(item.startUnixNano || 0),
      clientHTML: item.clientID
        ? clientTag(item.clientID, item.traceID, item.sessionID, clientTitle(clientsByID.get(item.clientID), item.clientID))
        : `<span class="data-placeholder">-</span>`,
      details: [`call ${shortDigest(item.causeCallID || "-")}`, item.visible ? "visible" : "hidden"].join(" | "),
      kind: "mutation",
      name: item.name || (item.kind ? `${String(item.kind).toUpperCase()} mutation` : "Mutation"),
      searchText: [
        "mutation",
        item.name,
        item.kind,
        item.bindingID,
        item.traceID,
        item.sessionID,
        item.clientID,
        item.causeCallID,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase(),
      sessionHTML: item.sessionID ? sessionTag(item.sessionID, item.traceID) : `<span class="data-placeholder">-</span>`,
      subject: item.bindingID || "-",
      subtitle: item.kind || "",
      traceID: item.traceID || "",
    });
  }

  for (const item of state.spans.items || []) {
    rows.push({
      atUnixNano: Number(item.startUnixNano || 0),
      clientHTML: item.clientID
        ? clientTag(item.clientID, item.traceID, item.sessionID, clientTitle(clientsByID.get(item.clientID), item.clientID))
        : `<span class="data-placeholder">-</span>`,
      details: [item.statusCode || "", item.internal ? "internal" : ""].filter(Boolean).join(" | "),
      kind: "span",
      name: item.name || "Span",
      searchText: [
        "span",
        item.name,
        item.traceID,
        item.sessionID,
        item.clientID,
        item.spanID,
        item.statusCode,
        item.statusMessage,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase(),
      sessionHTML: item.sessionID ? sessionTag(item.sessionID, item.traceID) : `<span class="data-placeholder">-</span>`,
      subject: shortDigest(item.spanID || "-"),
      subtitle: item.statusMessage || "",
      traceID: item.traceID || "",
    });
  }

  return rows;
}

function compareEventRows(a, b, sort) {
  switch (sort) {
    case "oldest":
      return compareNumberAsc(a.atUnixNano, b.atUnixNano) || compareString(a.name, b.name);
    case "name":
      return compareString(a.name, b.name) || compareNumberDesc(a.atUnixNano, b.atUnixNano);
    case "kind":
      return compareString(a.kind, b.kind) || compareNumberDesc(a.atUnixNano, b.atUnixNano);
    case "trace":
      return compareString(a.traceID, b.traceID) || compareNumberDesc(a.atUnixNano, b.atUnixNano);
    case "newest":
    default:
      return compareNumberDesc(a.atUnixNano, b.atUnixNano) || compareString(a.name, b.name);
  }
}

function renderEmpty(root, msg) {
  root.innerHTML = `<div class="data-empty">${escapeHTML(msg)}</div>`;
}

function wireScopeLinks(root) {
  for (const node of root.querySelectorAll("[data-scope-kind]")) {
    node.addEventListener("click", (event) => {
      event.preventDefault();
      const kind = node.getAttribute("data-scope-kind");
      if (kind === "trace") {
        state.options.traceID = node.getAttribute("data-trace-id") || "";
        state.options.sessionID = "";
        state.options.clientID = "";
      } else if (kind === "session") {
        state.options.traceID = node.getAttribute("data-trace-id") || "";
        state.options.sessionID = node.getAttribute("data-scope-session-id") || "";
        state.options.clientID = "";
      } else if (kind === "client") {
        state.options.traceID = node.getAttribute("data-trace-id") || "";
        state.options.sessionID = node.getAttribute("data-scope-session-id") || "";
        state.options.clientID = node.getAttribute("data-scope-client-id") || "";
      } else {
        return;
      }
      els.traceFilter.value = state.options.traceID;
      refreshAll().catch((err) => renderStatus(String(err)));
    });
  }
}

function traceTag(traceID) {
  if (!traceID) {
    return `<span class="data-placeholder">-</span>`;
  }
  return `<button class="scope-chip" data-scope-kind="trace" data-trace-id="${escapeHTML(traceID)}">${escapeHTML(shortDigest(traceID))}</button>`;
}

function sessionTag(sessionID, traceID) {
  if (!sessionID) {
    return `<span class="data-placeholder">-</span>`;
  }
  return `<button class="scope-chip" data-scope-kind="session" data-scope-session-id="${escapeHTML(sessionID)}" data-trace-id="${escapeHTML(traceID || "")}">${escapeHTML(shortDigest(sessionID))}</button>`;
}

function clientTag(clientID, traceID, sessionID, label) {
  if (!clientID) {
    return `<span class="data-placeholder">-</span>`;
  }
  const text = label || shortDigest(clientID);
  return `<button class="scope-chip" data-scope-kind="client" data-scope-client-id="${escapeHTML(clientID)}" data-scope-session-id="${escapeHTML(sessionID || "")}" data-trace-id="${escapeHTML(traceID || "")}">${escapeHTML(text)}</button>`;
}

function renderClientList(traceID, sessionID, clientIDs, clientNames) {
  if (!clientIDs.length) {
    return `<span class="data-placeholder">-</span>`;
  }
  const tags = [];
  const visibleCount = Math.min(clientIDs.length, 2);
  for (let i = 0; i < visibleCount; i++) {
    tags.push(clientTag(clientIDs[i], traceID, sessionID, clientNames[i]));
  }
  if (clientIDs.length > visibleCount) {
    tags.push(`<span class="data-note">+${clientIDs.length - visibleCount}</span>`);
  }
  return `<div class="data-chip-group">${tags.join("")}</div>`;
}

function scopeSummaryItem(label, valueHTML) {
  return `
    <span class="data-scope-item">
      <span class="data-scope-label">${escapeHTML(label)}</span>
      ${valueHTML}
    </span>
  `;
}

function responseCountLabel(resp) {
  return `${Number((resp.items || []).length)}${resp.nextCursor ? "+" : ""}`;
}

function eventCountLabel() {
  const total =
    Number((state.spans.items || []).length) +
    Number((state.calls.items || []).length) +
    Number((state.mutations.items || []).length);
  return `${total}${eventHasMorePages() ? "+" : ""}`;
}

function eventHasMorePages() {
  return Boolean(state.spans.nextCursor || state.calls.nextCursor || state.mutations.nextCursor);
}

function describeScope() {
  if (state.options.clientID) {
    return `client ${shortDigest(state.options.clientID)}`;
  }
  if (state.options.sessionID) {
    return `session ${shortDigest(state.options.sessionID)}`;
  }
  if (state.options.traceID) {
    return `trace ${shortDigest(state.options.traceID)}`;
  }
  return "all traces";
}

function clearScope() {
  state.options.traceID = "";
  state.options.sessionID = "";
  state.options.clientID = "";
  els.traceFilter.value = "";
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
    case "objects":
    case "events":
      return page;
    default:
      return "";
  }
}

function setActivePage(page, writeHash) {
  const normalized = normalizePageName(page) || "objects";
  state.selectedPage = normalized;

  for (const button of els.tabButtons) {
    const matches = button.getAttribute("data-page-tab") === normalized;
    button.classList.toggle("is-active", matches);
    button.setAttribute("aria-selected", matches ? "true" : "false");
  }
  for (const section of els.pageSections) {
    section.classList.toggle("is-active", section.getAttribute("data-page") === normalized);
  }

  if (writeHash) {
    const targetHash = `#${normalized}`;
    if (window.location.hash !== targetHash) {
      window.location.hash = targetHash;
    }
  }
}

function startLiveRefresh() {
  stopLiveRefresh();
  state.liveTimer = window.setInterval(() => {
    if (document.hidden || state.liveBusy) {
      return;
    }
    state.liveBusy = true;
    refreshAll()
      .catch((err) => renderStatus(String(err)))
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

function clientTitle(client, clientID) {
  if (client?.name) {
    return client.name;
  }
  if (client?.serviceName) {
    return client.serviceName;
  }
  return shortDigest(clientID || "-");
}

function kindPill(kind) {
  return `<span class="data-kind-pill is-${escapeHTML(kind)}">${escapeHTML(kind)}</span>`;
}

function statusPill(label) {
  const normalized = String(label || "").toLowerCase();
  return `<span class="data-status-pill is-${escapeHTML(normalized)}">${escapeHTML(label)}</span>`;
}

function compareString(a, b) {
  return String(a || "").localeCompare(String(b || ""), undefined, { sensitivity: "base" });
}

function compareNumberAsc(a, b) {
  return Number(a || 0) - Number(b || 0);
}

function compareNumberDesc(a, b) {
  return Number(b || 0) - Number(a || 0);
}

function formatAbsoluteRelative(unixNano) {
  if (!unixNano) {
    return "-";
  }
  const ms = unixNano / 1e6;
  const dt = new Date(ms);
  if (Number.isNaN(dt.getTime())) {
    return "-";
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

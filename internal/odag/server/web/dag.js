const state = {
  render: null,
  selectedObjectID: "",
  requestToken: 0,
  liveBusy: false,
  liveTimer: 0,
  fallbackNote: "",
  options: {
    traceID: "",
    sessionID: "",
    clientID: "",
    mode: "global",
    focusObjectID: "",
    scopeCallID: "",
    dependencyHops: 1,
    includeInternal: false,
    keepRules: true,
  },
};

const liveRefreshIntervalMs = 4000;

const els = {
  backBtn: document.getElementById("backBtn"),
  openTraceLink: document.getElementById("openTraceLink"),
  refreshBtn: document.getElementById("refreshBtn"),
  dagSubtitle: document.getElementById("dagSubtitle"),
  modeSelect: document.getElementById("modeSelect"),
  dependencyHops: document.getElementById("dependencyHops"),
  includeInternal: document.getElementById("includeInternal"),
  keepRules: document.getElementById("keepRules"),
  clearFocusBtn: document.getElementById("clearFocusBtn"),
  clearScopeCallBtn: document.getElementById("clearScopeCallBtn"),
  scopeSummary: document.getElementById("scopeSummary"),
  statusNote: document.getElementById("statusNote"),
  statGrid: document.getElementById("statGrid"),
  objectListMeta: document.getElementById("objectListMeta"),
  objectList: document.getElementById("objectList"),
  inspector: document.getElementById("inspector"),
  dagCanvas: document.getElementById("dagCanvas"),
  dagEmpty: document.getElementById("dagEmpty"),
};

init().catch((err) => {
  showError(err);
  renderNoScope("Initialization failed.");
});

async function init() {
  bindEvents();
  readOptionsFromURL();
  syncControls();
  if (!hasScope()) {
    renderNoScope("Open this page from an object row or a call event row.");
    return;
  }
  await refreshDag();
  startLiveRefresh();
}

function bindEvents() {
  if (els.backBtn) {
    els.backBtn.addEventListener("click", () => {
      const hasHistory = window.history.length > 1;
      const sameOriginReferrer =
        typeof document.referrer === "string" &&
        document.referrer.startsWith(window.location.origin);
      if (hasHistory && sameOriginReferrer) {
        window.history.back();
        return;
      }
      window.location.assign("/");
    });
  }

  els.refreshBtn.addEventListener("click", () => {
    refreshDag().catch((err) => showError(err));
  });

  els.modeSelect.addEventListener("change", () => {
    state.options.mode = normalizeMode(els.modeSelect.value) || defaultModeFromOptions();
    if (state.options.mode === "object" && !state.options.focusObjectID && state.selectedObjectID) {
      state.options.focusObjectID = state.selectedObjectID;
    }
    refreshDag().catch((err) => showError(err));
  });

  els.dependencyHops.addEventListener("change", () => {
    state.options.dependencyHops = clampDependencyHops(els.dependencyHops.value);
    refreshDag().catch((err) => showError(err));
  });

  els.includeInternal.addEventListener("change", () => {
    state.options.includeInternal = Boolean(els.includeInternal.checked);
    refreshDag().catch((err) => showError(err));
  });

  els.keepRules.addEventListener("change", () => {
    state.options.keepRules = Boolean(els.keepRules.checked);
    refreshDag().catch((err) => showError(err));
  });

  els.clearFocusBtn.addEventListener("click", () => {
    if (!state.options.focusObjectID) {
      return;
    }
    state.options.focusObjectID = "";
    state.options.mode = defaultModeFromOptions();
    refreshDag().catch((err) => showError(err));
  });

  els.clearScopeCallBtn.addEventListener("click", () => {
    if (!state.options.scopeCallID) {
      return;
    }
    state.options.scopeCallID = "";
    state.options.mode = defaultModeFromOptions();
    refreshDag().catch((err) => showError(err));
  });

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden && !state.liveBusy) {
      state.liveBusy = true;
      refreshDag()
        .catch((err) => showError(err))
        .finally(() => {
          state.liveBusy = false;
        });
    }
  });

  window.addEventListener("beforeunload", () => {
    stopLiveRefresh();
  });
}

function readOptionsFromURL() {
  const params = new URLSearchParams(window.location.search);
  state.options.traceID = stringOrEmpty(params.get("traceID"));
  state.options.sessionID = stringOrEmpty(params.get("sessionID"));
  state.options.clientID = stringOrEmpty(params.get("clientID"));
  state.options.focusObjectID = stringOrEmpty(params.get("focusObjectID"));
  state.options.scopeCallID = stringOrEmpty(params.get("scopeCallID"));
  state.options.dependencyHops = clampDependencyHops(params.get("dependencyHops"));
  state.options.includeInternal = parseBoolish(params.get("includeInternal"));
  if (params.has("keepRules")) {
    state.options.keepRules = parseKeepRules(params.get("keepRules"));
  } else {
    state.options.keepRules = !(state.options.focusObjectID || state.options.scopeCallID);
  }
  state.options.mode = normalizeMode(params.get("mode")) || defaultModeFromOptions();
}

function syncControls() {
  els.modeSelect.value = state.options.mode;
  els.dependencyHops.value = String(state.options.dependencyHops);
  els.includeInternal.checked = Boolean(state.options.includeInternal);
  els.keepRules.checked = Boolean(state.options.keepRules);
  els.clearFocusBtn.disabled = !state.options.focusObjectID;
  els.clearScopeCallBtn.disabled = !state.options.scopeCallID;
}

function hasScope() {
  return Boolean(state.options.traceID || state.options.sessionID || state.options.clientID);
}

async function refreshDag() {
  if (!hasScope()) {
    renderNoScope("Open this page from an object row or a call event row.");
    return;
  }

  syncControls();
  writeURL();
  state.fallbackNote = "";
  renderStatus("Loading DAG...");

  const token = ++state.requestToken;
  let render = await fetchRender();
  if (
    (render?.objects || []).length === 0 &&
    state.options.keepRules &&
    (state.options.focusObjectID || state.options.scopeCallID)
  ) {
    state.options.keepRules = false;
    state.fallbackNote = "Keep rules hid the selected drill-in, so pruning was disabled for this view";
    syncControls();
    writeURL();
    render = await fetchRender();
  }
  if (token !== state.requestToken) {
    return;
  }

  state.render = render;
  const objects = Array.isArray(render?.objects) ? render.objects : [];
  if (!objects.some((obj) => obj.objectID === state.selectedObjectID)) {
    const preferred = objects.some((obj) => obj.objectID === state.options.focusObjectID)
      ? state.options.focusObjectID
      : objects[0]?.objectID || "";
    state.selectedObjectID = preferred;
  }

  renderAll();
}

function renderAll() {
  syncControls();
  renderHeader();
  renderScopeSummary();
  renderStats();
  renderObjectList();
  renderInspector();
  renderGraph();
  renderStatus("");
}

function renderHeader() {
  const traceTitle = state.render?.context?.traceTitle || "";
  const titleToken = traceTitle || shortDigest(state.render?.context?.traceID || state.options.traceID || "DAG");
  document.title = `ODAG DAG ${titleToken}`;

  const objects = Array.isArray(state.render?.objects) ? state.render.objects.length : 0;
  const edges = fieldEdges().length;
  const calls = Array.isArray(state.render?.calls) ? state.render.calls.length : 0;
  const parts = [];
  if (traceTitle) {
    parts.push(traceTitle);
  }
  parts.push(`${objects} objects`);
  parts.push(`${edges} dependencies`);
  parts.push(`${calls} calls in lens`);
  parts.push(`mode ${state.options.mode}`);
  els.dagSubtitle.textContent = `${parts.join(" | ")}.`;

  const traceID = state.render?.context?.traceID || state.options.traceID;
  if (traceID) {
    els.openTraceLink.href = `/traces/${encodeURIComponent(traceID)}`;
    els.openTraceLink.removeAttribute("aria-disabled");
  } else {
    els.openTraceLink.href = "/";
    els.openTraceLink.setAttribute("aria-disabled", "true");
  }
}

function renderScopeSummary() {
  if (!hasScope()) {
    els.scopeSummary.innerHTML = `<span class="data-note">Choose a trace, session, or client scope to render a DAG.</span>`;
    return;
  }

  const traceID = state.render?.context?.traceID || state.options.traceID;
  const sessionID = state.render?.context?.sessionID || state.options.sessionID;
  const clientID = state.render?.context?.clientID || state.options.clientID;
  const parts = [];

  if (traceID) {
    parts.push(scopeSummaryItem("Trace", staticChip(shortDigest(traceID))));
  }
  if (sessionID) {
    parts.push(scopeSummaryItem("Session", staticChip(shortDigest(sessionID))));
  }
  if (clientID) {
    parts.push(scopeSummaryItem("Client", staticChip(shortDigest(clientID))));
  }
  if (state.options.scopeCallID) {
    parts.push(scopeSummaryItem("Call scope", staticChip(shortDigest(state.options.scopeCallID))));
  }
  if (state.options.focusObjectID) {
    parts.push(scopeSummaryItem("Focus", staticChip(shortDigest(state.options.focusObjectID))));
  }

  parts.push(`<span class="data-note">Field-reference edges only. Containment and provenance stay out of the default graph.</span>`);
  els.scopeSummary.innerHTML = parts.join("");
}

function renderStats() {
  const objects = Array.isArray(state.render?.objects) ? state.render.objects : [];
  const edges = fieldEdges();
  const calls = Array.isArray(state.render?.calls) ? state.render.calls : [];
  const warnings = Array.isArray(state.render?.warnings) ? state.render.warnings : [];

  const cards = [
    statCard("Visible objects", String(objects.length)),
    statCard("Dependency edges", String(edges.length)),
    statCard("Calls in lens", String(calls.length)),
    statCard("Warnings", String(warnings.length)),
  ];
  els.statGrid.innerHTML = cards.join("");
}

function renderStatus(prefix) {
  if (prefix) {
    els.statusNote.textContent = prefix;
    return;
  }

  const objects = Array.isArray(state.render?.objects) ? state.render.objects.length : 0;
  const edges = fieldEdges().length;
  const warnings = Array.isArray(state.render?.warnings) ? state.render.warnings.length : 0;
  const warningText = warnings > 0 ? ` | ${warnings} warning(s)` : "";
  const fallbackText = state.fallbackNote ? ` | ${state.fallbackNote}` : "";
  els.statusNote.textContent = `${objects} object(s), ${edges} field reference edge(s), dependency hops ${state.options.dependencyHops}${warningText}${fallbackText}.`;
}

function renderObjectList() {
  const objects = sortedObjects(state.render?.objects || []);
  els.objectListMeta.textContent = `${objects.length}`;

  if (objects.length === 0) {
    els.objectList.innerHTML = `<div class="data-empty">No objects are visible in this lens.</div>`;
    return;
  }

  els.objectList.innerHTML = objects
    .map((obj) => {
      const selected = obj.objectID === state.selectedObjectID ? " is-selected" : "";
      const focused = obj.objectID === state.options.focusObjectID ? "Focused" : "Focus";
      const title = escapeHTML(obj.alias || obj.typeName || obj.objectID);
      const meta = `${escapeHTML(obj.typeName || "Object")} | ${obj.stateCount || 0} state(s)`;
      return `
        <div class="dag-object-row${selected}">
          <button class="dag-object-link" data-select-object="${escapeHTML(obj.objectID)}" type="button">
            <span class="dag-object-title">${title}</span>
            <span class="dag-object-meta">${meta}</span>
          </button>
          <button class="dag-mini-btn" data-focus-object="${escapeHTML(obj.objectID)}" type="button">${escapeHTML(focused)}</button>
        </div>
      `;
    })
    .join("");

  wireObjectSelection(els.objectList);
}

function renderInspector() {
  const object = selectedObject();
  if (!object) {
    els.inspector.innerHTML = `<div class="data-empty">Select an object to inspect its fields and dependencies.</div>`;
    return;
  }

  const fields = latestStateFields(object.currentState);
  const callByID = new Map((state.render?.calls || []).map((call) => [call.callID, call]));
  const objectByID = new Map((state.render?.objects || []).map((item) => [item.objectID, item]));
  const incoming = fieldEdges().filter((edge) => edge.toID === object.objectID);
  const outgoing = fieldEdges().filter((edge) => edge.fromID === object.objectID);
  const activity = (object.activityCallIDs || [])
    .map((callID) => callByID.get(callID))
    .filter(Boolean);

  const actionLabel = object.objectID === state.options.focusObjectID ? "Clear focus" : "Focus graph here";
  const actionAttr = object.objectID === state.options.focusObjectID ? "data-clear-focus" : `data-focus-object="${escapeHTML(object.objectID)}"`;

  const header = `
    <div class="dag-inspector-head">
      <div>
        <h3>${escapeHTML(object.alias || object.typeName || object.objectID)}</h3>
        <p>${escapeHTML(object.typeName || "Object")} | ${escapeHTML(shortDigest(object.currentDagqlID || object.objectID))}</p>
      </div>
      <button class="btn btn-secondary dag-inspector-btn" ${actionAttr} type="button">${escapeHTML(actionLabel)}</button>
    </div>
  `;

  const facts = `
    <dl class="dag-fact-list">
      <div><dt>Object ID</dt><dd class="data-mono">${escapeHTML(object.objectID)}</dd></div>
      <div><dt>Binding</dt><dd class="data-mono">${escapeHTML(object.bindingID || "-")}</dd></div>
      <div><dt>DAGQL ID</dt><dd class="data-mono">${escapeHTML(object.currentDagqlID || "-")}</dd></div>
      <div><dt>States</dt><dd>${escapeHTML(String(object.stateCount || 0))}</dd></div>
      <div><dt>Activity calls</dt><dd>${escapeHTML(String((object.activityCallIDs || []).length))}</dd></div>
      <div><dt>Top referenced</dt><dd>${object.referencedByTop ? "yes" : "no"}</dd></div>
    </dl>
  `;

  const fieldSection =
    fields.length > 0
      ? `
        <div class="dag-inspector-section">
          <h4>Current fields</h4>
          <div class="dag-field-list">
            ${fields
              .map(
                (field) => `
                  <div class="dag-field-row">
                    <span class="dag-field-name">${escapeHTML(field.name)}</span>
                    <span class="dag-field-value">${escapeHTML(field.value)}</span>
                  </div>
                `,
              )
              .join("")}
          </div>
        </div>
      `
      : `
        <div class="dag-inspector-section">
          <h4>Current fields</h4>
          <div class="data-empty">No structured fields available for this object.</div>
        </div>
      `;

  const dependencySection = `
    <div class="dag-inspector-section">
      <h4>Depends on</h4>
      ${renderDependencyList(incoming, objectByID, "fromID")}
    </div>
    <div class="dag-inspector-section">
      <h4>Used by</h4>
      ${renderDependencyList(outgoing, objectByID, "toID")}
    </div>
  `;

  const activitySection = `
    <div class="dag-inspector-section">
      <h4>Activity</h4>
      ${
        activity.length > 0
          ? `
            <div class="dag-bullet-list">
              ${activity
                .slice(0, 8)
                .map((call) => `<div>${escapeHTML(call.name || shortDigest(call.callID || "-"))}</div>`)
                .join("")}
            </div>
          `
          : `<div class="data-empty">No call activity is visible in this lens.</div>`
      }
    </div>
  `;

  els.inspector.innerHTML = `${header}${facts}${fieldSection}${dependencySection}${activitySection}`;

  wireObjectSelection(els.inspector);
  const clearFocusNode = els.inspector.querySelector("[data-clear-focus]");
  if (clearFocusNode) {
    clearFocusNode.addEventListener("click", () => {
      state.options.focusObjectID = "";
      state.options.mode = defaultModeFromOptions();
      refreshDag().catch((err) => showError(err));
    });
  }
}

function renderDependencyList(edges, objectByID, idKey) {
  if (!edges.length) {
    return `<div class="data-empty">None in the current lens.</div>`;
  }
  return `
    <div class="dag-bullet-list">
      ${edges
        .slice(0, 10)
        .map((edge) => {
          const otherID = edge[idKey];
          const other = objectByID.get(otherID);
          const title = other?.alias || other?.typeName || otherID;
          const label = edge.label ? ` <span class="dag-inline-label">${escapeHTML(edge.label)}</span>` : "";
          return `
            <div>
              <button class="dag-inline-object" data-select-object="${escapeHTML(otherID)}" type="button">${escapeHTML(title)}</button>${label}
            </div>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderGraph() {
  const objects = sortedObjects(state.render?.objects || []);
  const edges = fieldEdges();
  if (objects.length === 0) {
    els.dagCanvas.innerHTML = "";
    els.dagEmpty.textContent = "No objects are visible in this lens.";
    els.dagEmpty.style.display = "block";
    return;
  }

  const cardW = 268;
  const baseCardH = 96;
  const dividerOffset = 86;
  const fieldStartOffset = 108;
  const fieldRowHeight = 20;
  const expandedBottomPad = 18;
  const selectedObjectID = state.selectedObjectID;

  const cards = objects.map((obj) => {
    const selected = obj.objectID === selectedObjectID;
    const fields = latestStateFields(obj.currentState);
    let detailRows = [];
    if (selected) {
      if (fields.length > 0) {
        detailRows = fields.map((field) => ({ kind: "field", field }));
      } else if (obj.missingState) {
        detailRows = [
          { kind: "warn", text: "state unavailable" },
          { kind: "sub", text: `${obj.stateCount || 0} snapshot(s)` },
          { kind: "sub", text: `${(obj.activityCallIDs || []).length} call(s)` },
        ];
      } else {
        detailRows = [
          { kind: "sub", text: "no state fields" },
          { kind: "sub", text: `${obj.stateCount || 0} snapshot(s)` },
          { kind: "sub", text: `${(obj.activityCallIDs || []).length} call(s)` },
        ];
      }
    }
    return {
      obj,
      selected,
      detailRows,
      cardH: selected ? baseCardH + detailRows.length * fieldRowHeight + expandedBottomPad : baseCardH,
    };
  });

  const layout = computeGraphLayout(cards, edges, cardW);
  const positions = layout.positions;

  const edgeMarkup = edges
    .map((edge) => {
      const from = positions.get(edge.fromID);
      const to = positions.get(edge.toID);
      if (!from || !to) {
        return "";
      }
      const x1 = from.x + from.w;
      const y1 = from.y + from.h / 2;
      const x2 = to.x;
      const y2 = to.y + to.h / 2;
      const midX = (x1 + x2) / 2;
      const midY = (y1 + y2) / 2;
      const highlighted = selectedObjectID && (edge.fromID === selectedObjectID || edge.toID === selectedObjectID);
      const className = highlighted ? "dag-edge-line is-highlighted" : "dag-edge-line";
      const labelMarkup =
        highlighted && edge.label
          ? `<text class="dag-edge-label" x="${midX}" y="${midY - 8}">${escapeHTML(edge.label)}</text>`
          : "";
      return `
        <g>
          <path class="${className}" d="M ${x1} ${y1} C ${midX} ${y1}, ${midX} ${y2}, ${x2} ${y2}" />
          ${labelMarkup}
        </g>
      `;
    })
    .join("");

  const nodeMarkup = cards
    .map((card) => {
      const obj = card.obj;
      const pos = positions.get(obj.objectID);
      if (!pos) {
        return "";
      }
      const selected = card.selected;
      const focused = obj.objectID === state.options.focusObjectID;
      const topReferenced = obj.referencedByTop;
      const className = [
        "dag-node-card",
        selected ? "is-selected" : "",
        focused ? "is-focused" : "",
        topReferenced ? "is-top" : "",
      ]
        .filter(Boolean)
        .join(" ");

      const title = escapeHTML(obj.alias || obj.typeName || obj.objectID);
      const subtitle = escapeHTML(obj.typeName || "Object");
      const digest = escapeHTML(shortDigest(obj.currentDagqlID || obj.objectID));
      const meta = escapeHTML(`${obj.stateCount || 0} state(s)`);
      const badge = focused
        ? `<text class="dag-node-badge" x="${pos.x + pos.w - 20}" y="${pos.y + 18}">F</text>`
        : topReferenced
          ? `<text class="dag-node-badge" x="${pos.x + pos.w - 20}" y="${pos.y + 18}">T</text>`
          : "";
      const keyX = pos.x + 16;
      const valX = pos.x + 132;
      let rowsMarkup = "";
      if (selected) {
        rowsMarkup = `
          <line class="dag-node-divider" x1="${pos.x + 12}" y1="${pos.y + dividerOffset}" x2="${pos.x + pos.w - 12}" y2="${pos.y + dividerOffset}" />
          ${card.detailRows
            .map((row, idx) => {
              const y = pos.y + fieldStartOffset + idx * fieldRowHeight;
              if (row.kind === "field") {
                return `<text class="dag-node-prop-key" x="${keyX}" y="${y}">${escapeHTML(row.field.name)}</text>
          <text class="dag-node-prop-val" x="${valX}" y="${y}">${escapeHTML(row.field.value)}</text>`;
              }
              if (row.kind === "warn") {
                return `<text class="dag-node-warn" x="${keyX}" y="${y}">${escapeHTML(row.text)}</text>`;
              }
              return `<text class="dag-node-sub" x="${keyX}" y="${y}">${escapeHTML(row.text)}</text>`;
            })
            .join("")}
        `;
      }

      return `
        <g data-object-id="${escapeHTML(obj.objectID)}">
          <rect class="${className}" x="${pos.x}" y="${pos.y}" rx="16" ry="16" width="${pos.w}" height="${pos.h}" />
          ${badge}
          <text class="dag-node-title" x="${pos.x + 16}" y="${pos.y + 34}">${title}</text>
          <text class="dag-node-subtitle" x="${pos.x + 16}" y="${pos.y + 56}">${subtitle}</text>
          <text class="dag-node-meta" x="${pos.x + 16}" y="${pos.y + 78}">${digest} | ${meta}</text>
          ${rowsMarkup}
        </g>
      `;
    })
    .join("");

  els.dagCanvas.setAttribute("viewBox", `0 0 ${Math.max(1200, layout.width)} ${Math.max(700, layout.height)}`);
  els.dagCanvas.innerHTML = `
    <defs>
      <marker id="dagArrow" viewBox="0 0 12 12" refX="10" refY="6" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path d="M 0 0 L 12 6 L 0 12 z" fill="rgba(80, 108, 136, 0.72)"></path>
      </marker>
    </defs>
    ${edgeMarkup}
    ${nodeMarkup}
  `;
  els.dagEmpty.style.display = "none";

  for (const node of els.dagCanvas.querySelectorAll("[data-object-id]")) {
    node.addEventListener("click", () => {
      const objectID = node.getAttribute("data-object-id") || "";
      state.selectedObjectID = state.selectedObjectID === objectID ? "" : objectID;
      renderObjectList();
      renderInspector();
      renderGraph();
    });
    node.addEventListener("dblclick", () => {
      const objectID = node.getAttribute("data-object-id") || "";
      focusGraphOnObject(objectID);
    });
  }
}

function computeGraphLayout(cards, edges, cardW) {
  const gapX = 110;
  const gapY = 30;
  const padding = 56;

  const byID = new Map(cards.map((card) => [card.obj.objectID, card]));
  const ids = cards.map((card) => card.obj.objectID);
  const outgoing = new Map();
  const incoming = new Map();
  for (const id of ids) {
    outgoing.set(id, new Set());
    incoming.set(id, 0);
  }

  const seenEdges = new Set();
  for (const edge of edges) {
    if (!byID.has(edge.fromID) || !byID.has(edge.toID) || edge.fromID === edge.toID) {
      continue;
    }
    const key = `${edge.fromID}->${edge.toID}`;
    if (seenEdges.has(key)) {
      continue;
    }
    seenEdges.add(key);
    outgoing.get(edge.fromID).add(edge.toID);
    incoming.set(edge.toID, (incoming.get(edge.toID) || 0) + 1);
  }

  const compareIDs = (a, b) => compareObjectSummary(byID.get(a)?.obj, byID.get(b)?.obj);
  const queue = ids.filter((id) => (incoming.get(id) || 0) === 0).sort(compareIDs);
  const depth = new Map(ids.map((id) => [id, 0]));
  const processed = [];

  while (queue.length > 0) {
    const id = queue.shift();
    processed.push(id);
    const neighbors = Array.from(outgoing.get(id) || []).sort(compareIDs);
    for (const neighbor of neighbors) {
      depth.set(neighbor, Math.max(depth.get(neighbor) || 0, (depth.get(id) || 0) + 1));
      incoming.set(neighbor, (incoming.get(neighbor) || 0) - 1);
      if ((incoming.get(neighbor) || 0) === 0) {
        queue.push(neighbor);
        queue.sort(compareIDs);
      }
    }
  }

  if (processed.length < ids.length) {
    const maxDepth = Math.max(0, ...Array.from(depth.values()));
    const remaining = ids.filter((id) => !processed.includes(id)).sort(compareIDs);
    remaining.forEach((id, idx) => {
      depth.set(id, maxDepth + (idx % 2));
    });
  }

  const layers = new Map();
  for (const card of cards) {
    const layer = depth.get(card.obj.objectID) || 0;
    const rows = layers.get(layer) || [];
    rows.push(card);
    layers.set(layer, rows);
  }

  const orderedLayers = Array.from(layers.keys()).sort((a, b) => a - b);
  let maxLayerHeight = 96;
  for (const layer of orderedLayers) {
    const rows = layers.get(layer) || [];
    rows.sort((a, b) => compareObjectSummary(a.obj, b.obj));
    const layerHeight =
      rows.reduce((sum, card) => sum + card.cardH, 0) + Math.max(0, rows.length - 1) * gapY;
    maxLayerHeight = Math.max(maxLayerHeight, layerHeight);
  }

  const width = padding * 2 + orderedLayers.length * cardW + Math.max(0, orderedLayers.length - 1) * gapX;
  const height = padding * 2 + maxLayerHeight;
  const positions = new Map();

  orderedLayers.forEach((layer, layerIndex) => {
    const rows = layers.get(layer) || [];
    const layerHeight =
      rows.reduce((sum, card) => sum + card.cardH, 0) + Math.max(0, rows.length - 1) * gapY;
    let cursorY = padding + Math.max(0, (height - padding * 2 - layerHeight) / 2);
    const x = padding + layerIndex * (cardW + gapX);
    rows.forEach((card) => {
      positions.set(card.obj.objectID, { x, y: cursorY, w: cardW, h: card.cardH });
      cursorY += card.cardH + gapY;
    });
  });

  return { positions, width, height };
}

function focusGraphOnObject(objectID) {
  if (!objectID) {
    return;
  }
  state.selectedObjectID = objectID;
  state.options.focusObjectID = objectID;
  state.options.mode = state.options.scopeCallID ? "hybrid" : "object";
  refreshDag().catch((err) => showError(err));
}

function selectedObject() {
  return (state.render?.objects || []).find((obj) => obj.objectID === state.selectedObjectID) || null;
}

function fieldEdges() {
  return (state.render?.edges || []).filter((edge) => edge.kind === "field_ref");
}

function sortedObjects(objects) {
  return [...objects].sort(compareObjectSummary);
}

function compareObjectSummary(a, b) {
  const aliasCmp = String(a?.alias || "").localeCompare(String(b?.alias || ""), undefined, { sensitivity: "base" });
  if (aliasCmp !== 0) {
    return aliasCmp;
  }
  const typeCmp = String(a?.typeName || "").localeCompare(String(b?.typeName || ""), undefined, { sensitivity: "base" });
  if (typeCmp !== 0) {
    return typeCmp;
  }
  const seenCmp = Number(a?.firstSeenUnixNano || 0) - Number(b?.firstSeenUnixNano || 0);
  if (seenCmp !== 0) {
    return seenCmp;
  }
  return String(a?.objectID || "").localeCompare(String(b?.objectID || ""));
}

function renderNoScope(msg) {
  state.render = null;
  document.title = "ODAG DAG";
  els.dagSubtitle.textContent = "Open this page from the explorer to render a scoped object graph.";
  els.scopeSummary.innerHTML = `<span class="data-note">Choose an object or call as your starting point.</span>`;
  els.statusNote.textContent = msg;
  els.statGrid.innerHTML = "";
  els.objectListMeta.textContent = "0";
  els.objectList.innerHTML = `<div class="data-empty">${escapeHTML(msg)}</div>`;
  els.inspector.innerHTML = `<div class="data-empty">Select an object from the explorer first.</div>`;
  els.dagCanvas.innerHTML = "";
  els.dagEmpty.textContent = msg;
  els.dagEmpty.style.display = "block";
}

function wireObjectSelection(root) {
  for (const node of root.querySelectorAll("[data-select-object]")) {
    node.addEventListener("click", () => {
      const objectID = node.getAttribute("data-select-object") || "";
      if (!objectID) {
        return;
      }
      state.selectedObjectID = objectID;
      renderObjectList();
      renderInspector();
      renderGraph();
    });
  }

  for (const node of root.querySelectorAll("[data-focus-object]")) {
    node.addEventListener("click", () => {
      const objectID = node.getAttribute("data-focus-object") || "";
      focusGraphOnObject(objectID);
    });
  }
}

function statCard(label, value) {
  return `
    <article class="dag-stat-card">
      <span class="dag-stat-label">${escapeHTML(label)}</span>
      <span class="dag-stat-value">${escapeHTML(value)}</span>
    </article>
  `;
}

function scopeSummaryItem(label, valueHTML) {
  return `
    <span class="data-scope-item">
      <span class="data-scope-label">${escapeHTML(label)}</span>
      ${valueHTML}
    </span>
  `;
}

function staticChip(label) {
  return `<span class="scope-chip scope-chip-static">${escapeHTML(label)}</span>`;
}

function writeURL() {
  const url = new URL(window.location.href);
  const params = url.searchParams;
  params.delete("traceID");
  params.delete("sessionID");
  params.delete("clientID");
  params.delete("mode");
  params.delete("focusObjectID");
  params.delete("scopeCallID");
  params.delete("dependencyHops");
  params.delete("includeInternal");
  params.delete("keepRules");

  setSearchParam(params, "traceID", state.options.traceID);
  setSearchParam(params, "sessionID", state.options.sessionID);
  setSearchParam(params, "clientID", state.options.clientID);
  setSearchParam(params, "mode", state.options.mode);
  setSearchParam(params, "focusObjectID", state.options.focusObjectID);
  setSearchParam(params, "scopeCallID", state.options.scopeCallID);
  if (state.options.dependencyHops > 0) {
    params.set("dependencyHops", String(state.options.dependencyHops));
  }
  if (state.options.includeInternal) {
    params.set("includeInternal", "true");
  }
  params.set("keepRules", state.options.keepRules ? "default" : "off");
  window.history.replaceState({}, "", url);
}

async function fetchRender() {
  const params = new URLSearchParams();
  setSearchParam(params, "traceID", state.options.traceID);
  setSearchParam(params, "sessionID", state.options.sessionID);
  setSearchParam(params, "clientID", state.options.clientID);
  setSearchParam(params, "mode", state.options.mode);
  setSearchParam(params, "focusObjectID", state.options.focusObjectID);
  setSearchParam(params, "scopeCallID", state.options.scopeCallID);
  params.set("dependencyHops", String(state.options.dependencyHops));
  params.set("keepRules", state.options.keepRules ? "default" : "off");
  if (state.options.includeInternal) {
    params.set("includeInternal", "true");
  }
  return await fetchJSON(`/api/v2/render?${params.toString()}`);
}

function startLiveRefresh() {
  stopLiveRefresh();
  state.liveTimer = window.setInterval(() => {
    if (document.hidden || state.liveBusy) {
      return;
    }
    state.liveBusy = true;
    refreshDag()
      .catch((err) => showError(err))
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

function defaultModeFromOptions() {
  if (state.options.focusObjectID && state.options.scopeCallID) {
    return "hybrid";
  }
  if (state.options.focusObjectID) {
    return "object";
  }
  if (state.options.scopeCallID) {
    return "scope";
  }
  if (state.options.sessionID || state.options.clientID) {
    return "hybrid";
  }
  return "global";
}

function normalizeMode(raw) {
  switch (String(raw || "").toLowerCase()) {
    case "global":
    case "scope":
    case "object":
    case "hybrid":
      return String(raw || "").toLowerCase();
    default:
      return "";
  }
}

function clampDependencyHops(raw) {
  const value = Number.parseInt(String(raw || "1"), 10);
  if (!Number.isInteger(value)) {
    return 1;
  }
  return Math.max(0, Math.min(4, value));
}

function stringOrEmpty(raw) {
  return String(raw || "").trim();
}

function parseBoolish(raw) {
  switch (String(raw || "").toLowerCase()) {
    case "1":
    case "true":
    case "on":
    case "yes":
      return true;
    default:
      return false;
  }
}

function parseKeepRules(raw) {
  if (!raw) {
    return true;
  }
  switch (String(raw).toLowerCase()) {
    case "0":
    case "false":
    case "off":
    case "none":
      return false;
    default:
      return true;
  }
}

function setSearchParam(params, key, value) {
  if (value) {
    params.set(key, value);
  }
}

function latestStateFields(currentState) {
  if (!currentState || typeof currentState !== "object") {
    return [];
  }
  const fields = currentState.fields;
  if (!fields || typeof fields !== "object") {
    return [];
  }

  const out = [];
  for (const [fallbackName, raw] of Object.entries(fields)) {
    if (!raw || typeof raw !== "object") {
      continue;
    }
    const name = typeof raw.name === "string" && raw.name ? raw.name : fallbackName;
    out.push({
      name,
      value: formatStateValue(raw.value),
    });
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
    return truncateText(v, 40);
  }
  if (typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  if (Array.isArray(v)) {
    return `[${v.length}]`;
  }
  if (typeof v === "object") {
    if (typeof v.error === "string" && v.error) {
      return `error: ${truncateText(v.error, 22)}`;
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
  return `${v.slice(0, Math.max(1, maxLen - 3))}...`;
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

function showError(err) {
  console.error(err);
  els.statusNote.textContent = `Error: ${String(err)}`;
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

const OVERVIEW_ROUTE_ID = "overview";

const entities = [
  {
    id: "terminals",
    label: "Terminals",
    code: "TM",
    category: "Execution-centric",
    eyebrow: "Interactive runtime surfaces",
    blurb:
      "Terminal entities represent live interactive attachments and handoffs. They are discovered from continuity and operator behavior first, then mapped back onto objects and calls.",
    metrics: [
      { label: "Open sessions", value: "12", detail: "+3 in the latest mock window" },
      { label: "Attach p95", value: "42 ms", detail: "steady under the working budget" },
      { label: "Pending teardown", value: "2", detail: "both idle longer than 10m" },
    ],
    highlights: [
      { title: "release-shell-04", value: "Focused", note: "Pinned to workspace alpha with a single operator handoff." },
      { title: "triage-ssh-02", value: "Warm", note: "Collecting repro notes against the preview lane." },
      { title: "nightly-diff-01", value: "Queued", note: "Waiting on a service-backed readiness signal." },
    ],
    signals: [
      { label: "Attach latency", value: "42 ms", tone: "good", detail: "p95 over the current mock sample" },
      { label: "Idle pressure", value: "2 sessions", tone: "warn", detail: "old enough to merit cleanup" },
      { label: "Exit noise", value: "Low", tone: "neutral", detail: "few abrupt closes in recent runs" },
    ],
    evidence: [
      { kind: "Function call", confidence: "high", source: "Container.terminal()", note: "Repeated same-surface mutations point to one interactive session entity." },
      { kind: "TTY spans", confidence: "medium", source: "child span shape", note: "Attach, resize, and IO bursts cluster into one operator surface." },
      { kind: "Client mode", confidence: "medium", source: "shell-oriented client run", note: "Some terminals are launched from a broader shell session rather than a single call." },
    ],
    inventory: [
      { name: "release-shell-04", status: "ready", owner: "ci-bot", scope: "workspace alpha", updated: "2m ago" },
      { name: "triage-ssh-02", status: "live", owner: "ops", scope: "workspace beta", updated: "6m ago" },
      { name: "nightly-diff-01", status: "queued", owner: "scheduler", scope: "workspace gamma", updated: "12m ago" },
      { name: "handoff-shell-09", status: "cooldown", owner: "platform", scope: "workspace alpha", updated: "18m ago" },
    ],
    relations: [
      { source: "release-shell-04", relation: "feeds", target: "svc-cache-03", note: "interactive debug shell" },
      { source: "triage-ssh-02", relation: "opens", target: "repl-preview-7", note: "paired investigation lane" },
      { source: "nightly-diff-01", relation: "blocks on", target: "chk-release-acceptance", note: "awaiting latest signal" },
    ],
  },
  {
    id: "services",
    label: "Services",
    code: "SV",
    category: "Object-centric",
    eyebrow: "Long-lived runtime support",
    blurb:
      "Services are the clearest example of an entity that may map closely onto one or more object bindings, but still needs service-specific lifecycle and dependency views.",
    metrics: [
      { label: "Running", value: "7", detail: "one service in warm standby" },
      { label: "Cold start p95", value: "310 ms", detail: "cache service still dominates" },
      { label: "Edge count", value: "19", detail: "dense graph around auth and cache" },
    ],
    highlights: [
      { title: "svc-cache-03", value: "Hot path", note: "Touched by every check and most terminals." },
      { title: "svc-auth-01", value: "Stable", note: "No failed restarts in the latest mock window." },
      { title: "svc-preview-02", value: "Shadow", note: "Used only by preview repls and ad hoc checks." },
    ],
    signals: [
      { label: "Readiness drift", value: "None", tone: "good", detail: "all startup probes inside expected band" },
      { label: "Restart churn", value: "1 restart", tone: "warn", detail: "preview service recycled after config swap" },
      { label: "Dependency fan-in", value: "Cache high", tone: "neutral", detail: "expected but still worth watching" },
    ],
    evidence: [
      { kind: "Object output", confidence: "high", source: "Service outputs", note: "Service-like bindings and snapshots are directly visible in ODAG substrate data." },
      { kind: "Lifecycle spans", confidence: "high", source: "service start and health checks", note: "Readiness and teardown spans tighten service identity over time." },
      { kind: "Consumer fan-in", confidence: "medium", source: "dependent calls", note: "Many checks and terminals converge on a few stable service entities." },
    ],
    inventory: [
      { name: "svc-cache-03", status: "ready", owner: "runtime", scope: "shared infra", updated: "1m ago" },
      { name: "svc-auth-01", status: "ready", owner: "identity", scope: "shared infra", updated: "4m ago" },
      { name: "svc-preview-02", status: "warming", owner: "preview", scope: "sandbox lane", updated: "8m ago" },
      { name: "svc-queue-01", status: "degraded", owner: "runtime", scope: "async lane", updated: "15m ago" },
    ],
    relations: [
      { source: "svc-cache-03", relation: "backs", target: "chk-release-acceptance", note: "artifact reuse" },
      { source: "svc-auth-01", relation: "gates", target: "repl-preview-7", note: "token fanout" },
      { source: "svc-preview-02", relation: "serves", target: "release-shell-04", note: "interactive smoke path" },
    ],
  },
  {
    id: "calls",
    label: "Calls",
    code: "CL",
    category: "Substrate",
    eyebrow: "Function call substrate",
    blurb:
      "Calls are the atomic semantic operations in the trace: one function field invocation with a receiver, args, output snapshot, and parent-call context.",
    metrics: [
      { label: "Visible calls", value: "0", detail: "hydrates from /api/v2/calls" },
      { label: "Receivers", value: "0", detail: "linked from output and receiver DAGQL IDs" },
      { label: "Outputs", value: "0", detail: "each call can link directly to its output object page" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "functions",
    label: "Functions",
    code: "FN",
    category: "Substrate",
    eyebrow: "Schema-level function identities",
    blurb:
      "Functions aggregate semantic call identities across traces. Each row belongs to exactly one module, links to the calls that invoked it, and carries any recorded function metadata snapshots.",
    metrics: [
      { label: "Functions", value: "0", detail: "hydrates from /api/functions" },
      { label: "Calls", value: "0", detail: "semantic calls linked back to their canonical function row" },
      { label: "Snapshots", value: "0", detail: "Function metadata snapshots attached when schema state was recorded" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "objects",
    label: "Objects",
    code: "OB",
    category: "Substrate",
    eyebrow: "DAGQL object snapshots",
    blurb:
      "Objects are immutable snapshot values keyed by DAGQL ID. Their pages should answer what this snapshot contains, which call produced it, and which other snapshots it points at.",
    metrics: [
      { label: "Snapshots", value: "0", detail: "hydrates from /api/v2/object-snapshots" },
      { label: "Field refs", value: "0", detail: "links to other object pages when state exposes refs" },
      { label: "Produced by", value: "0", detail: "call links come from snapshot provenance" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "object-types",
    label: "Object Types",
    code: "TY",
    category: "Substrate",
    eyebrow: "Type-level aggregation",
    blurb:
      "Object types group snapshot and function metadata by type name, so custom module-defined types can be inspected without needing one lucky concrete snapshot on screen.",
    metrics: [
      { label: "Types", value: "0", detail: "hydrates from /api/object-types" },
      { label: "Snapshots", value: "0", detail: "real snapshot rows grouped by type name" },
      { label: "Functions", value: "0", detail: "function metadata rows that return this type" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "modules",
    label: "Modules",
    code: "MO",
    category: "Substrate",
    eyebrow: "Loaded schema sources",
    blurb:
      "Modules represent explicit loaded schema origins. They are the dependency anchors for custom object types and the clean place to show module-prelude calls.",
    metrics: [
      { label: "Modules", value: "0", detail: "hydrates from /api/modules" },
      { label: "Prelude calls", value: "0", detail: "moduleSource/asModule style setup calls" },
      { label: "Type deps", value: "0", detail: "object types can depend on one loaded module when provenance is unambiguous" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "repls",
    label: "Repls",
    code: "RP",
    category: "Execution-centric",
    eyebrow: "Iterative object spelunking",
    blurb:
      "Repls are defined more by continuity of an exploratory session than by a single object binding. They want history, overlays, and promotion paths back into workspaces or checks.",
    metrics: [
      { label: "Open repls", value: "5", detail: "two team-owned, three personal" },
      { label: "Persisted state", value: "18 GB", detail: "mostly workspace mirrors and caches" },
      { label: "Branch drift", value: "3", detail: "repls lagging behind target workspaces" },
    ],
    highlights: [
      { title: "repl-preview-7", value: "Shared", note: "Design review surface for the latest service shell." },
      { title: "repl-fixup-3", value: "Diverged", note: "Two commits ahead of its last attached check." },
      { title: "repl-scratch-9", value: "Ephemeral", note: "Auto-expiring unless promoted." },
    ],
    signals: [
      { label: "Unsaved overlays", value: "4", tone: "warn", detail: "repls with mutable state not attached to checks" },
      { label: "Promotion rate", value: "60%", tone: "good", detail: "share of repls graduating into workspaces" },
      { label: "Cold wake", value: "120 ms", tone: "neutral", detail: "good enough for interactive work" },
    ],
    evidence: [
      { kind: "Client continuity", confidence: "high", source: "reused connect scope", note: "Long-lived interactive client sessions are the strongest signal." },
      { kind: "Workspace affinity", confidence: "medium", source: "host path and mount patterns", note: "Most repls orbit one workspace but can fork away over time." },
      { kind: "Manual replay", confidence: "medium", source: "check and service reuse", note: "Repls often replay checks or inspect services without being reducible to them." },
    ],
    inventory: [
      { name: "repl-preview-7", status: "live", owner: "design", scope: "workspace beta", updated: "3m ago" },
      { name: "repl-fixup-3", status: "drifted", owner: "platform", scope: "workspace alpha", updated: "9m ago" },
      { name: "repl-scratch-9", status: "ephemeral", owner: "solo", scope: "workspace gamma", updated: "14m ago" },
      { name: "repl-check-bridge", status: "ready", owner: "qa", scope: "workspace beta", updated: "25m ago" },
    ],
    relations: [
      { source: "repl-preview-7", relation: "forked from", target: "ws-beta", note: "live design lane" },
      { source: "repl-fixup-3", relation: "replays", target: "chk-schema-drift", note: "manual patch verification" },
      { source: "repl-check-bridge", relation: "attaches", target: "svc-auth-01", note: "tokened API mock" },
    ],
  },
  {
    id: "checks",
    label: "Checks",
    code: "CK",
    category: "Mixed",
    eyebrow: "Policy and proof surfaces",
    blurb:
      "Checks sit between execution and object state. Some behave like reusable calls, others like long-lived quality entities with their own evidence, flake history, and gates.",
    metrics: [
      { label: "Passing", value: "26", detail: "4 currently running" },
      { label: "Flaky", value: "3", detail: "all service-backed" },
      { label: "Queue wait", value: "1.8 m", detail: "slight bump after the last fan-out" },
    ],
    highlights: [
      { title: "chk-release-acceptance", value: "Critical", note: "High-confidence gate with wide dependency reach." },
      { title: "chk-schema-drift", value: "Noisy", note: "Useful signal but still chatty in preview lanes." },
      { title: "chk-shell-health", value: "Cheap", note: "Fast enough to run inline with terminal launch." },
    ],
    signals: [
      { label: "Failure density", value: "Low", tone: "good", detail: "most failures isolated to one service lane" },
      { label: "Flake pressure", value: "3 checks", tone: "warn", detail: "same group as last mock refresh" },
      { label: "Template reuse", value: "High", tone: "neutral", detail: "good candidate for later dedupe work" },
    ],
    evidence: [
      { kind: "Call naming", confidence: "medium", source: "module function patterns", note: "Some checks are obvious from names, but taxonomy should not rely on names alone." },
      { kind: "Outcome history", confidence: "high", source: "pass and fail spans", note: "Repeated gate behavior makes checks stable derived entities." },
      { kind: "Dependency reuse", confidence: "medium", source: "services and workspaces", note: "Checks often anchor on a stable set of dependencies over time." },
    ],
    inventory: [
      { name: "chk-release-acceptance", status: "passing", owner: "release", scope: "shared gate", updated: "1m ago" },
      { name: "chk-schema-drift", status: "flaky", owner: "platform", scope: "workspace alpha", updated: "7m ago" },
      { name: "chk-shell-health", status: "passing", owner: "runtime", scope: "terminal lane", updated: "11m ago" },
      { name: "chk-preview-links", status: "queued", owner: "preview", scope: "workspace beta", updated: "16m ago" },
    ],
    relations: [
      { source: "chk-release-acceptance", relation: "depends on", target: "svc-cache-03", note: "artifact cache is mandatory" },
      { source: "chk-schema-drift", relation: "replayed in", target: "repl-fixup-3", note: "manual verification loop" },
      { source: "chk-shell-health", relation: "guards", target: "release-shell-04", note: "preflight before attach" },
    ],
  },
  {
    id: "workspaces",
    label: "Workspaces",
    code: "WS",
    navHidden: true,
    category: "External-resource-centric",
    eyebrow: "Host composition roots",
    blurb:
      "Workspaces are broader than a single object. They are defined by host context, mounts, exports, and the surfaces they collect on behalf of clients and objects.",
    metrics: [
      { label: "Active workspaces", value: "4", detail: "one staging lane, three active branches" },
      { label: "Mounted entities", value: "31", detail: "terminals, repls, services, and checks combined" },
      { label: "Config drift", value: "1", detail: "preview workspace differs from baseline" },
    ],
    highlights: [
      { title: "ws-alpha", value: "Primary", note: "Hosts the release and runtime surfaces." },
      { title: "ws-beta", value: "Preview", note: "Carries most design and repl activity." },
      { title: "ws-gamma", value: "Lab", note: "Low-traffic sandbox for speculative changes." },
    ],
    signals: [
      { label: "Workspace churn", value: "Moderate", tone: "neutral", detail: "mostly repl and check movement" },
      { label: "Mount saturation", value: "31 entities", tone: "warn", detail: "alpha is getting crowded" },
      { label: "Ownership clarity", value: "High", tone: "good", detail: "each workspace still has a clear steward" },
    ],
    evidence: [
      { kind: "Host path use", confidence: "high", source: "workspace root and mount spans", note: "File and directory operations cluster around stable workspace roots." },
      { kind: "Entity fan-in", confidence: "medium", source: "attached terminals, repls, and checks", note: "Workspaces collect many other entities without owning them semantically." },
      { kind: "Export activity", confidence: "medium", source: "host-side effects", note: "Exports and local host access are strong workspace-oriented clues." },
    ],
    inventory: [
      { name: "ws-alpha", status: "loaded", owner: "platform", scope: "release lane", updated: "2m ago" },
      { name: "ws-beta", status: "loaded", owner: "design", scope: "preview lane", updated: "5m ago" },
      { name: "ws-gamma", status: "light", owner: "solo", scope: "lab lane", updated: "13m ago" },
      { name: "ws-staging", status: "warming", owner: "release", scope: "staging lane", updated: "21m ago" },
    ],
    relations: [
      { source: "ws-alpha", relation: "contains", target: "release-shell-04", note: "release terminal surface" },
      { source: "ws-beta", relation: "hosts", target: "repl-preview-7", note: "preview iteration lane" },
      { source: "ws-staging", relation: "collects", target: "chk-release-acceptance", note: "staging verification pass" },
    ],
  },
  {
    id: "devices",
    label: "Devices",
    code: "DV",
    category: "Host-centric",
    eyebrow: "Top-level client origins",
    blurb:
      "Devices represent stable host identities derived from top-level clients only. They anchor the sessions, pipelines, and local workspaces that originate from one user machine.",
    metrics: [
      { label: "Detected devices", value: "3", detail: "top-level clients collapsed into stable machine identities" },
      { label: "Active sessions", value: "7", detail: "recent execution lanes started from those hosts" },
      { label: "Observed workspaces", value: "4", detail: "local roots touched by those devices" },
    ],
    highlights: [
      { title: "Device 5e4d46", value: "Primary", note: "Owns the release and docs command lanes." },
      { title: "Device 9ac211", value: "Preview", note: "Mostly drives sandbox and shell exploration." },
      { title: "Device f03ab8", value: "Cold", note: "Historical host with no recent local workspace activity." },
    ],
    signals: [
      { label: "Root-only", value: "Yes", tone: "good", detail: "Nested module/runtime clients do not create device identities." },
      { label: "Host spread", value: "3 devices", tone: "neutral", detail: "Distinct top-level machine fingerprints in the current sample." },
      { label: "Workspace overlap", value: "Low", tone: "good", detail: "Most observed roots still map cleanly back to one host." },
    ],
    evidence: [
      { kind: "Client machine ID", confidence: "high", source: "top-level client resource labels", note: "Anonymous machine fingerprints remain stable enough to group repeated command roots." },
      { kind: "Root-client boundary", confidence: "high", source: "parent-client graph", note: "Only top-level clients contribute to device identity; nested runtimes do not." },
      { kind: "Workspace attachment", confidence: "medium", source: "session and client scope", note: "Local workspaces can be attached back to a device through the top-level client lane that touched them." },
    ],
    inventory: [
      { name: "Device 5e4d46", status: "active", owner: "platform", scope: "darwin arm64", updated: "2m ago" },
      { name: "Device 9ac211", status: "active", owner: "design", scope: "linux amd64", updated: "12m ago" },
      { name: "Device f03ab8", status: "idle", owner: "release", scope: "linux amd64", updated: "3h ago" },
    ],
    relations: [
      { source: "Device 5e4d46", relation: "started", target: "session-release-main", note: "top-level command lane" },
      { source: "Device 5e4d46", relation: "submitted", target: "call-release-91", note: "one-shot pipeline" },
      { source: "Device 9ac211", relation: "touched", target: "ws-beta", note: "local preview workspace" },
    ],
  },
  {
    id: "clients",
    label: "Clients",
    code: "CT",
    category: "Execution-centric",
    eyebrow: "Execution roots and nested runtimes",
    blurb:
      "Clients are the execution entry surfaces ODAG derives from engine connect spans. They capture the submitted command, SDK identity when declared, parent/root relationships, and the DAGQL calls owned by that execution lane.",
    metrics: [
      { label: "Detected clients", value: "0", detail: "hydrates from /api/v2/clients" },
      { label: "Nested clients", value: "0", detail: "child runtimes attached to one root session tree" },
      { label: "SDK-labeled", value: "0", detail: "clients that declared dagger.io/sdk.name" },
    ],
    highlights: [],
    signals: [],
    evidence: [],
    inventory: [],
    relations: [],
  },
  {
    id: "sessions",
    label: "Sessions",
    code: "SE",
    category: "Execution-centric",
    eyebrow: "Root client execution containers",
    blurb:
      "Sessions are the primitive execution lanes derived from root clients. They are broader than one CLI run and narrower than a whole trace.",
    metrics: [
      { label: "Detected sessions", value: "6", detail: "mixed interactive and one-shot command roots" },
      { label: "Open sessions", value: "2", detail: "still ingesting in the current mock window" },
      { label: "Fallback sessions", value: "0", detail: "all current samples have real root-client derivation" },
    ],
    highlights: [
      { title: "session-release-main", value: "Stable", note: "One release-oriented root client with several downstream commands." },
      { title: "session-preview-shell", value: "Open", note: "Interactive root session still collecting child activity." },
      { title: "session-lab-scratch", value: "Short", note: "Ephemeral experimental lane with little downstream fanout." },
    ],
    signals: [
      { label: "Root clarity", value: "High", tone: "good", detail: "sessions currently map cleanly to root clients" },
      { label: "Open pressure", value: "2 sessions", tone: "neutral", detail: "long-lived sessions still deserve direct visibility" },
      { label: "Fallback use", value: "None", tone: "good", detail: "no trace-only session fallback in the current sample" },
    ],
    evidence: [
      { kind: "Root client", confidence: "high", source: "connect span ancestry", note: "Sessions are derived from root clients rather than from raw traces." },
      { kind: "Client tree", confidence: "high", source: "parent-client graph", note: "Each descendant client stays attached to exactly one session." },
      { kind: "Trace boundary", confidence: "medium", source: "ingest container", note: "Trace still matters, but only as a container around the execution tree." },
    ],
    inventory: [
      { name: "session-release-main", status: "completed", owner: "release", scope: "root-client release-main", updated: "4m ago" },
      { name: "session-preview-shell", status: "open", owner: "design", scope: "root-client preview-shell", updated: "9m ago" },
      { name: "session-lab-scratch", status: "completed", owner: "solo", scope: "root-client lab-scratch", updated: "18m ago" },
    ],
    relations: [
      { source: "session-release-main", relation: "owns", target: "call-release-91", note: "one-shot command lane" },
      { source: "session-preview-shell", relation: "contains", target: "shell-preview-main", note: "interactive shell root" },
      { source: "session-lab-scratch", relation: "contains", target: "repl-scratch-9", note: "experimental lane" },
    ],
  },
  {
    id: "pipelines",
    label: "Pipelines",
    code: "PL",
    category: "Execution-centric",
    eyebrow: "One-shot command sessions",
    blurb:
      "Pipeline entities group telemetry around one submitted DAGQL call chain. They are execution-scoped first, with objects as outputs and evidence rather than the primary identity.",
    metrics: [
      { label: "Recent pipelines", value: "18", detail: "mostly one-shot module calls" },
      { label: "Median duration", value: "9.4 s", detail: "healthy for the current mock mix" },
      { label: "Failed runs", value: "2", detail: "both tied to remote fetch issues" },
    ],
    highlights: [
      { title: "call-release-91", value: "Stable", note: "Single check-focused command with reusable outputs." },
      { title: "call-preview-17", value: "Noisy", note: "Fan-out command that touches shell and registry surfaces." },
      { title: "call-git-sync-05", value: "Remote", note: "Mostly interesting for its git and export side effects." },
    ],
    signals: [
      { label: "Retry pressure", value: "Low", tone: "good", detail: "few re-run attempts in the sample" },
      { label: "Remote coupling", value: "2 pipelines", tone: "warn", detail: "latest failures involved remote resources" },
      { label: "Output reuse", value: "Moderate", tone: "neutral", detail: "some outputs flow into checks and services" },
    ],
    evidence: [
      { kind: "Command root", confidence: "high", source: "CLI root span context", note: "The command invocation itself anchors entity identity strongly." },
      { kind: "Client kind", confidence: "high", source: "dagger call mode", note: "Command-mode clients should classify cleanly once surfaced explicitly." },
      { kind: "Output set", confidence: "medium", source: "returned objects and side effects", note: "Useful for summarizing a run, but not the main identity." },
    ],
    inventory: [
      { name: "call-release-91", status: "ready", owner: "release", scope: "main branch", updated: "4m ago" },
      { name: "call-preview-17", status: "running", owner: "preview", scope: "feature branch", updated: "8m ago" },
      { name: "call-git-sync-05", status: "failed", owner: "ops", scope: "sync lane", updated: "19m ago" },
      { name: "call-docs-33", status: "ready", owner: "docs", scope: "docs lane", updated: "27m ago" },
    ],
    relations: [
      { source: "call-release-91", relation: "triggers", target: "chk-release-acceptance", note: "one-shot release gate" },
      { source: "call-preview-17", relation: "touches", target: "reg-preview-01", note: "pushes preview image" },
      { source: "call-git-sync-05", relation: "fetches", target: "git-origin-main", note: "remote workspace sync" },
    ],
  },
  {
    id: "shells",
    label: "Shells",
    code: "SH",
    navHidden: true,
    navOwnerID: "repls",
    category: "Execution-centric",
    eyebrow: "Long-lived client command mode",
    blurb:
      "Shell entities capture `dagger shell` style sessions. They are broader than a terminal attachment and often act as parent context for terminals, repls, and ad hoc object inspection.",
    metrics: [
      { label: "Open shells", value: "3", detail: "all user-driven in the current mock set" },
      { label: "Median age", value: "24 m", detail: "longer lived than one-shot CLI runs" },
      { label: "Child surfaces", value: "11", detail: "terminals and repls spawned from shells" },
    ],
    highlights: [
      { title: "shell-preview-main", value: "Busy", note: "Parent context for most preview terminals." },
      { title: "shell-hotfix-beta", value: "Focused", note: "Small, service-oriented live repair session." },
      { title: "shell-lab-gamma", value: "Exploratory", note: "Mostly object inspection and scratch work." },
    ],
    signals: [
      { label: "Spawn density", value: "11 children", tone: "neutral", detail: "shells create many downstream surfaces" },
      { label: "Idle age", value: "1 shell", tone: "warn", detail: "stale but still attached" },
      { label: "Ownership clarity", value: "High", tone: "good", detail: "all shells have one clear operator" },
    ],
    evidence: [
      { kind: "Client kind", confidence: "high", source: "dagger shell mode", note: "Once exposed explicitly this should classify almost perfectly." },
      { kind: "Child terminals", confidence: "medium", source: "interactive spawn patterns", note: "Shells often parent multiple terminals and repl-like flows." },
      { kind: "Session continuity", confidence: "high", source: "shared connect scope", note: "Long-running client context is stronger than any single object output." },
    ],
    inventory: [
      { name: "shell-preview-main", status: "live", owner: "design", scope: "workspace beta", updated: "2m ago" },
      { name: "shell-hotfix-beta", status: "attached", owner: "ops", scope: "workspace alpha", updated: "10m ago" },
      { name: "shell-lab-gamma", status: "idle", owner: "solo", scope: "workspace gamma", updated: "22m ago" },
      { name: "shell-release-review", status: "ready", owner: "release", scope: "staging lane", updated: "31m ago" },
    ],
    relations: [
      { source: "shell-preview-main", relation: "spawns", target: "triage-ssh-02", note: "interactive terminal branch" },
      { source: "shell-hotfix-beta", relation: "inspects", target: "svc-auth-01", note: "live service repair" },
      { source: "shell-release-review", relation: "replays", target: "chk-shell-health", note: "manual validation loop" },
    ],
  },
  {
    id: "workspace-ops",
    label: "Workspace Ops",
    code: "WO",
    navHidden: true,
    navOwnerID: "workspaces",
    category: "External-resource-centric",
    eyebrow: "Host-side movement and export",
    blurb:
      "Workspace ops are not stable objects by themselves. They are derived from side effects such as export, import, and host access around a client workspace boundary.",
    metrics: [
      { label: "Recent ops", value: "14", detail: "exports, imports, and host directory reads" },
      { label: "Write-heavy", value: "4", detail: "materialized local changes" },
      { label: "Scope fan-out", value: "3 roots", detail: "activity clustered around three workspaces" },
    ],
    highlights: [
      { title: "export-release-artifacts", value: "Heavy", note: "Largest materialized write in the mock set." },
      { title: "host-dir-preview", value: "Broad", note: "Reads a large preview workspace tree." },
      { title: "import-docs-cache", value: "Reusable", note: "Shared across several check and call runs." },
    ],
    signals: [
      { label: "Write pressure", value: "4 ops", tone: "warn", detail: "could merit a dedicated risk view later" },
      { label: "Boundary clarity", value: "Good", tone: "good", detail: "ops still cluster around a few stable roots" },
      { label: "Reuse", value: "Moderate", tone: "neutral", detail: "imports and exports repeat across runs" },
    ],
    evidence: [
      { kind: "Function call", confidence: "high", source: "File.export and Directory.export", note: "Export APIs provide crisp side-effect signals." },
      { kind: "Host access", confidence: "high", source: "Host.directory and local path spans", note: "Workspace boundary interactions identify this domain well." },
      { kind: "Path affinity", confidence: "medium", source: "workspace-root clustering", note: "Ops group more naturally by host root than by object binding." },
    ],
    inventory: [
      { name: "export-release-artifacts", status: "running", owner: "release", scope: "ws-alpha", updated: "5m ago" },
      { name: "host-dir-preview", status: "ready", owner: "design", scope: "ws-beta", updated: "9m ago" },
      { name: "import-docs-cache", status: "ready", owner: "docs", scope: "ws-gamma", updated: "17m ago" },
      { name: "export-preview-snapshot", status: "queued", owner: "preview", scope: "ws-beta", updated: "28m ago" },
    ],
    relations: [
      { source: "export-release-artifacts", relation: "writes", target: "ws-alpha", note: "materialized release outputs" },
      { source: "host-dir-preview", relation: "reads", target: "ws-beta", note: "large host tree scan" },
      { source: "import-docs-cache", relation: "feeds", target: "chk-preview-links", note: "reused imported content" },
    ],
  },
  {
    id: "git-remotes",
    label: "Git Remotes",
    code: "GT",
    category: "External-resource-centric",
    eyebrow: "Remote source dependencies",
    blurb:
      "Git remotes are defined by external repository identity and access patterns. They can participate in many calls and workspaces without behaving like one mutable local object.",
    metrics: [
      { label: "Remotes touched", value: "6", detail: "two high-frequency, four incidental" },
      { label: "Fetch p95", value: "780 ms", detail: "wide spread by remote and depth" },
      { label: "Auth failures", value: "1", detail: "single mock credentials miss" },
    ],
    highlights: [
      { title: "git-origin-main", value: "Primary", note: "Default remote for most workspace syncs." },
      { title: "git-preview-upstream", value: "Volatile", note: "Preview lane rebases and forks are concentrated here." },
      { title: "git-docs-mirror", value: "Cached", note: "Usually fetched through a warm path." },
    ],
    signals: [
      { label: "Latency spread", value: "High", tone: "warn", detail: "remote fetch cost varies widely" },
      { label: "Cache benefit", value: "Visible", tone: "good", detail: "mirrored remotes are materially faster" },
      { label: "Fan-out", value: "Moderate", tone: "neutral", detail: "a few runs depend on each remote" },
    ],
    evidence: [
      { kind: "Remote URL", confidence: "high", source: "git source spans", note: "Remote identity is explicit and external." },
      { kind: "Workspace sync", confidence: "medium", source: "checkout and update flows", note: "Many remote entities matter because of their workspace impact." },
      { kind: "Registry adjacency", confidence: "low", source: "shared CI runs", note: "Some runs touch both git and registry surfaces but they should stay distinct." },
    ],
    inventory: [
      { name: "git-origin-main", status: "ready", owner: "platform", scope: "primary repo", updated: "6m ago" },
      { name: "git-preview-upstream", status: "warming", owner: "preview", scope: "preview repo", updated: "13m ago" },
      { name: "git-docs-mirror", status: "ready", owner: "docs", scope: "mirror repo", updated: "24m ago" },
      { name: "git-ops-hotfix", status: "failed", owner: "ops", scope: "ops repo", updated: "33m ago" },
    ],
    relations: [
      { source: "git-origin-main", relation: "hydrates", target: "ws-alpha", note: "main workspace source" },
      { source: "git-preview-upstream", relation: "feeds", target: "call-preview-17", note: "remote code for preview command" },
      { source: "git-docs-mirror", relation: "backs", target: "chk-preview-links", note: "docs verification source" },
    ],
  },
  {
    id: "registries",
    label: "Registries",
    code: "RG",
    category: "External-resource-centric",
    eyebrow: "Remote image and artifact endpoints",
    blurb:
      "Registry entities capture remote pushes, pulls, and authentication surfaces. They are external dependencies with their own health and churn, not just another local object chain.",
    metrics: [
      { label: "Registries touched", value: "4", detail: "one primary, three lane-specific" },
      { label: "Push p95", value: "1.9 s", detail: "preview pushes dominate the tail" },
      { label: "Auth churn", value: "2 events", detail: "token refresh and one denied push" },
    ],
    highlights: [
      { title: "reg-preview-01", value: "Busy", note: "Most preview images land here." },
      { title: "reg-release-00", value: "Critical", note: "Protected release destination." },
      { title: "reg-cache-02", value: "Support", note: "Acts as a cache-first pull source." },
    ],
    signals: [
      { label: "Push stability", value: "Mostly good", tone: "good", detail: "one denied push in the sample" },
      { label: "Credential churn", value: "2 events", tone: "warn", detail: "still a useful watch surface" },
      { label: "Consumer spread", value: "Moderate", tone: "neutral", detail: "shared by services and CLI runs" },
    ],
    evidence: [
      { kind: "Remote endpoint", confidence: "high", source: "registry URLs", note: "External endpoint identity is explicit and stable." },
      { kind: "Push and pull spans", confidence: "high", source: "distribution activity", note: "Network operations make the domain obvious." },
      { kind: "Service adjacency", confidence: "medium", source: "service and build outputs", note: "Registries matter through both object outputs and external side effects." },
    ],
    inventory: [
      { name: "reg-preview-01", status: "ready", owner: "preview", scope: "preview lane", updated: "4m ago" },
      { name: "reg-release-00", status: "protected", owner: "release", scope: "release lane", updated: "15m ago" },
      { name: "reg-cache-02", status: "warm", owner: "runtime", scope: "cache lane", updated: "26m ago" },
      { name: "reg-ops-failover", status: "degraded", owner: "ops", scope: "failover lane", updated: "39m ago" },
    ],
    relations: [
      { source: "reg-preview-01", relation: "receives", target: "call-preview-17", note: "preview image publish" },
      { source: "reg-release-00", relation: "ships", target: "svc-preview-02", note: "release artifact source" },
      { source: "reg-cache-02", relation: "accelerates", target: "svc-cache-03", note: "cache-backed pulls" },
    ],
  },
];

const liveDomainConfigs = {
  terminals: {
    stateKey: "terminals",
    endpoint: "/api/terminals?limit=100",
    label: "Terminals",
    singularLabel: "Terminal",
  },
  repls: {
    stateKey: "repls",
    endpoint: "/api/repls?limit=100",
    label: "Repls",
    singularLabel: "Repl",
  },
  checks: {
    stateKey: "checks",
    endpoint: "/api/checks?limit=200",
    label: "Checks",
    singularLabel: "Check",
  },
  workspaces: {
    stateKey: "workspaces",
    endpoint: "/api/workspaces?limit=200",
    label: "Workspaces",
    singularLabel: "Workspace",
  },
  devices: {
    stateKey: "devices",
    endpoint: "/api/devices?limit=200",
    label: "Devices",
    singularLabel: "Device",
  },
  clients: {
    stateKey: "clients",
    endpoint: "/api/v2/clients?limit=400",
    label: "Clients",
    singularLabel: "Client",
  },
  calls: {
    stateKey: "calls",
    endpoint: "/api/v2/calls?limit=400",
    label: "Calls",
    singularLabel: "Call",
  },
  functions: {
    stateKey: "functions",
    endpoint: "/api/functions?limit=200",
    label: "Functions",
    singularLabel: "Function",
  },
  objects: {
    stateKey: "objects",
    endpoint: "/api/v2/object-snapshots?limit=400",
    label: "Objects",
    singularLabel: "Object",
  },
  "object-types": {
    stateKey: "objectTypes",
    endpoint: "/api/object-types?limit=200",
    label: "Object Types",
    singularLabel: "Object Type",
  },
  modules: {
    stateKey: "modules",
    endpoint: "/api/modules?limit=200",
    label: "Modules",
    singularLabel: "Module",
  },
  services: {
    stateKey: "services",
    endpoint: "/api/services?limit=200",
    label: "Services",
    singularLabel: "Service",
  },
  sessions: {
    stateKey: "sessions",
    endpoint: "/api/sessions?limit=100",
    label: "Sessions",
    singularLabel: "Session",
  },
  pipelines: {
    stateKey: "pipelines",
    endpoint: "/api/pipelines?limit=100",
    label: "Pipelines",
    singularLabel: "Pipeline",
  },
  shells: {
    stateKey: "shells",
    endpoint: "/api/shells?limit=100",
    label: "Shells",
    singularLabel: "Shell",
  },
  "workspace-ops": {
    stateKey: "workspaceOps",
    endpoint: "/api/workspace-ops?limit=200",
    label: "Workspace Ops",
    singularLabel: "Workspace Op",
  },
  "git-remotes": {
    stateKey: "gitRemotes",
    endpoint: "/api/git-remotes?limit=200",
    label: "Git Remotes",
    singularLabel: "Git Remote",
  },
  registries: {
    stateKey: "registries",
    endpoint: "/api/registries?limit=200",
    label: "Registries",
    singularLabel: "Registry",
  },
};

const workspaceScopeCache = new WeakMap();
const deviceScopeCache = new WeakMap();
const moduleSnapshotCanonicalCache = new WeakMap();
let autoTableCounter = 0;
const nonPageObjectTypeNames = new Set([
  "Boolean",
  "Env",
  "Function",
  "Host",
  "Int",
  "Module",
  "ModuleSource",
  "ModuleSourceKind",
  "Query",
  "String",
  "TypeDef",
  "Void",
]);

const sessionHubEntityIDs = [
  "clients",
  "pipelines",
  "repls",
  "shells",
  "terminals",
  "services",
  "checks",
  "workspaces",
  "workspace-ops",
  "git-remotes",
  "registries",
];

const deviceDetailEntityIDs = [
  "devices",
  "sessions",
  "pipelines",
  "workspaces",
  "workspace-ops",
];

const clientDetailEntityIDs = [
  "clients",
  "calls",
  "devices",
  "sessions",
  "modules",
];

const substrateDetailEntityIDs = [
  "calls",
  "functions",
  "objects",
  "object-types",
  "modules",
  "sessions",
  "devices",
  "clients",
];

const state = {
  entityID: OVERVIEW_ROUTE_ID,
  detailID: "",
  workspaceFilterID: "",
  sessionFilterID: "",
  tableControls: {},
  importTraceOpen: false,
  importTraceTraceID: "",
  importTraceOrg: "",
  importTraceStatus: "idle",
  importTraceMessage: "",
  importTraceImportedID: "",
  sessionFilterOpen: false,
  sessionFilterQuery: "",
  detailGraphs: {
    pipelines: {},
    functionCalls: {},
    clientCalls: {},
  },
  live: {
    terminals: {
      status: "idle",
      items: [],
      error: "",
    },
    repls: {
      status: "idle",
      items: [],
      error: "",
    },
    checks: {
      status: "idle",
      items: [],
      error: "",
    },
    workspaces: {
      status: "idle",
      items: [],
      error: "",
    },
    devices: {
      status: "idle",
      items: [],
      error: "",
    },
    calls: {
      status: "idle",
      items: [],
      error: "",
    },
    functions: {
      status: "idle",
      items: [],
      error: "",
    },
    objects: {
      status: "idle",
      items: [],
      error: "",
    },
    objectTypes: {
      status: "idle",
      items: [],
      error: "",
    },
    modules: {
      status: "idle",
      items: [],
      error: "",
    },
    clients: {
      status: "idle",
      items: [],
      error: "",
    },
    services: {
      status: "idle",
      items: [],
      error: "",
    },
    sessions: {
      status: "idle",
      items: [],
      error: "",
    },
    pipelines: {
      status: "idle",
      items: [],
      error: "",
    },
    shells: {
      status: "idle",
      items: [],
      error: "",
    },
    workspaceOps: {
      status: "idle",
      items: [],
      error: "",
    },
    gitRemotes: {
      status: "idle",
      items: [],
      error: "",
    },
    registries: {
      status: "idle",
      items: [],
      error: "",
    },
  },
};

const els = {
  sidebarBrand: document.getElementById("sidebarBrand"),
  workspaceFilter: document.getElementById("workspaceFilter"),
  importTraceShell: document.getElementById("importTraceShell"),
  sessionFilterShell: document.getElementById("sessionFilterShell"),
  pageCrumb: document.getElementById("pageCrumb"),
  entityNav: document.getElementById("entityNav"),
  panelHead: document.querySelector(".v3-panel-head"),
  tableTitle: document.getElementById("tableTitle"),
  tableMeta: document.getElementById("tableMeta"),
  tableShell: document.getElementById("tableShell"),
};

init();

function init() {
  readURLState();
  syncWorkspaceFilterFromRoute();
  syncSessionFilterFromRoute();
  bindEvents();
  render();
  void ensureActiveEntityData();
}

function bindEvents() {
  els.workspaceFilter?.addEventListener("change", () => {
    state.workspaceFilterID = String(els.workspaceFilter.value || "");
    state.sessionFilterOpen = false;
    state.sessionFilterQuery = "";
    state.importTraceOpen = false;
    sanitizeSessionFilterSelection();
    render();
    void ensureActiveEntityData();
  });

  window.addEventListener("popstate", () => {
    readURLState();
    syncWorkspaceFilterFromRoute();
    syncSessionFilterFromRoute();
    render();
    void ensureActiveEntityData();
  });

  document.addEventListener("pointerdown", (event) => {
    const target = event.target;
    let changed = false;
    if (state.sessionFilterOpen && els.sessionFilterShell && !els.sessionFilterShell.contains(target)) {
      state.sessionFilterOpen = false;
      state.sessionFilterQuery = "";
      changed = true;
    }
    if (state.importTraceOpen && els.importTraceShell && !els.importTraceShell.contains(target)) {
      state.importTraceOpen = false;
      changed = true;
    }
    if (changed) {
      render();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") {
      return;
    }
    let changed = false;
    if (state.sessionFilterOpen) {
      state.sessionFilterOpen = false;
      state.sessionFilterQuery = "";
      changed = true;
    }
    if (state.importTraceOpen) {
      state.importTraceOpen = false;
      changed = true;
    }
    if (changed) {
      render();
    }
  });

  document.addEventListener("click", (event) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    const link = event.target.closest("a[data-route-path]");
    if (!link || link.target === "_blank" || link.hasAttribute("download")) {
      return;
    }
    const nextPath = link.getAttribute("data-route-path");
    if (!nextPath) {
      return;
    }
    event.preventDefault();
    navigateTo(nextPath);
  });
}

function readURLState() {
  const route = parseRoute(window.location.pathname, window.location.search);
  state.entityID = route.entityID;
  state.detailID = route.detailID;
}

function parseRoute(pathname, search) {
  const segments = String(pathname || "/")
    .split("/")
    .filter(Boolean)
    .map((segment) => decodeURIComponent(segment));
  const legacyID = legacyEntityID(search);
  if (!segments.length) {
    return {
      entityID: legacyID || OVERVIEW_ROUTE_ID,
      detailID: "",
    };
  }
  if (segments[0] === OVERVIEW_ROUTE_ID) {
    return {
      entityID: OVERVIEW_ROUTE_ID,
      detailID: "",
    };
  }
  const entityID = resolveEntityID(String(segments[0] || "").toLowerCase()) || legacyID;
  const detailID = supportsDetailRoute(entityID) && segments[1] ? segments[1] : "";
  return {
    entityID: entityID || entities[0].id,
    detailID,
  };
}

function legacyEntityID(search) {
  const params = new URLSearchParams(search);
  return resolveEntityID(String(params.get("entity") || params.get("type") || "").toLowerCase()) || entities[0].id;
}

function resolveEntityID(raw) {
  const entityID = String(raw || "").toLowerCase();
  const aliases = {
    "cli-runs": "pipelines",
  };
  const normalized = aliases[entityID] || entityID;
  return findEntity(normalized) ? normalized : "";
}

function navigateTo(nextPath, replace = false) {
  const url = new URL(nextPath, window.location.origin);
  const route = parseRoute(url.pathname, url.search);
  state.entityID = route.entityID;
  state.detailID = route.detailID;
  state.sessionFilterOpen = false;
  state.sessionFilterQuery = "";
  state.importTraceOpen = false;
  syncWorkspaceFilterFromRoute();
  syncSessionFilterFromRoute();
  const nextURL = `${url.pathname}${url.search}`;
  const currentURL = `${window.location.pathname}${window.location.search}`;
  if (nextURL !== currentURL) {
    const writeHistory = replace ? window.history.replaceState.bind(window.history) : window.history.pushState.bind(window.history);
    writeHistory({}, "", nextURL);
  }
  render();
  void ensureActiveEntityData();
}

function entityPath(entityID, detailID = "") {
  const base = `/${encodeURIComponent(entityID || entities[0].id)}`;
  if (!detailID) {
    return base;
  }
  return `${base}/${encodeURIComponent(detailID)}`;
}

async function ensureActiveEntityData() {
  const shellHydration = ensureShellHydration();
  if (isOverviewRoute()) {
    await ensureOverviewData();
    void shellHydration;
    return;
  }
  if (state.entityID === "sessions" && state.detailID) {
    await ensureSessionDetailData();
    void shellHydration;
    return;
  }
  if (state.entityID === "devices" && state.detailID) {
    await ensureDeviceDetailData();
    void shellHydration;
    return;
  }
  if (state.entityID === "clients" && state.detailID) {
    await ensureClientDetailData();
    void shellHydration;
    return;
  }
  if (substrateDetailEntityIDs.includes(state.entityID) && state.detailID) {
    await ensureSubstrateDetailData();
    void shellHydration;
    return;
  }
  const config = liveDomainConfigs[state.entityID];
  if (!config) {
    void shellHydration;
    return;
  }
  const scope = activeSessionFetchScope();
  await ensureInventoryDependencyData(state.entityID, scope);
  await ensureLiveDomainData(config, scope);
  void shellHydration;
}

function ensureShellHydration() {
  return Promise.allSettled([
    ensureLiveDomainData(liveDomainConfigs.sessions),
    ensureLiveDomainData(liveDomainConfigs.workspaces),
    ensureLiveDomainData(liveDomainConfigs.devices),
    ensureLiveDomainData(liveDomainConfigs.clients),
  ]);
}

async function ensureSessionDetailData() {
  await ensureLiveDomainData(liveDomainConfigs.sessions);
  const sessionEntity = materializedEntityByID("sessions");
  const sessionRow = currentDetailItem(sessionEntity);
  const scope = sessionRow?.id ? { sessionID: String(sessionRow.id || "") } : null;
  const jobs = ["sessions", ...sessionHubEntityIDs]
    .map((entityID) => liveDomainConfigs[entityID])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config, config.stateKey === "sessions" ? null : scope));
  await Promise.all(jobs);
}

async function ensureDeviceDetailData() {
  const jobs = deviceDetailEntityIDs
    .map((entityID) => liveDomainConfigs[entityID])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config));
  await Promise.all(jobs);
}

async function ensureClientDetailData() {
  await ensureLiveDomainData(liveDomainConfigs.clients, activeSessionFetchScope());
  const clientEntity = materializedEntityByID("clients");
  const clientRow = currentDetailItem(clientEntity);
  const scope = clientRow?.sessionID ? { sessionID: String(clientRow.sessionID || "") } : activeSessionFetchScope();
  const jobs = clientDetailEntityIDs
    .map((entityID) => liveDomainConfigs[entityID])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config, config.stateKey === "devices" ? null : scope));
  await Promise.all(jobs);
}

async function ensureSubstrateDetailData() {
  const scope = activeSessionFetchScope();
  const jobs = substrateDetailEntityIDs
    .map((entityID) => liveDomainConfigs[entityID])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config, scope));
  await Promise.all(jobs);
}

async function ensureOverviewData() {
  const jobs = overviewEntities()
    .map((entity) => liveDomainConfigs[entity.id])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config));
  await Promise.all(jobs);
}

async function ensureInventoryDependencyData(entityID, scope) {
  if (entityID === "objects") {
    await ensureLiveDomainData(liveDomainConfigs.calls, scope);
  }
}

async function ensureLiveDomainData(config, scope = null) {
  if (!config) {
    return;
  }
  const entry = state.live[config.stateKey];
  const normalizedScope = normalizeLiveScope(scope);
  const scopeKey = liveScopeKey(normalizedScope);
  if ((entry.status === "loading" || entry.status === "loaded") && String(entry.scopeKey || "") === scopeKey) {
    return;
  }
  entry.status = "loading";
  entry.error = "";
  entry.scopeKey = scopeKey;
  render();
  try {
    const res = await fetch(liveEndpointWithScope(config.endpoint, normalizedScope));
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    const payload = await res.json();
    if (String(entry.scopeKey || "") !== scopeKey) {
      return;
    }
    entry.items = Array.isArray(payload.items) ? payload.items : [];
    entry.status = "loaded";
    entry.error = "";
  } catch (err) {
    if (String(entry.scopeKey || "") !== scopeKey) {
      return;
    }
    entry.status = "error";
    entry.error = err instanceof Error ? err.message : String(err || "unknown error");
  }
  render();
}

function activeSessionFetchScope() {
  const sessionRow = currentSessionFilterRow();
  const sessionID = String(sessionRow?.id || "");
  if (!sessionID || state.entityID === "sessions") {
    return null;
  }
  return { sessionID };
}

function normalizeLiveScope(scope) {
  const normalized = {
    traceID: String(scope?.traceID || "").trim(),
    sessionID: String(scope?.sessionID || "").trim(),
    clientID: String(scope?.clientID || "").trim(),
    functionID: String(scope?.functionID || "").trim(),
  };
  return Object.values(normalized).some(Boolean) ? normalized : null;
}

function liveScopeKey(scope) {
  if (!scope) {
    return "";
  }
  return [scope.traceID, scope.sessionID, scope.clientID, scope.functionID].join("|");
}

function liveEndpointWithScope(endpoint, scope) {
  if (!scope) {
    return endpoint;
  }
  const url = new URL(endpoint, window.location.origin);
  if (scope.traceID) {
    url.searchParams.set("traceID", scope.traceID);
  }
  if (scope.sessionID) {
    url.searchParams.set("sessionID", scope.sessionID);
  }
  if (scope.clientID) {
    url.searchParams.set("clientID", scope.clientID);
  }
  if (scope.functionID) {
    url.searchParams.set("functionID", scope.functionID);
  }
  return `${url.pathname}?${url.searchParams.toString()}`;
}

function syncSessionFilterFromRoute() {
  if (state.entityID === "sessions" && state.detailID) {
    state.sessionFilterID = state.detailID;
  }
}

function syncWorkspaceFilterFromRoute() {
  if (state.entityID === "workspaces" && state.detailID) {
    state.workspaceFilterID = state.detailID;
  }
}

function renderWorkspaceFilter() {
  if (!els.workspaceFilter) {
    return;
  }
  const workspaces = materializedEntityByID("workspaces");
  const rows = Array.isArray(workspaces?.liveItems)
    ? workspaces.liveItems.slice().sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0))
    : [];
  const selected = currentWorkspaceFilterID();
  const status = state.live.workspaces.status;
  const options = [];
  const allLabel = status === "error" ? "Workspaces unavailable" : rows.length === 0 ? "No workspaces" : "Workspace";
  options.push(`<option value="">${escapeHTML(allLabel)}</option>`);
  for (const row of rows) {
    options.push(`<option value="${escapeHTML(row.routeID)}">${escapeHTML(workspaceFilterOptionLabel(row))}</option>`);
  }
  els.workspaceFilter.innerHTML = options.join("");
  els.workspaceFilter.value = rows.some((row) => row.routeID === selected) ? selected : "";
  els.workspaceFilter.disabled = status !== "loaded" || rows.length === 0;
}

function renderImportTraceControl() {
  if (!els.importTraceShell) {
    return;
  }
  const triggerLabel = state.importTraceStatus === "loading" ? "Importing..." : "Import Trace";
  const notice =
    state.importTraceStatus === "error"
      ? `<div class="v3-import-note is-error">${escapeHTML(state.importTraceMessage || "Import failed.")}</div>`
      : state.importTraceStatus === "success"
        ? `<div class="v3-import-note is-success">Imported ${escapeHTML(shortID(state.importTraceImportedID || state.importTraceTraceID || ""))}. <a class="v3-inline-link" href="/" data-route-path="/">Overview</a></div>`
        : "";
  els.importTraceShell.innerHTML = `
    <div class="v3-import-trace${state.importTraceOpen ? " is-open" : ""}">
      <button
        id="importTraceTrigger"
        class="v3-import-trigger"
        type="button"
        aria-haspopup="dialog"
        aria-expanded="${state.importTraceOpen ? "true" : "false"}"
      >
        <span class="v3-import-trigger-plus" aria-hidden="true">+</span>
        <span>${escapeHTML(triggerLabel)}</span>
      </button>
      <div class="v3-import-popover${state.importTraceOpen ? " is-open" : ""}">
        <form id="importTraceForm" class="v3-import-form">
          <label class="v3-import-field">
            <span class="v3-import-label">Trace ID</span>
            <input
              id="importTraceIDInput"
              class="v3-import-input"
              type="text"
              name="traceID"
              placeholder="32-char trace id"
              value="${escapeHTML(state.importTraceTraceID)}"
              autocomplete="off"
              spellcheck="false"
            />
          </label>
          <label class="v3-import-field">
            <span class="v3-import-label">Org</span>
            <input
              id="importTraceOrgInput"
              class="v3-import-input"
              type="text"
              name="org"
              placeholder="Optional"
              value="${escapeHTML(state.importTraceOrg)}"
              autocomplete="off"
              spellcheck="false"
            />
          </label>
          <div class="v3-import-actions">
            <button id="importTraceSubmit" class="v3-import-submit" type="submit"${state.importTraceStatus === "loading" ? " disabled" : ""}>Import</button>
          </div>
          ${notice}
        </form>
      </div>
    </div>
  `;

  const trigger = document.getElementById("importTraceTrigger");
  trigger?.addEventListener("click", () => {
    state.importTraceOpen = !state.importTraceOpen;
    state.sessionFilterOpen = false;
    state.sessionFilterQuery = "";
    render();
    if (state.importTraceOpen) {
      const input = document.getElementById("importTraceIDInput");
      input?.focus();
      input?.select();
    }
  });

  const idInput = document.getElementById("importTraceIDInput");
  idInput?.addEventListener("input", () => {
    state.importTraceTraceID = idInput.value || "";
    if (state.importTraceStatus !== "loading") {
      state.importTraceStatus = "idle";
      state.importTraceMessage = "";
      state.importTraceImportedID = "";
    }
  });

  const orgInput = document.getElementById("importTraceOrgInput");
  orgInput?.addEventListener("input", () => {
    state.importTraceOrg = orgInput.value || "";
    if (state.importTraceStatus !== "loading") {
      state.importTraceStatus = "idle";
      state.importTraceMessage = "";
      state.importTraceImportedID = "";
    }
  });

  const form = document.getElementById("importTraceForm");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitImportTrace();
  });
}

function renderSessionFilter() {
  if (!els.sessionFilterShell) {
    return;
  }
  const rows = availableSessionRows();
  const selected = currentSessionFilterRow();
  const status = state.live.sessions.status;
  const query = String(state.sessionFilterQuery || "").trim().toLowerCase();
  const matches = rows.filter((row) => sessionFilterMatches(row, query)).slice(0, 24);
  const label =
    status === "error"
      ? "Sessions unavailable"
      : selected
        ? sessionFilterOptionLabel(selected)
        : "All Sessions";
  const emptyCopy =
    status === "error"
      ? escapeHTML(state.live.sessions.error || "Sessions unavailable")
      : status !== "loaded"
        ? "Loading sessions..."
        : query
          ? "No matching sessions"
          : "No sessions in scope";

  els.sessionFilterShell.innerHTML = `
    <div class="v3-session-filter${state.sessionFilterOpen ? " is-open" : ""}">
      <button
        id="sessionFilterTrigger"
        class="v3-session-trigger"
        type="button"
        aria-haspopup="listbox"
        aria-expanded="${state.sessionFilterOpen ? "true" : "false"}"
      >
        <span class="v3-session-trigger-label">${escapeHTML(label)}</span>
      </button>
      <div class="v3-session-popover${state.sessionFilterOpen ? " is-open" : ""}">
        <div class="v3-session-search">
          <input
            id="sessionFilterInput"
            class="v3-session-input"
            type="search"
            placeholder="Filter sessions"
            value="${escapeHTML(state.sessionFilterQuery || "")}"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
        <div class="v3-session-options" role="listbox" aria-label="Sessions">
          <button class="v3-session-option${selected ? "" : " is-selected"}" type="button" data-session-filter="">
            <span class="v3-session-option-main">All Sessions</span>
          </button>
          ${
            matches.length
              ? matches
                  .map((row) => {
                    const isSelected = selected && row.routeID === selected.routeID;
                    return `
                      <button class="v3-session-option${isSelected ? " is-selected" : ""}" type="button" data-session-filter="${escapeHTML(row.routeID)}">
                        <span class="v3-session-option-main">${escapeHTML(sessionFilterOptionLabel(row))}</span>
                        <span class="v3-session-option-meta">${escapeHTML(sessionFilterOptionMeta(row))}</span>
                        <span class="v3-session-option-orb">${sessionStatusOrb(sessionStatusLabel(row))}</span>
                      </button>
                    `;
                  })
                  .join("")
              : `<div class="v3-session-empty">${emptyCopy}</div>`
          }
        </div>
      </div>
    </div>
  `;

  const trigger = document.getElementById("sessionFilterTrigger");
  trigger?.addEventListener("click", () => {
    state.sessionFilterOpen = !state.sessionFilterOpen;
    state.sessionFilterQuery = "";
    state.importTraceOpen = false;
    render();
  });

  const input = document.getElementById("sessionFilterInput");
  input?.addEventListener("input", () => {
    state.sessionFilterQuery = input.value || "";
    renderSessionFilter();
  });
  input?.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      state.sessionFilterOpen = false;
      state.sessionFilterQuery = "";
      render();
    }
  });

  for (const node of els.sessionFilterShell.querySelectorAll("[data-session-filter]")) {
    node.addEventListener("click", () => {
      state.sessionFilterID = String(node.getAttribute("data-session-filter") || "");
      state.sessionFilterOpen = false;
      state.sessionFilterQuery = "";
      render();
      void ensureActiveEntityData();
    });
  }

  if (state.sessionFilterOpen && input && document.activeElement !== input) {
    queueMicrotask(() => {
      input.focus();
      input.select();
    });
  }
}

function resetLiveData() {
  for (const entry of Object.values(state.live)) {
    entry.status = "idle";
    entry.items = [];
    entry.error = "";
    entry.scopeKey = "";
  }
  state.detailGraphs.pipelines = {};
}

async function submitImportTrace() {
  const traceID = String(state.importTraceTraceID || "").trim();
  if (!traceID || state.importTraceStatus === "loading") {
    return;
  }
  state.importTraceStatus = "loading";
  state.importTraceMessage = "";
  render();
  try {
    const body = {
      traceID,
      ...(String(state.importTraceOrg || "").trim() ? { org: String(state.importTraceOrg || "").trim() } : {}),
    };
    const res = await fetch("/api/traces/open", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const message = (await res.text()).trim();
      throw new Error(message || `HTTP ${res.status}`);
    }
    const payload = await res.json();
    state.importTraceStatus = "success";
    state.importTraceMessage = "";
    state.importTraceImportedID = String(payload?.trace?.traceID || traceID);
    state.workspaceFilterID = "";
    state.sessionFilterID = "";
    state.sessionFilterQuery = "";
    resetLiveData();
    render();
    void ensureActiveEntityData();
  } catch (err) {
    state.importTraceStatus = "error";
    state.importTraceMessage = err instanceof Error ? err.message : String(err || "Import failed.");
    render();
  }
}

function renderBreadcrumb(entity = null, detailItem = null) {
  if (!els.pageCrumb) {
    return;
  }
  if (isOverviewRoute()) {
    els.pageCrumb.innerHTML = `<span class="v3-breadcrumb-current">Overview</span>`;
    return;
  }
  const current = entity || currentEntity();
  if (!current) {
    els.pageCrumb.textContent = "";
    return;
  }
  const resolvedDetail = detailItem || (state.detailID && supportsDetailRoute(current.id) ? currentDetailItem(current) : null);
  if (!resolvedDetail) {
    els.pageCrumb.innerHTML = `<span class="v3-breadcrumb-current">${escapeHTML(current.label)}</span>`;
    return;
  }
  const basePath = entityPath(current.id);
  els.pageCrumb.innerHTML = [
    `<a class="v3-breadcrumb-link" href="${escapeHTML(basePath)}" data-route-path="${escapeHTML(basePath)}">${escapeHTML(current.label)}</a>`,
    `<span class="v3-breadcrumb-sep">/</span>`,
    `<span class="v3-breadcrumb-current">${escapeHTML(resolvedDetail.name)}</span>`,
  ].join("");
}

function pipelineGraphEntry(row) {
  const key = String(row?.id || row?.routeID || "");
  if (!key) {
    return {
      status: "error",
      data: null,
      error: "Pipeline graph key is missing.",
    };
  }
  if (!state.detailGraphs.pipelines[key]) {
    state.detailGraphs.pipelines[key] = {
      status: "idle",
      data: null,
      error: "",
    };
  }
  return state.detailGraphs.pipelines[key];
}

function ensurePipelineGraph(row) {
  const entry = pipelineGraphEntry(row);
  if (entry.status !== "idle") {
    return entry;
  }
  entry.status = "loading";
  entry.error = "";
  render();
  void fetchPipelineGraph(row, entry);
  return entry;
}

async function fetchPipelineGraph(row, entry) {
  if (!row?.traceID) {
    entry.status = "error";
    entry.error = "Trace context is missing for this pipeline.";
    render();
    return;
  }
  const callID = pipelineTerminalCallID(row);
  if (!callID) {
    entry.status = "error";
    entry.error = "Terminal call is missing for this pipeline.";
    render();
    return;
  }
  try {
    const params = new URLSearchParams({
      traceID: row.traceID,
      callID,
    });
    const res = await fetch(`/api/pipelines/object-dag?${params.toString()}`);
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    entry.data = await res.json();
    entry.status = "loaded";
    entry.error = "";
  } catch (err) {
    entry.status = "error";
    entry.error = err instanceof Error ? err.message : String(err || "unknown error");
  }
  render();
}

function functionCallEntry(row) {
  const key = String(row?.id || row?.routeID || "");
  if (!key) {
    return {
      status: "error",
      items: [],
      error: "Function key is missing.",
    };
  }
  if (!state.detailGraphs.functionCalls[key]) {
    state.detailGraphs.functionCalls[key] = {
      status: "idle",
      items: [],
      error: "",
    };
  }
  return state.detailGraphs.functionCalls[key];
}

function ensureFunctionCallEntry(row) {
  const entry = functionCallEntry(row);
  const expected = Array.isArray(row?.callIDs) ? row.callIDs.filter(Boolean) : [];
  if (expected.length === 0 && Number(row?.callCount || 0) === 0) {
    entry.status = "loaded";
    entry.items = [];
    entry.error = "";
    return entry;
  }
  const present = new Set(functionCallRows(row, entry).map((call) => String(call?.routeID || call?.id || "")));
  const missing = expected.filter((id) => !present.has(String(id || "")));
  if (missing.length === 0 && (expected.length > 0 || entry.status === "loaded")) {
    entry.status = "loaded";
    entry.error = "";
    return entry;
  }
  if (entry.status !== "idle") {
    return entry;
  }
  entry.status = "loading";
  entry.error = "";
  render();
  void fetchFunctionCalls(row, entry);
  return entry;
}

async function fetchFunctionCalls(row, entry) {
  const functionID = String(row?.id || "");
  if (!functionID) {
    entry.status = "error";
    entry.error = "Function identity is missing.";
    render();
    return;
  }
  try {
    const items = [];
    let cursor = "";
    do {
      const params = new URLSearchParams({
        functionID,
        limit: "2000",
      });
      if (cursor) {
        params.set("cursor", cursor);
      }
      const res = await fetch(`/api/v2/calls?${params.toString()}`);
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const payload = await res.json();
      if (Array.isArray(payload?.items)) {
        items.push(...payload.items);
      }
      cursor = String(payload?.nextCursor || "").trim();
    } while (cursor && items.length < Number(row?.callCount || 0));
    entry.items = items.map((item) => ({
      ...item,
      routeID: String(item?.routeID || item?.id || ""),
    }));
    entry.status = "loaded";
    entry.error = "";
  } catch (err) {
    entry.status = "error";
    entry.error = err instanceof Error ? err.message : String(err || "unknown error");
  }
  render();
}

function clientCallEntry(row) {
  const key = String(row?.id || row?.routeID || "");
  if (!key) {
    return {
      status: "error",
      items: [],
      error: "Client key is missing.",
    };
  }
  if (!state.detailGraphs.clientCalls[key]) {
    state.detailGraphs.clientCalls[key] = {
      status: "idle",
      items: [],
      error: "",
    };
  }
  return state.detailGraphs.clientCalls[key];
}

function ensureClientCallEntry(row) {
  const entry = clientCallEntry(row);
  if (Number(row?.callCount || 0) === 0) {
    entry.status = "loaded";
    entry.items = [];
    entry.error = "";
    return entry;
  }
  const present = clientCallRows(row, entry).length;
  if (present >= Number(row?.callCount || 0)) {
    entry.status = "loaded";
    entry.error = "";
    return entry;
  }
  if (entry.status !== "idle") {
    return entry;
  }
  entry.status = "loading";
  entry.error = "";
  render();
  void fetchClientCalls(row, entry);
  return entry;
}

async function fetchClientCalls(row, entry) {
  const clientID = String(row?.id || "");
  if (!clientID) {
    entry.status = "error";
    entry.error = "Client identity is missing.";
    render();
    return;
  }
  try {
    const items = [];
    let cursor = "";
    do {
      const params = new URLSearchParams({
        clientID,
        limit: "2000",
      });
      if (cursor) {
        params.set("cursor", cursor);
      }
      const res = await fetch(`/api/v2/calls?${params.toString()}`);
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const payload = await res.json();
      if (Array.isArray(payload?.items)) {
        items.push(...payload.items);
      }
      cursor = String(payload?.nextCursor || "").trim();
    } while (cursor && items.length < Number(row?.callCount || 0));
    entry.items = items;
    entry.status = "loaded";
    entry.error = "";
  } catch (err) {
    entry.status = "error";
    entry.error = err instanceof Error ? err.message : String(err || "unknown error");
  }
  render();
}

function render() {
  autoTableCounter = 0;
  sanitizeSessionFilterSelection();
  renderWorkspaceFilter();
  renderImportTraceControl();
  renderSessionFilter();
  renderEntityNav();
  renderMain();
}

function renderEntityNav() {
  const navEntityID = currentNavEntityID();
  els.sidebarBrand.classList.toggle("is-active", isOverviewRoute());

  els.entityNav.innerHTML = entities
    .filter((entity) => !entity.navHidden)
    .map((entity) => {
      const active = entity.id === navEntityID;
      const mockBadge = isMockEntity(entity)
        ? `<span class="v3-type-badge" aria-label="Mock domain">mock</span>`
        : "";
      return `
        <button
          class="v3-type-item${active ? " is-active" : ""}"
          data-entity-id="${escapeHTML(entity.id)}"
          data-route-path="${escapeHTML(entityPath(entity.id))}"
          type="button"
        >
          <span class="v3-type-icon" aria-hidden="true">${navIcon(entity.id)}</span>
          <span class="v3-type-title">${escapeHTML(entity.label)}</span>
          ${mockBadge}
        </button>
      `;
    })
    .join("");

  for (const node of els.entityNav.querySelectorAll("[data-entity-id]")) {
    node.addEventListener("click", () => {
      navigateTo(node.getAttribute("data-route-path") || entityPath(entities[0].id));
    });
  }
}

function isMockEntity(entity) {
  return !liveDomainConfigs[entity.id];
}

function renderMain() {
  setPanelHeadHidden(true);
  if (isOverviewRoute()) {
    renderBreadcrumb();
    renderOverview();
    return;
  }
  const entity = currentEntity();
  const detailItem = state.detailID && supportsDetailRoute(entity.id) ? currentDetailItem(entity) : null;
  renderBreadcrumb(entity, detailItem);

  if (state.detailID && supportsDetailRoute(entity.id)) {
    renderDetail(entity);
    return;
  }

  const model = tableModel(entity, "inventory");
  model.tableID = `${entity.dynamicKind || entity.id}:inventory`;
  model.filterPlaceholder = `Filter ${String(entity.label || "rows").toLowerCase()}`;
  document.title = `ODAG ${entity.label}`;
  els.tableTitle.textContent = entity.label;
  els.tableMeta.textContent = model.meta;
  renderTable(model);
}

function renderOverview() {
  const cards = overviewCards();
  const loadedDomains = cards.filter((card) => card.state === "loaded").length;
  const loadingDomains = cards.filter((card) => card.state === "loading" || card.state === "idle").length;
  const errorDomains = cards.filter((card) => card.state === "error").length;
  const totalEntities = cards.reduce((sum, card) => sum + card.count, 0);

  document.title = "ODAG Overview";
  els.tableTitle.textContent = "Live Domains";
  els.tableMeta.textContent = `${totalEntities} entities across ${cards.length} domains`;
  els.tableShell.innerHTML = renderOverviewHTML({
    cards,
    totalDomains: cards.length,
    loadedDomains,
    loadingDomains,
    errorDomains,
    totalEntities,
  });
}

function renderTable(model) {
  const interactive = interactiveTableModel(model);
  els.tableShell.innerHTML = renderInteractiveTableHTML(interactive);
  bindTableControls();
}

function renderTableHTML(model) {
  const interactive = model?.tableState ? model : interactiveTableModel(model);
  const head = interactive.columns.map((column) => `<th>${column.headerHTML || escapeHTML(column.label)}</th>`).join("");
  const body = interactive.rows.length
    ? interactive.rows
        .map((row) => {
          const cells = interactive.columns
            .map((column) => {
              const value =
                typeof column.render === "function"
                  ? column.render(row)
                  : escapeHTML(row[column.key] == null ? "" : row[column.key]);
              return `<td>${value}</td>`;
            })
            .join("");
          return `<tr>${cells}</tr>`;
        })
        .join("")
    : `<tr><td colspan="${interactive.columns.length}">${escapeHTML(interactive.emptyMessage || "No rows yet.")}</td></tr>`;

  return `
    <div class="v3-data-table" data-table-id="${escapeHTML(interactive.tableID || "")}">
      <table class="v3-table">
        <thead>
          <tr>${head}</tr>
        </thead>
        <tbody>${body}</tbody>
      </table>
    </div>
  `;
}

function interactiveTableModel(model) {
  const tableID = String(model?.tableID || nextAutoTableID(model));
  const tableState = tableControlsState(tableID);
  const columns = Array.isArray(model?.columns)
    ? model.columns.map((column, index) => ({
        ...column,
        columnID: tableColumnID(column, index),
      }))
    : [];
  const sourceRows = Array.isArray(model?.rows) ? model.rows.slice() : [];
  const filterStateByColumn = {};
  const facetModelByColumn = {};
  tableState.facetMeta = {};
  for (const column of columns) {
    filterStateByColumn[column.columnID] = tableColumnFilterState(tableState, column.columnID);
    facetModelByColumn[column.columnID] = tableColumnFacetModel(column, sourceRows, filterStateByColumn[column.columnID]);
    tableState.facetMeta[column.columnID] = {
      allValues: facetModelByColumn[column.columnID].allOptions.map((option) => option.value),
    };
  }
  const query = String(tableState.query || "").trim().toLowerCase();
  let rows = sourceRows;
  if (query) {
    rows = rows.filter((row) => tableRowMatchesQuery(row, columns, query));
  }
  rows = rows.filter((row) => tableRowMatchesColumnFilters(row, columns, filterStateByColumn, facetModelByColumn));
  const sortColumn = columns.find((column) => column.columnID === tableState.sortColumnID && column.sortable !== false) || null;
  if (sortColumn) {
    const direction = tableState.sortDir === "desc" ? "desc" : "asc";
    rows = stableSortedRows(rows, (left, right) => compareTableValues(tableColumnSortValue(sortColumn, left), tableColumnSortValue(sortColumn, right), direction));
  }
  const decoratedColumns = columns.map((column) => ({
    ...column,
    headerHTML: interactiveTableHeaderHTML(tableID, column, tableState, filterStateByColumn[column.columnID], facetModelByColumn[column.columnID]),
  }));
  return {
    ...model,
    tableID,
    columns: decoratedColumns,
    rows,
    totalRows: sourceRows.length,
    filteredRows: rows.length,
    tableState,
  };
}

function renderInteractiveTableHTML(model) {
  const query = String(model?.tableState?.query || "");
  const hasQuery = query.trim().length > 0;
  const activeFilterCount = tableActiveFilterCount(model?.tableState);
  const resultLabel = hasQuery || activeFilterCount > 0 ? `${model.filteredRows} of ${model.totalRows}` : String(model.filteredRows);
  const clearButton = hasQuery || activeFilterCount > 0
    ? `<button class="v3-table-clear" type="button" data-table-filter-clear data-table-id="${escapeHTML(model.tableID || "")}">Clear Filters</button>`
    : "";
  const filterSummary = activeFilterCount > 0 ? ` · ${activeFilterCount} column filter${activeFilterCount === 1 ? "" : "s"}` : "";
  return `
    <div class="v3-table-toolbar">
      <label class="v3-table-search">
        <span class="v3-table-search-label">Filter</span>
        <input
          class="v3-table-search-input"
          type="search"
          value="${escapeHTML(query)}"
          placeholder="${escapeHTML(model.filterPlaceholder || "Filter rows")}"
          data-table-filter-input
          data-table-id="${escapeHTML(model.tableID || "")}"
          autocomplete="off"
          spellcheck="false"
        />
      </label>
      <div class="v3-table-toolbar-side">
        <span class="v3-table-summary">${escapeHTML(resultLabel)} rows${escapeHTML(filterSummary)}</span>
        ${clearButton}
      </div>
    </div>
    ${renderTableHTML(model)}
  `;
}

function bindTableControls() {
  for (const input of els.tableShell?.querySelectorAll("[data-table-filter-input]") || []) {
    input.addEventListener("input", () => {
      const tableID = String(input.getAttribute("data-table-id") || "");
      if (!tableID) {
        return;
      }
      tableControlsState(tableID).query = String(input.value || "");
      render();
    });
  }
  for (const clear of els.tableShell?.querySelectorAll("[data-table-filter-clear]") || []) {
    clear.addEventListener("click", () => {
      const tableID = String(clear.getAttribute("data-table-id") || "");
      if (!tableID) {
        return;
      }
      const tableState = tableControlsState(tableID);
      tableState.query = "";
      tableState.openColumnID = "";
      tableState.columnFilters = {};
      render();
    });
  }
  for (const node of els.tableShell?.querySelectorAll("[data-table-sort]") || []) {
    node.addEventListener("click", () => {
      const tableID = String(node.getAttribute("data-table-id") || "");
      const columnID = String(node.getAttribute("data-table-sort") || "");
      if (!tableID || !columnID) {
        return;
      }
      toggleTableSort(tableID, columnID, node.getAttribute("data-table-sort-default") || "asc");
      render();
    });
  }
  for (const node of els.tableShell?.querySelectorAll("[data-table-facet]") || []) {
    node.addEventListener("toggle", () => {
      const tableID = String(node.getAttribute("data-table-id") || "");
      const columnID = String(node.getAttribute("data-column-id") || "");
      if (!tableID || !columnID) {
        return;
      }
      const tableState = tableControlsState(tableID);
      tableState.openColumnID = node.open ? columnID : "";
    });
  }
  for (const node of els.tableShell?.querySelectorAll("[data-table-facet-all]") || []) {
    node.addEventListener("click", () => {
      const tableID = String(node.getAttribute("data-table-id") || "");
      const columnID = String(node.getAttribute("data-column-id") || "");
      if (!tableID || !columnID) {
        return;
      }
      const filterState = tableColumnFilterState(tableControlsState(tableID), columnID);
      filterState.mode = "all";
      filterState.selected = [];
      render();
    });
  }
  for (const node of els.tableShell?.querySelectorAll("[data-table-facet-none]") || []) {
    node.addEventListener("click", () => {
      const tableID = String(node.getAttribute("data-table-id") || "");
      const columnID = String(node.getAttribute("data-column-id") || "");
      if (!tableID || !columnID) {
        return;
      }
      const filterState = tableColumnFilterState(tableControlsState(tableID), columnID);
      filterState.mode = "subset";
      filterState.selected = [];
      render();
    });
  }
  for (const node of els.tableShell?.querySelectorAll("[data-table-facet-option]") || []) {
    node.addEventListener("change", () => {
      const tableID = String(node.getAttribute("data-table-id") || "");
      const columnID = String(node.getAttribute("data-column-id") || "");
      if (!tableID || !columnID) {
        return;
      }
      const filterState = tableColumnFilterState(tableControlsState(tableID), columnID);
      const facetMeta = tableControlsState(tableID).facetMeta?.[columnID];
      const allValues = Array.isArray(facetMeta?.allValues) ? facetMeta.allValues.slice() : [];
      const currentValue = String(node.getAttribute("data-option-value") || "");
      const selectedValues = new Set(filterState.mode === "all" ? allValues : filterState.selected);
      if (node.checked) {
        selectedValues.add(currentValue);
      } else {
        selectedValues.delete(currentValue);
      }
      if (allValues.length && selectedValues.size === allValues.length) {
        filterState.mode = "all";
        filterState.selected = [];
      } else {
        filterState.mode = "subset";
        filterState.selected = Array.from(selectedValues);
      }
      render();
    });
  }
  for (const input of els.tableShell?.querySelectorAll("[data-table-facet-search]") || []) {
    input.addEventListener("input", () => {
      const tableID = String(input.getAttribute("data-table-id") || "");
      const columnID = String(input.getAttribute("data-column-id") || "");
      if (!tableID || !columnID) {
        return;
      }
      const tableState = tableControlsState(tableID);
      tableState.openColumnID = columnID;
      tableColumnFilterState(tableState, columnID).query = String(input.value || "");
      const start = Number(input.selectionStart || 0);
      const end = Number(input.selectionEnd || start);
      render();
      const selector = `[data-table-facet-search][data-table-id="${cssEscape(tableID)}"][data-column-id="${cssEscape(columnID)}"]`;
      const next = els.tableShell?.querySelector(selector);
      if (next) {
        next.focus();
        next.setSelectionRange(start, end);
      }
    });
  }
}

function tableControlsState(tableID) {
  const key = String(tableID || "");
  if (!state.tableControls[key]) {
    state.tableControls[key] = {
      query: "",
      sortColumnID: "",
      sortDir: "",
      openColumnID: "",
      columnFilters: {},
      facetMeta: {},
    };
  }
  return state.tableControls[key];
}

function toggleTableSort(tableID, columnID, defaultDir = "asc") {
  const tableState = tableControlsState(tableID);
  if (tableState.sortColumnID === columnID) {
    tableState.sortDir = tableState.sortDir === "asc" ? "desc" : "asc";
    return;
  }
  tableState.sortColumnID = columnID;
  tableState.sortDir = defaultDir === "desc" ? "desc" : "asc";
}

function tableColumnID(column, index) {
  if (column?.id) {
    return String(column.id);
  }
  if (column?.key) {
    return String(column.key);
  }
  return `${slugifyText(column?.label || "column")}-${index}`;
}

function nextAutoTableID(model) {
  autoTableCounter += 1;
  const scope = `${state.entityID || "table"}-${state.detailID || "list"}`;
  const label = Array.isArray(model?.columns) && model.columns.length
    ? model.columns.map((column) => column?.label || column?.key || "col").join("-")
    : model?.filterPlaceholder || model?.emptyMessage || "table";
  return `${slugifyText(scope)}-${slugifyText(label).slice(0, 48)}-${autoTableCounter}`;
}

function tableColumnFilterState(tableState, columnID) {
  if (!tableState.columnFilters || typeof tableState.columnFilters !== "object") {
    tableState.columnFilters = {};
  }
  if (!tableState.columnFilters[columnID] || typeof tableState.columnFilters[columnID] !== "object") {
    tableState.columnFilters[columnID] = {
      mode: "all",
      selected: [],
      query: "",
    };
  }
  const filterState = tableState.columnFilters[columnID];
  filterState.mode = filterState.mode === "subset" ? "subset" : "all";
  filterState.selected = Array.isArray(filterState.selected)
    ? Array.from(new Set(filterState.selected.map((value) => String(value || "")).filter(Boolean)))
    : [];
  filterState.query = String(filterState.query || "");
  return filterState;
}

function tableActiveFilterCount(tableState) {
  if (!tableState?.columnFilters || typeof tableState.columnFilters !== "object") {
    return 0;
  }
  return Object.values(tableState.columnFilters).filter((filterState) => filterState?.mode === "subset").length;
}

function interactiveTableHeaderHTML(tableID, column, tableState, filterState, facetModel) {
  return `
    <div class="v3-table-head-cell">
      ${sortableTableHeaderHTML(tableID, column, tableState)}
      ${tableFacetHeaderHTML(tableID, column, tableState, filterState, facetModel)}
    </div>
  `;
}

function tableFacetHeaderHTML(tableID, column, tableState, filterState, facetModel) {
  const active = filterState?.mode === "subset";
  const open = String(tableState?.openColumnID || "") === String(column?.columnID || "");
  const selectedCount = Array.isArray(filterState?.selected) ? filterState.selected.length : 0;
  const visibleOptions = Array.isArray(facetModel?.visibleOptions) ? facetModel.visibleOptions : [];
  const totalOptionCount = Number(facetModel?.totalOptionCount || 0);
  const facetQuery = String(filterState?.query || "");
  const searchHTML = facetModel?.searchable
    ? `
        <label class="v3-table-facet-search">
          <span class="v3-table-facet-search-label">Search values</span>
          <input
            class="v3-table-facet-search-input"
            type="search"
            value="${escapeHTML(facetQuery)}"
            placeholder="Search values"
            data-table-facet-search
            data-table-id="${escapeHTML(tableID || "")}"
            data-column-id="${escapeHTML(column?.columnID || "")}"
            autocomplete="off"
            spellcheck="false"
          />
        </label>
      `
    : "";
  const noteHTML = facetModel?.note ? `<div class="v3-table-facet-note">${escapeHTML(facetModel.note)}</div>` : "";
  const optionsHTML = visibleOptions.length
    ? visibleOptions
        .map((option) => {
          const checked = filterState?.mode === "all" || filterState?.selected?.includes(option.value);
          return `
            <label class="v3-table-facet-option">
              <input
                type="checkbox"
                data-table-facet-option
                data-table-id="${escapeHTML(tableID || "")}"
                data-column-id="${escapeHTML(column?.columnID || "")}"
                data-option-value="${escapeHTML(option.value)}"
                ${checked ? "checked" : ""}
              />
              <span class="v3-table-facet-option-label">${escapeHTML(option.label)}</span>
              <span class="v3-table-facet-option-count">${escapeHTML(String(option.count))}</span>
            </label>
          `;
        })
        .join("")
    : `<div class="v3-table-facet-empty">${escapeHTML(facetQuery ? "No matching values." : "No values in this column.")}</div>`;
  return `
    <details
      class="v3-table-facet${active ? " is-active" : ""}"
      data-table-facet
      data-table-id="${escapeHTML(tableID || "")}"
      data-column-id="${escapeHTML(column?.columnID || "")}"
      ${open ? "open" : ""}
    >
      <summary class="v3-table-facet-trigger" title="${escapeHTML(`Filter ${column?.label || "column"}`)}" aria-label="${escapeHTML(`Filter ${column?.label || "column"}`)}">
        <span class="v3-table-facet-icon" aria-hidden="true">
          <svg viewBox="0 0 16 16" focusable="false" role="presentation">
            <path d="M2.5 4h11M4.5 8h7M6.5 12h3"></path>
          </svg>
        </span>
        ${active ? `<span class="v3-table-facet-badge">${escapeHTML(String(selectedCount))}</span>` : ""}
      </summary>
      <div class="v3-table-facet-popover">
        <div class="v3-table-facet-head">
          <strong>${escapeHTML(column?.label || "Column")}</strong>
          <span>${escapeHTML(totalOptionCount === 1 ? "1 value" : `${totalOptionCount} values`)}</span>
        </div>
        <div class="v3-table-facet-actions">
          <button type="button" data-table-facet-all data-table-id="${escapeHTML(tableID || "")}" data-column-id="${escapeHTML(column?.columnID || "")}">All</button>
          <button type="button" data-table-facet-none data-table-id="${escapeHTML(tableID || "")}" data-column-id="${escapeHTML(column?.columnID || "")}">None</button>
        </div>
        ${searchHTML}
        ${noteHTML}
        <div class="v3-table-facet-options">${optionsHTML}</div>
      </div>
    </details>
  `;
}

function tableRowMatchesColumnFilters(row, columns, filterStateByColumn, facetModelByColumn) {
  for (const column of columns) {
    const filterState = filterStateByColumn?.[column.columnID];
    if (!filterState || filterState.mode !== "subset") {
      continue;
    }
    const selected = Array.isArray(filterState.selected) ? filterState.selected : [];
    if (!selected.length) {
      return false;
    }
    const facetKind = facetModelByColumn?.[column.columnID]?.kind || tableColumnFilterKind(column);
    const values = new Set(tableColumnFacetValues(column, row, facetKind));
    let matched = false;
    for (const value of selected) {
      if (values.has(value)) {
        matched = true;
        break;
      }
    }
    if (!matched) {
      return false;
    }
  }
  return true;
}

function tableColumnFacetModel(column, rows, filterState) {
  const kind = tableColumnFilterKind(column);
  const counts = new Map();
  for (const row of rows) {
    for (const entry of tableColumnFacetEntries(column, row, kind)) {
      const existing = counts.get(entry.value) || { ...entry, count: 0 };
      existing.count += 1;
      counts.set(entry.value, existing);
    }
  }
  const allOptions = Array.from(counts.values()).sort((left, right) => compareFacetOptions(left, right, kind));
  const searchable = kind === "categorical" && allOptions.length > 8;
  const query = searchable ? String(filterState?.query || "").trim().toLowerCase() : "";
  const matchingOptions = query ? allOptions.filter((option) => option.label.toLowerCase().includes(query)) : allOptions;
  const limit = searchable && !query ? 24 : 80;
  const visibleOptions = matchingOptions.slice(0, limit);
  let note = "";
  if (query && matchingOptions.length > visibleOptions.length) {
    note = `Showing ${visibleOptions.length} of ${matchingOptions.length} matching values.`;
  } else if (!query && searchable && allOptions.length > visibleOptions.length) {
    note = `Showing top ${visibleOptions.length} values by frequency.`;
  }
  return {
    kind,
    searchable,
    allOptions,
    visibleOptions,
    totalOptionCount: allOptions.length,
    note,
  };
}

function compareFacetOptions(left, right, kind) {
  if (kind !== "categorical") {
    return Number(left.order || 0) - Number(right.order || 0);
  }
  return Number(right.count || 0) - Number(left.count || 0) || left.label.localeCompare(right.label, undefined, { numeric: true, sensitivity: "base" });
}

function tableColumnFilterKind(column) {
  const explicit = String(column?.filterKind || "").trim().toLowerCase();
  if (explicit) {
    return explicit;
  }
  const label = normalizeTableLabel(column?.label);
  if (["started", "first seen", "last seen", "last activity", "updated"].includes(label)) {
    return "time";
  }
  if (label === "duration") {
    return "duration";
  }
  if (["sessions", "ops", "pipelines", "top-level clients", "traces", "refs", "snapshots", "functions", "prelude calls", "requests", "commands"].includes(label)) {
    return "count";
  }
  return "categorical";
}

function tableColumnFacetEntries(column, row, kind) {
  if (kind === "time") {
    return [timeFacetEntry(tableColumnSortValue(column, row))];
  }
  if (kind === "duration") {
    return [durationFacetEntry(row)];
  }
  if (kind === "count") {
    return [countFacetEntry(tableColumnSortValue(column, row))];
  }
  const rawValue = typeof column?.facetValue === "function" ? column.facetValue(row) : tableColumnFilterValue(column, row);
  const values = normalizeTableFacetValues(rawValue);
  if (!values.length) {
    return [{ value: "__odag_empty__", label: "Empty", order: 999 }];
  }
  return values.map((value) => ({
    value,
    label: value === "__odag_empty__" ? "Empty" : value,
    order: 0,
  }));
}

function tableColumnFacetValues(column, row, kind = tableColumnFilterKind(column)) {
  return tableColumnFacetEntries(column, row, kind).map((entry) => entry.value);
}

function timeFacetEntry(unixNano) {
  const stamp = Number(unixNano || 0);
  if (!(stamp > 0)) {
    return { value: "time:unknown", label: "Unknown", order: 5 };
  }
  const ageMs = Math.max(0, Date.now() - stamp / 1e6);
  if (ageMs < 5 * 60 * 1000) {
    return { value: "time:5m", label: "Past 5 min", order: 0 };
  }
  if (ageMs < 60 * 60 * 1000) {
    return { value: "time:1h", label: "Past hour", order: 1 };
  }
  if (ageMs < 24 * 60 * 60 * 1000) {
    return { value: "time:1d", label: "Today", order: 2 };
  }
  if (ageMs < 7 * 24 * 60 * 60 * 1000) {
    return { value: "time:7d", label: "Past 7 days", order: 3 };
  }
  return { value: "time:older", label: "Older", order: 4 };
}

function durationFacetEntry(row) {
  if (row?.status === "running" || row?.open) {
    return { value: "duration:running", label: "Running", order: 0 };
  }
  const start = numericSortToken(row?.startUnixNano, row?.firstSeenUnixNano);
  const end = numericSortToken(row?.endUnixNano, row?.lastSeenUnixNano, row?.lastActivityUnixNano);
  if (!(start > 0) || !(end > start)) {
    return { value: "duration:unknown", label: "Unknown", order: 6 };
  }
  const durationMs = (end - start) / 1e6;
  if (durationMs < 1000) {
    return { value: "duration:1s", label: "<1s", order: 1 };
  }
  if (durationMs < 10 * 1000) {
    return { value: "duration:10s", label: "<10s", order: 2 };
  }
  if (durationMs < 60 * 1000) {
    return { value: "duration:1m", label: "<1m", order: 3 };
  }
  if (durationMs < 10 * 60 * 1000) {
    return { value: "duration:10m", label: "<10m", order: 4 };
  }
  return { value: "duration:long", label: "10m+", order: 5 };
}

function countFacetEntry(value) {
  const count = Number(value);
  if (!Number.isFinite(count) || count < 0) {
    return { value: "count:unknown", label: "Unknown", order: 5 };
  }
  if (count === 0) {
    return { value: "count:0", label: "0", order: 0 };
  }
  if (count === 1) {
    return { value: "count:1", label: "1", order: 1 };
  }
  if (count <= 5) {
    return { value: "count:2-5", label: "2-5", order: 2 };
  }
  if (count <= 20) {
    return { value: "count:6-20", label: "6-20", order: 3 };
  }
  return { value: "count:21+", label: "21+", order: 4 };
}

function normalizeTableFacetValues(value, depth = 0) {
  if (value == null || depth > 2) {
    return [];
  }
  if (typeof value === "string") {
    const text = value.trim();
    return text ? [text] : [];
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return [String(value)];
  }
  if (Array.isArray(value)) {
    return Array.from(new Set(value.flatMap((item) => normalizeTableFacetValues(item, depth + 1)).filter(Boolean)));
  }
  if (typeof value === "object") {
    for (const key of ["label", "name", "title", "routeID", "id", "ref"]) {
      if (typeof value[key] === "string" && value[key].trim()) {
        return [value[key].trim()];
      }
    }
    const text = truncateText(JSON.stringify(value), 120);
    return text ? [text] : [];
  }
  return [String(value)];
}

function cssEscape(value) {
  if (globalThis.CSS && typeof globalThis.CSS.escape === "function") {
    return globalThis.CSS.escape(String(value || ""));
  }
  return String(value || "").replaceAll(/["\\\]]/g, "\\$&");
}

function sortableTableHeaderHTML(tableID, column, tableState) {
  if (column?.sortable === false) {
    return `<span class="v3-table-head-label">${escapeHTML(column?.label || "")}</span>`;
  }
  const active = String(tableState?.sortColumnID || "") === String(column?.columnID || "");
  const direction = active && tableState?.sortDir === "desc" ? "desc" : "asc";
  const glyph = active ? (direction === "desc" ? "v" : "^") : "+";
  const ariaSort = active ? (direction === "desc" ? "descending" : "ascending") : "none";
  return `
    <button
      class="v3-table-sort${active ? " is-active" : ""}"
      type="button"
      data-table-id="${escapeHTML(tableID || "")}"
      data-table-sort="${escapeHTML(column.columnID || "")}"
      data-table-sort-default="${escapeHTML(defaultTableSortDirection(column))}"
      aria-sort="${escapeHTML(ariaSort)}"
      title="${escapeHTML(`Sort by ${column.label || "column"}`)}"
    >
      <span>${escapeHTML(column.label || "")}</span>
      <span class="v3-table-sort-indicator" aria-hidden="true">${glyph}</span>
    </button>
  `;
}

function tableRowMatchesQuery(row, columns, query) {
  if (!query) {
    return true;
  }
  const parts = [];
  for (const column of columns) {
    const value = tableColumnFilterValue(column, row);
    collectFilterFragments(parts, value);
  }
  if (parts.length === 0) {
    collectFilterFragments(parts, row);
  }
  return parts.join(" ").toLowerCase().includes(query);
}

function tableColumnFilterValue(column, row) {
  if (typeof column?.filterValue === "function") {
    return column.filterValue(row);
  }
  if (column?.key) {
    return row?.[column.key];
  }
  if (typeof column?.render === "function") {
    return stripHTMLText(column.render(row));
  }
  return "";
}

function collectFilterFragments(parts, value, depth = 0) {
  if (value == null || depth > 2 || parts.length > 32) {
    return;
  }
  if (typeof value === "string") {
    const text = value.trim();
    if (text) {
      parts.push(text);
    }
    return;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    parts.push(String(value));
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value.slice(0, 12)) {
      collectFilterFragments(parts, item, depth + 1);
    }
    return;
  }
  if (typeof value === "object") {
    const entries = Object.entries(value).slice(0, 12);
    for (const [key, item] of entries) {
      parts.push(String(key));
      collectFilterFragments(parts, item, depth + 1);
    }
  }
}

function tableColumnSortValue(column, row) {
  if (typeof column?.sortValue === "function") {
    return column.sortValue(row);
  }
  const label = normalizeTableLabel(column?.label);
  switch (label) {
    case "status":
      return row?.status || row?.statusCode || sessionStatusLabel(row) || "";
    case "started":
      return numericSortToken(row?.startUnixNano, row?.firstSeenUnixNano);
    case "first seen":
      return numericSortToken(row?.firstSeenUnixNano);
    case "last seen":
    case "last activity":
    case "updated":
      return numericSortToken(row?.lastActivityUnixNano, row?.lastSeenUnixNano, row?.updatedUnixNano, row?.endUnixNano);
    case "duration":
      return rowDurationSortValue(row);
    case "sessions":
      return numericSortToken(row?.sessionCount, arrayLength(row?.sessionIDs));
    case "ops":
      return numericSortToken(row?.opCount);
    case "pipelines":
      return numericSortToken(row?.pipelineCount);
    case "top-level clients":
      return numericSortToken(row?.clientCount);
    case "traces":
      return numericSortToken(row?.traceCount, arrayLength(row?.traceIDs));
    case "refs":
      return numericSortToken(arrayLength(row?.fieldRefs));
    case "snapshots":
      return numericSortToken(row?.snapshotCount);
    case "functions":
      return numericSortToken(row?.functionCount);
    case "prelude calls":
      return numericSortToken(arrayLength(row?.callIDs));
    case "requests":
      return numericSortToken(row?.activityCount);
    case "commands":
      return numericSortToken(row?.commandCount);
    default:
      break;
  }
  if (column?.key) {
    return row?.[column.key];
  }
  if (typeof column?.render === "function") {
    return stripHTMLText(column.render(row));
  }
  return "";
}

function rowDurationSortValue(row) {
  const start = numericSortToken(row?.startUnixNano, row?.firstSeenUnixNano);
  const end = numericSortToken(row?.endUnixNano, row?.lastSeenUnixNano, row?.lastActivityUnixNano);
  if (start > 0 && end > start) {
    return end - start;
  }
  if (row?.status === "running" || row?.open) {
    return Number.MAX_SAFE_INTEGER;
  }
  return 0;
}

function numericSortToken(...values) {
  for (const value of values) {
    const num = Number(value);
    if (Number.isFinite(num)) {
      return num;
    }
  }
  return 0;
}

function defaultTableSortDirection(column) {
  const label = normalizeTableLabel(column?.label);
  if (
    [
      "status",
      "started",
      "first seen",
      "last seen",
      "last activity",
      "updated",
      "duration",
      "sessions",
      "ops",
      "pipelines",
      "top-level clients",
      "traces",
      "refs",
      "snapshots",
      "functions",
      "prelude calls",
      "requests",
      "commands",
    ].includes(label)
  ) {
    return "desc";
  }
  return "asc";
}

function normalizeTableLabel(label) {
  return String(label || "")
    .trim()
    .toLowerCase();
}

function compareTableValues(left, right, direction = "asc") {
  const leftValue = normalizeSortValue(left);
  const rightValue = normalizeSortValue(right);
  let result = 0;
  if (typeof leftValue === "number" && typeof rightValue === "number") {
    result = leftValue - rightValue;
  } else {
    result = String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true, sensitivity: "base" });
  }
  return direction === "desc" ? -result : result;
}

function normalizeSortValue(value) {
  if (value == null) {
    return "";
  }
  if (typeof value === "number") {
    return Number.isFinite(value) ? value : 0;
  }
  if (typeof value === "boolean") {
    return value ? 1 : 0;
  }
  if (typeof value === "string") {
    return value.trim().toLowerCase();
  }
  if (Array.isArray(value)) {
    return value.map((item) => normalizeSortValue(item)).join(" ");
  }
  if (typeof value === "object") {
    return JSON.stringify(value);
  }
  return String(value);
}

function stableSortedRows(rows, compare) {
  return rows
    .map((row, index) => ({ row, index }))
    .sort((left, right) => compare(left.row, right.row) || left.index - right.index)
    .map((entry) => entry.row);
}

function stripHTMLText(value) {
  const text = decodeBasicEntities(String(value || "").replaceAll(/<[^>]*>/g, " "));
  return text.replaceAll(/\s+/g, " ").trim();
}

function decodeBasicEntities(value) {
  return String(value || "")
    .replaceAll("&quot;", "\"")
    .replaceAll("&#39;", "'")
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

function slugifyText(value) {
  const text = String(value || "")
    .trim()
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, "-")
    .replaceAll(/^-+|-+$/g, "");
  return text || "column";
}

function arrayLength(value) {
  return Array.isArray(value) ? value.length : 0;
}

function renderOverviewHTML(model) {
  const loadingDetail = model.loadingDomains > 0 ? `${model.loadingDomains} loading` : "All loaded";
  const errorDetail = model.errorDomains > 0 ? `${model.errorDomains} unavailable` : "Healthy";
  const cards = model.cards
    .map((card) => {
      const statePill = overviewDomainStatePill(card);
      const items = card.items.length
        ? `<ul class="v3-overview-list">${card.items
            .map((item) => {
              const status = item.status ? `<span class="v3-overview-item-status">${statusOrb(item.status)}</span>` : "";
              const time = item.timeLabel ? `<span class="v3-overview-item-time">${escapeHTML(item.timeLabel)}</span>` : "";
              return `
                <li class="v3-overview-item">
                  <a class="v3-overview-item-link" href="${escapeHTML(item.href)}" data-route-path="${escapeHTML(item.href)}">${escapeHTML(item.label)}</a>
                  <div class="v3-overview-item-meta">
                    ${status}
                    ${time}
                  </div>
                </li>
              `;
            })
            .join("")}</ul>`
        : `<p class="v3-overview-empty">${escapeHTML(card.emptyCopy)}</p>`;
      return `
        <section class="v3-overview-card">
          <div class="v3-overview-card-head">
            <div>
              <a class="v3-overview-card-title" href="${escapeHTML(card.href)}" data-route-path="${escapeHTML(card.href)}">${escapeHTML(card.label)}</a>
              <p class="v3-overview-card-copy">${escapeHTML(card.copy)}</p>
            </div>
            <div class="v3-overview-card-side">
              <strong class="v3-overview-count">${escapeHTML(String(card.count))}</strong>
              ${statePill}
            </div>
          </div>
          ${items}
        </section>
      `;
    })
    .join("");

  return `
    <div class="v3-overview-summary">
      <article class="v3-overview-stat">
        <p class="v3-foot-label">Domains</p>
        <strong>${escapeHTML(String(model.totalDomains))}</strong>
        <span>${escapeHTML(`${model.loadedDomains} loaded`)}</span>
      </article>
      <article class="v3-overview-stat">
        <p class="v3-foot-label">Entities</p>
        <strong>${escapeHTML(String(model.totalEntities))}</strong>
        <span>${escapeHTML(loadingDetail)}</span>
      </article>
      <article class="v3-overview-stat">
        <p class="v3-foot-label">API State</p>
        <strong>${escapeHTML(errorDetail)}</strong>
        <span>${escapeHTML(model.errorDomains > 0 ? "Some domains need attention" : "All live domains available")}</span>
      </article>
    </div>
    <div class="v3-overview-grid">${cards}</div>
  `;
}

function renderDetail(entity) {
  const config = liveDomainConfigs[entity.id];
  const live = config ? state.live[config.stateKey] : null;
  const detailLabel = config?.singularLabel || entity.label;
  const detailItem = currentDetailItem(entity);
  const commitDetail = (html) => {
    els.tableShell.innerHTML = html;
    bindTableControls();
  };
  els.tableTitle.textContent = entity.dynamicKind === "sessions" && detailItem ? detailItem.name : `${detailLabel} Details`;
  setPanelHeadHidden(true);

  if (live && live.status !== "loaded") {
    document.title = `ODAG ${detailLabel}`;
    els.tableMeta.textContent = live.status === "error" ? "Unavailable" : "Loading";
    commitDetail(renderDetailState(entity, detailLabel, live.status === "error" ? "unavailable" : "loading"));
    return;
  }

  if (!detailItem) {
    document.title = `ODAG ${detailLabel}`;
    els.tableMeta.textContent = detailLabel;
    commitDetail(renderDetailState(entity, detailLabel, "missing"));
    return;
  }

  document.title = `ODAG ${detailLabel}: ${detailPageTitle(entity, detailItem)}`;
  els.tableMeta.textContent = detailPageMeta(entity, detailItem);

  if (entity.dynamicKind === "pipelines") {
    const graph = ensurePipelineGraph(detailItem);
    commitDetail(renderPipelineDetail(entity, detailItem, graph));
    return;
  }
  if (entity.dynamicKind === "repls") {
    commitDetail(renderReplDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "checks") {
    commitDetail(renderCheckDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "workspaces") {
    commitDetail(renderWorkspaceDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "devices") {
    commitDetail(renderDeviceDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "clients") {
    commitDetail(renderClientDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "calls") {
    commitDetail(renderCallDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "functions") {
    commitDetail(renderFunctionDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "objects") {
    commitDetail(renderObjectDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "object-types") {
    commitDetail(renderObjectTypeDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "modules") {
    commitDetail(renderModuleDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "workspace-ops") {
    commitDetail(renderWorkspaceOpDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "services") {
    commitDetail(renderServiceDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "terminals") {
    commitDetail(renderTerminalDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "git-remotes") {
    commitDetail(renderGitRemoteDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "registries") {
    commitDetail(renderRegistryDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "sessions") {
    commitDetail(renderSessionDetail(entity, detailItem));
    return;
  }
  if (entity.dynamicKind === "shells") {
    commitDetail(renderShellDetail(entity, detailItem));
    return;
  }

  commitDetail(renderDetailState(entity, detailLabel, "missing"));
}

function renderDetailState(entity, label, kind) {
  const copyByKind = {
    loading: `${label} data is still loading from the live API.`,
    unavailable: `${label} data could not be loaded from the live API.`,
    missing: `${label} ${state.detailID} was not found in the current result set.`,
  };
  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <div class="v3-detail-card">
        <p class="v3-foot-label">${escapeHTML(label)}</p>
        <p class="v3-detail-empty">${escapeHTML(copyByKind[kind] || "No detail available.")}</p>
      </div>
    </div>
  `;
}

function renderPipelineDetail(entity, row, graph) {
  const moduleItem = pipelineModuleRecapItem(graph?.data);
  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Command</p>
          <strong>${escapeHTML(row.command || row.name)}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(pipelineDurationLabel(row)))}
          ${pipelineRecapItem("Output Type", escapeHTML(pipelineOutputTypeLabel(row)))}
          ${pipelineRecapItem("Device", deviceSummaryForEntity("pipelines", row))}
          ${pipelineRecapItem("Session", pipelineSessionSummary(row))}
          ${pipelineRecapItem("Client", pipelineClientSummary(row))}
          ${moduleItem}
        </div>
      </section>
      <section class="v3-detail-card">
        ${renderPipelineGraph(row, graph)}
      </section>
    </div>
  `;
}

function pipelineRecapItem(label, value) {
  return `
    <div class="v3-pipeline-recap-item">
      <span class="v3-foot-label">${escapeHTML(label)}</span>
      <div class="v3-pipeline-recap-value">${value}</div>
    </div>
  `;
}

function pipelineSessionSummary(row) {
  return sessionInlineLinkByID(row?.sessionID, row?.traceID);
}

function pipelineModuleRecapItem(payload) {
  const moduleRef = payload?.module?.ref;
  if (!moduleRef) {
    return "";
  }
  return pipelineRecapItem("Module", moduleSummaryLink(moduleRef));
}

function pipelineClientSummary(row) {
  return row?.clientID ? clientLinkByID(row.clientID) : row?.rootClientID ? clientLinkByID(row.rootClientID) : "Unknown";
}

function renderPipelineGraph(row, graph) {
  const graphStatus = graph?.status || "idle";
  if (graphStatus === "loading" || graphStatus === "idle") {
    return renderPipelineGraphState(row, "loading", "Loading the scoped object DAG for this pipeline.");
  }
  if (graphStatus === "error") {
    return renderPipelineGraphState(row, "error", graph?.error || "Pipeline graph could not be loaded.");
  }
  const model = buildPipelineGraphModel(row, graph?.data);
  if (!model.nodes.length) {
    return renderPipelineGraphState(
      row,
      "empty",
      `No object DAG is available for this pipeline yet. Output type: ${pipelineOutputTypeLabel(row)}.`,
    );
  }
  return `
    <div class="v3-graph-panel">
      <div class="v3-graph-head">
        <p class="v3-graph-meta">${escapeHTML(model.meta)}</p>
      </div>
      ${model.notice ? `<p class="v3-graph-note">${escapeHTML(model.notice)}</p>` : ""}
      <div class="v3-graph-scroll">
        <div class="v3-graph-canvas" style="width:${model.width}px; height:${model.height}px;">
          <svg class="v3-graph-svg" viewBox="0 0 ${model.width} ${model.height}" aria-hidden="true" focusable="false">
            ${model.defsMarkup}
            ${model.edgeMarkup}
          </svg>
          ${model.nodeMarkup}
        </div>
      </div>
    </div>
  `;
}

function renderPipelineGraphState(row, tone, message) {
  const toneClass = tone === "error" ? " is-error" : "";
  return `
    <div class="v3-graph-panel">
      <div class="v3-graph-empty${toneClass}">
        <p>${escapeHTML(message)}</p>
      </div>
    </div>
  `;
}

function renderWorkspaceOpDetail(entity, row) {
  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Workspace op</p>
          <strong>${escapeHTML(row.name || row.callName || row.kind)}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Direction", escapeHTML(row.direction || "unknown"))}
          ${pipelineRecapItem("Call", detailCode(row.callName || "Unknown"))}
          ${pipelineRecapItem("Target", row.path ? detailCode(row.path) : "Unknown")}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)))}
          ${pipelineRecapItem("Device", deviceSummaryForEntity("workspace-ops", row))}
          ${pipelineRecapItem("Session", workspaceOpSessionSummary(row))}
          ${pipelineRecapItem("Pipeline", workspaceOpPipelineSummary(row))}
        </div>
      </section>
      <section class="v3-detail-card">
        <div class="v3-detail-list">
          ${workspaceOpDetailItem("Target", row.path ? detailCode(row.path) : "Unknown")}
          ${workspaceOpDetailItem("Kind", escapeHTML(row.kind || "unknown"))}
          ${workspaceOpDetailItem("Target Type", escapeHTML(row.targetType || "Unknown"))}
          ${workspaceOpDetailItem("Device", deviceSummaryForEntity("workspace-ops", row))}
          ${workspaceOpDetailItem("Receiver", row.receiverDagqlID ? objectSummaryLink(row.receiverDagqlID) : "None")}
          ${workspaceOpDetailItem("Output", row.outputDagqlID ? objectSummaryLink(row.outputDagqlID) : "None")}
          ${workspaceOpDetailItem("Root Client", row.rootClientID ? clientLinkByID(row.rootClientID) : "Unknown")}
          ${workspaceOpDetailItem("Client", row.clientID ? clientLinkByID(row.clientID) : "Unknown")}
        </div>
      </section>
    </div>
  `;
}

function renderGitRemoteDetail(entity, row) {
  const pipelineTable = renderTableHTML({
    columns: [
      { label: "Pipeline", render: (item) => gitRemotePipelineSummaryCell(item) },
      { label: "Session", render: (item) => gitRemotePipelineSessionCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: row.pipelines || [],
    emptyMessage: "No attached pipelines yet.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Git remote</p>
          <strong>${escapeHTML(row.ref || row.name || "Unknown")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Host", row.host ? detailCode(row.host) : "Unknown")}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Latest Ref", row.latestResolvedRef ? detailCode(row.latestResolvedRef) : detailCode(row.ref || "Unknown"))}
          ${pipelineRecapItem("Pipelines", escapeHTML(String(row.pipelineCount || 0)))}
          ${pipelineRecapItem("Sessions", escapeHTML(String(row.sessionCount || 0)))}
          ${pipelineRecapItem("Traces", escapeHTML(String(row.traceCount || 0)))}
          ${pipelineRecapItem("Spans", escapeHTML(String(row.spanCount || 0)))}
          ${pipelineRecapItem("Sources", detailInlineList(row.sourceKinds, "Unknown"))}
        </div>
      </section>
      ${detailSection("Recent Pipelines", pipelineTable)}
    </div>
  `;
}

function renderRegistryDetail(entity, row) {
  const activityTable = renderTableHTML({
    columns: [
      { label: "Activity", render: (item) => primaryCell(item.operation || item.name, item.path || item.url || item.name) },
      { label: "Source", render: (item) => tonePill("neutral", item.sourceKind || "unknown") },
      { label: "Status", render: (item) => statusOrbCell(item.status) },
      { label: "Pipeline", render: (item) => registryActivityPipelineCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: row.activities || [],
    emptyMessage: "No registry activity recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Registry</p>
          <strong>${escapeHTML(row.ref || row.name || "Unknown")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Host", row.host ? detailCode(row.host) : "Unknown")}
          ${pipelineRecapItem("Repository", row.repository ? detailCode(row.repository) : "Unknown")}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Latest Ref", row.latestRef ? detailCode(row.latestRef) : detailCode(row.ref || "Unknown"))}
          ${pipelineRecapItem("Pipelines", escapeHTML(String(row.pipelineCount || 0)))}
          ${pipelineRecapItem("Sessions", escapeHTML(String(row.sessionCount || 0)))}
          ${pipelineRecapItem("Requests", escapeHTML(String(row.activityCount || 0)))}
          ${pipelineRecapItem("Sources", detailInlineList(row.sourceKinds, "Unknown"))}
        </div>
      </section>
      ${detailSection("Recent Activity", activityTable)}
    </div>
  `;
}

function renderServiceDetail(entity, row) {
  const definitionItems = [];
  if (row.customHostname) {
    definitionItems.push(["Hostname", detailCode(row.customHostname)]);
  }
  if (row.containerDagqlID) {
    definitionItems.push(["Container", objectSummaryLink(row.containerDagqlID)]);
  }
  if (row.tunnelUpstreamDagqlID) {
    definitionItems.push(["Tunnel Upstream", objectSummaryLink(row.tunnelUpstreamDagqlID)]);
  }
  const activityItems = [
    ["Calls", escapeHTML(String((row.activity || []).length))],
    ["Started", escapeHTML(relativeTimeFromNow(row.startUnixNano))],
    ["Last activity", escapeHTML(relativeTimeFromNow(row.lastActivityUnixNano))],
    ["Client", row.clientID ? clientLinkByID(row.clientID) : "Unknown"],
  ];
  if (row.pipelineCommand) {
    activityItems.push(["Pipeline Command", detailCode(row.pipelineCommand)]);
  }
  const activityTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => callSummaryLink(item.callID, item.name) || escapeHTML(item.name || "Call") },
      { label: "Role", render: (item) => tonePill("neutral", item.role || "activity") },
      { label: "Status", render: (item) => statusOrbCell(item.status) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: row.activity || [],
    emptyMessage: "No service activity recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Service</p>
          <strong>${escapeHTML(row.name || "Service")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Kind", tonePill("neutral", row.kind || "service"))}
          ${pipelineRecapItem("Session", serviceSessionSummary(row))}
          ${pipelineRecapItem("Last Activity", escapeHTML(relativeTimeFromNow(row.lastActivityUnixNano)))}
          ${pipelineRecapItem("Created By", row.createdByCallID ? callSummaryLink(row.createdByCallID, row.createdByCallName || "Call") : detailCode(row.createdByCallName || "Unknown"))}
          ${pipelineRecapItem("Pipeline", servicePipelineSummary(row))}
          ${pipelineRecapItem("Image", row.imageRef ? detailCode(row.imageRef) : tonePill("neutral", "Synthetic"))}
        </div>
      </section>
      <div class="v3-detail-grid">
        ${definitionItems.length > 0 ? detailCard("Definition", detailList(definitionItems)) : ""}
        ${detailCard(
          "Activity",
          detailList(activityItems),
        )}
      </div>
      ${detailSection("Logs", renderServiceLogs(row.logs || []))}
      ${detailSection("Activity", activityTable)}
    </div>
  `;
}

function renderServiceLogs(logs) {
  if (!Array.isArray(logs) || logs.length === 0) {
    return `<p class="v3-detail-empty">No service log lines observed in the service lifecycle subtree.</p>`;
  }
  return `
    <div class="v3-service-log-list">
      ${logs
        .map(
          (item) => `
            <div class="v3-service-log-line">
              <span class="v3-service-log-time">${escapeHTML(relativeTimeFromNow(item.timeUnixNano))}</span>
              <span class="v3-service-log-level">${serviceLogLevelPill(item.level)}</span>
              <span class="v3-service-log-source">${escapeHTML(item.source || item.kind || "log")}</span>
              <span class="v3-service-log-message">${escapeHTML(item.message || "")}</span>
            </div>`,
        )
        .join("")}
    </div>
  `;
}

function serviceLogLevelPill(level) {
  const normalized = String(level || "").toLowerCase();
  if (normalized === "error" || normalized === "failed") {
    return tonePill("warn", "error");
  }
  return tonePill("neutral", normalized || "info");
}

function workspaceOpDetailItem(label, value) {
  return `
    <div class="v3-pipeline-recap-item">
      <span class="v3-foot-label">${escapeHTML(label)}</span>
      <div class="v3-pipeline-recap-value">${value}</div>
    </div>
  `;
}

function buildPipelineGraphModel(row, payload) {
  const objects = Array.isArray(payload?.objects)
    ? payload.objects
        .filter((item) => item && item.dagqlID)
        .slice()
        .sort((a, b) => {
          if (Number(a.firstSeenUnixNano || 0) !== Number(b.firstSeenUnixNano || 0)) {
            return Number(a.firstSeenUnixNano || 0) - Number(b.firstSeenUnixNano || 0);
          }
          return String(a.dagqlID).localeCompare(String(b.dagqlID));
        })
    : [];
  const objectByID = new Map(objects.map((item) => [item.dagqlID, item]));
  const edges = Array.isArray(payload?.edges)
    ? payload.edges.filter(
        (edge) =>
          edge &&
          (edge.kind === "field_ref" || edge.kind === "call_chain") &&
          objectByID.has(edge.fromDagqlID) &&
          objectByID.has(edge.toDagqlID),
      )
    : [];
  const focusObjectID = payload?.context?.outputDagqlID && objectByID.has(payload.context.outputDagqlID) ? payload.context.outputDagqlID : "";
  const layout = layoutPipelineGraph(objects, edges, focusObjectID);
  const aliases = pipelineSnapshotAliases(objects);
  const nodeW = 282;
  const baseNodeH = 108;
  const fieldRowH = 28;
  const colGap = objects.length <= 2 ? 88 : 60;
  const rowGap = 22;
  const padX = 24;
  const padY = 24;
  const totalColumns = layout.columns.length || 1;
  const width = padX * 2 + totalColumns * nodeW + Math.max(0, totalColumns - 1) * colGap;
  const nodeMeta = new Map();
  for (const obj of objects) {
    const fieldPreview = pipelineNodeFieldPreview(obj);
    const previewCount = fieldPreview.items.length + (fieldPreview.hiddenCount > 0 ? 1 : 0);
    const nodeH = baseNodeH + (previewCount > 0 ? 12 + previewCount * fieldRowH : 0);
    nodeMeta.set(obj.dagqlID, {
      title: pipelineNodeTitle(obj, aliases),
      subtitle: pipelineNodeSubtitle(obj),
      eyebrow: pipelineNodeEyebrow(obj, focusObjectID),
      fieldPreview,
      nodeH,
    });
  }
  const nodePositions = new Map();
  let contentHeight = padY * 2;
  for (let colIndex = 0; colIndex < layout.columns.length; colIndex += 1) {
    const column = layout.columns[colIndex];
    let cursorY = padY;
    for (const obj of column) {
      const meta = nodeMeta.get(obj.dagqlID);
      const nodeH = Number(meta?.nodeH || baseNodeH);
      const x = padX + colIndex * (nodeW + colGap);
      const y = cursorY;
      nodePositions.set(obj.dagqlID, {
        x,
        y,
        width: nodeW,
        height: nodeH,
        centerX: x + nodeW / 2,
        centerY: y + nodeH / 2,
      });
      cursorY += nodeH + rowGap;
    }
    if (column.length > 0) {
      contentHeight = Math.max(contentHeight, cursorY - rowGap + padY);
    }
  }
  const height = Math.max(220, contentHeight);
  const nodeMarkup = layout.columns
    .map((column) =>
      column
        .map((obj) => {
          const pos = nodePositions.get(obj.dagqlID);
          const meta = nodeMeta.get(obj.dagqlID);
          if (!pos || !meta) {
            return "";
          }
          const focusClass = obj.role === "output" || obj.dagqlID === focusObjectID ? " is-output" : "";
          const placeholderClass = obj.placeholder ? " is-placeholder" : "";
          const fieldMarkup = renderPipelineNodeFields(meta.fieldPreview);
          const eyebrowMarkup = meta.eyebrow ? `<span class="v3-pipeline-node-label">${escapeHTML(meta.eyebrow)}</span>` : "";
          return `
            <article class="v3-pipeline-node${focusClass}${placeholderClass}" style="left:${pos.x}px; top:${pos.y}px; width:${nodeW}px; height:${pos.height}px;">
              ${eyebrowMarkup}
              <strong>${escapeHTML(meta.title)}</strong>
              <span class="v3-pipeline-node-subtitle">${escapeHTML(meta.subtitle)}</span>
              ${fieldMarkup}
            </article>
          `;
        })
        .join(""),
    )
    .join("");
  const edgeMarkup = edges
    .map((edge) => {
      const from = nodePositions.get(edge.fromDagqlID);
      const to = nodePositions.get(edge.toDagqlID);
      if (!from || !to) {
        return "";
      }
      const start = graphNodeBorderPoint(from, to.centerX, to.centerY);
      const end = graphNodeBorderPoint(to, from.centerX, from.centerY);
      const x1 = start.x;
      const y1 = start.y;
      const x2 = end.x;
      const y2 = end.y;
      const curve = Math.max(32, Math.abs(x2 - x1) * 0.4);
      const edgeClass = edge.kind === "call_chain" ? " is-chain" : " is-ref";
      const title = edge.label ? `${edge.kind}: ${edge.label}` : edge.kind;
      const markerID = edge.kind === "call_chain" ? "v3-graph-arrow-chain" : "v3-graph-arrow-ref";
      return `<path class="v3-graph-edge${edgeClass}" marker-end="url(#${markerID})" d="M ${x1} ${y1} C ${x1 + curve} ${y1}, ${x2 - curve} ${y2}, ${x2} ${y2}"><title>${escapeHTML(title)}</title></path>`;
    })
    .join("");
  const chainCount = edges.filter((edge) => edge.kind === "call_chain").length;
  const refCount = edges.filter((edge) => edge.kind === "field_ref").length;
  const statefulNodeCount = objects.filter((obj) => obj?.outputState && typeof obj.outputState === "object").length;
  const fieldfulNodeCount = objects.filter((obj) => {
    const fields = obj?.outputState?.fields;
    return fields && typeof fields === "object" && Object.keys(fields).length > 0;
  }).length;
  const meta = [
    `${objects.length} object${objects.length === 1 ? "" : "s"}`,
    chainCount ? `${chainCount} chain step${chainCount === 1 ? "" : "s"}` : "",
    refCount ? `${refCount} ref${refCount === 1 ? "" : "s"}` : "",
    focusObjectID ? "output node highlighted" : `${pipelineOutputTypeLabel(row)} output`,
  ]
    .filter(Boolean)
    .join(" · ");
  let notice = "";
  if (objects.length > 0 && fieldfulNodeCount === 0) {
    if (statefulNodeCount === 0) {
      notice = "This pipeline recorded object identities and links, but not output-state payloads, so no fields are available to render.";
    } else {
      notice = "This pipeline captured object state, but none of these nodes expose field values.";
    }
  }

  return {
    nodes: objects,
    edges,
    width,
    height,
    meta,
    notice,
    defsMarkup: pipelineGraphDefs(),
    edgeMarkup,
    nodeMarkup,
  };
}

function pipelineGraphDefs() {
  return `
    <defs>
      <marker id="v3-graph-arrow-chain" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto" markerUnits="userSpaceOnUse">
        <path d="M 0 0 L 10 5 L 0 10 z" fill="rgba(106, 188, 255, 0.9)"></path>
      </marker>
      <marker id="v3-graph-arrow-ref" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto" markerUnits="userSpaceOnUse">
        <path d="M 0 0 L 10 5 L 0 10 z" fill="rgba(124, 146, 182, 0.82)"></path>
      </marker>
    </defs>
  `;
}

function graphNodeBorderPoint(node, targetX, targetY) {
  const halfW = Number(node?.width || 0) / 2;
  const halfH = Number(node?.height || 0) / 2;
  const centerX = Number(node?.centerX || 0);
  const centerY = Number(node?.centerY || 0);
  const dx = Number(targetX || 0) - centerX;
  const dy = Number(targetY || 0) - centerY;
  if ((!halfW && !halfH) || (!dx && !dy)) {
    return { x: centerX, y: centerY };
  }
  const scale = 1 / Math.max(Math.abs(dx) / Math.max(halfW, 1), Math.abs(dy) / Math.max(halfH, 1));
  return {
    x: centerX + dx * scale,
    y: centerY + dy * scale,
  };
}

function layoutPipelineGraph(objects, edges, focusObjectID) {
  const objectIDs = objects.map((item) => item.dagqlID);
  const adjacency = new Map(objectIDs.map((id) => [id, new Set()]));
  for (const edge of edges) {
    adjacency.get(edge.fromDagqlID)?.add(edge.toDagqlID);
    adjacency.get(edge.toDagqlID)?.add(edge.fromDagqlID);
  }
  const objectByID = new Map(objects.map((item) => [item.dagqlID, item]));
  const components = collectPipelineGraphComponents(objectIDs, adjacency);
  const sortedComponents = components.slice().sort((left, right) => {
    const leftHasFocus = left.includes(focusObjectID);
    const rightHasFocus = right.includes(focusObjectID);
    if (leftHasFocus !== rightHasFocus) {
      return leftHasFocus ? 1 : -1;
    }
    return pipelineComponentFirstSeen(left, objectByID) - pipelineComponentFirstSeen(right, objectByID);
  });

  const columns = [];
  for (const component of sortedComponents) {
    const levels = component.includes(focusObjectID)
      ? levelsFromFocus(component, adjacency, focusObjectID)
      : levelsFromSeed(component, adjacency, component[0]);
    const maxLevel = Math.max(0, ...levels.values());
    const localColumns = Array.from({ length: maxLevel + 1 }, () => []);
    const orderedComponent = component
      .map((id) => objectByID.get(id))
      .filter(Boolean)
      .sort((a, b) => {
        const levelDiff = Number(levels.get(a.dagqlID) || 0) - Number(levels.get(b.dagqlID) || 0);
        if (levelDiff !== 0) {
          return levelDiff;
        }
        if (Number(a.firstSeenUnixNano || 0) !== Number(b.firstSeenUnixNano || 0)) {
          return Number(a.firstSeenUnixNano || 0) - Number(b.firstSeenUnixNano || 0);
        }
        return String(a.dagqlID).localeCompare(String(b.dagqlID));
      });
    for (const obj of orderedComponent) {
      const level = Number(levels.get(obj.dagqlID) || 0);
      localColumns[level].push(obj);
    }
    if (columns.length > 0) {
      columns.push([]);
    }
    columns.push(...localColumns);
  }

  return {
    columns: columns.length ? columns : [objects],
  };
}

function collectPipelineGraphComponents(objectIDs, adjacency) {
  const seen = new Set();
  const components = [];
  for (const startID of objectIDs) {
    if (seen.has(startID)) {
      continue;
    }
    const queue = [startID];
    const component = [];
    seen.add(startID);
    while (queue.length) {
      const current = queue.shift();
      component.push(current);
      for (const next of adjacency.get(current) || []) {
        if (seen.has(next)) {
          continue;
        }
        seen.add(next);
        queue.push(next);
      }
    }
    components.push(component);
  }
  return components;
}

function levelsFromFocus(component, adjacency, focusObjectID) {
  const distance = bfsDistances(component, adjacency, focusObjectID);
  const maxDistance = Math.max(0, ...distance.values());
  const levels = new Map();
  for (const objectID of component) {
    levels.set(objectID, maxDistance - Number(distance.get(objectID) || 0));
  }
  return levels;
}

function levelsFromSeed(component, adjacency, seedID) {
  return bfsDistances(component, adjacency, seedID);
}

function bfsDistances(component, adjacency, seedID) {
  const allowed = new Set(component);
  const distance = new Map();
  const queue = [seedID];
  distance.set(seedID, 0);
  while (queue.length) {
    const current = queue.shift();
    const currentDistance = Number(distance.get(current) || 0);
    for (const next of adjacency.get(current) || []) {
      if (!allowed.has(next) || distance.has(next)) {
        continue;
      }
      distance.set(next, currentDistance + 1);
      queue.push(next);
    }
  }
  for (const objectID of component) {
    if (!distance.has(objectID)) {
      distance.set(objectID, 0);
    }
  }
  return distance;
}

function pipelineComponentFirstSeen(component, objectByID) {
  let best = Number.POSITIVE_INFINITY;
  for (const objectID of component) {
    const ts = Number(objectByID.get(objectID)?.firstSeenUnixNano || 0);
    if (ts > 0 && ts < best) {
      best = ts;
    }
  }
  return Number.isFinite(best) ? best : 0;
}

function pipelineSnapshotAliases(objects) {
  const counters = new Map();
  const aliases = new Map();
  for (const obj of objects) {
    const typeName = obj.typeName || "Object";
    const next = Number(counters.get(typeName) || 0) + 1;
    counters.set(typeName, next);
    aliases.set(obj.dagqlID, `${typeName}#${next}`);
  }
  return aliases;
}

function pipelineNodeTitle(obj, aliases) {
  if (obj.placeholder && String(obj.dagqlID || "").startsWith("result:")) {
    return obj.typeName || "Result";
  }
  if (obj.placeholder) {
    return `${obj.typeName || "Object"} ref`;
  }
  return aliases.get(obj.dagqlID) || obj.typeName || shortID(obj.dagqlID);
}

function pipelineNodeSubtitle(obj) {
  if (obj.placeholder && String(obj.dagqlID || "").startsWith("result:")) {
    return "pipeline result";
  }
  if (obj.placeholder) {
    return `unemitted ref · ${shortID(obj.dagqlID, 18)}`;
  }
  return shortID(obj.dagqlID, 18);
}

function pipelineNodeEyebrow(obj, focusObjectID) {
  if (obj.role === "output" || obj.dagqlID === focusObjectID) {
    return "Output";
  }
  if (obj.placeholder && String(obj.dagqlID || "").startsWith("result:")) {
    return "Result";
  }
  if (obj.placeholder) {
    return "Ref";
  }
  return "";
}

function pipelineNodeFieldPreview(obj) {
  const outputState = obj?.outputState;
  if (!outputState || typeof outputState !== "object") {
    return { items: [], hiddenCount: 0 };
  }
  const fields = outputState.fields;
  if (!fields || typeof fields !== "object") {
    return { items: [], hiddenCount: 0 };
  }

  const expandedRows = previewPipelineNestedFields(fields);
  const items = expandedRows.slice();
  const rawItems = [];
  for (const [fallbackName, raw] of Object.entries(fields)) {
    if (!raw || typeof raw !== "object") {
      continue;
    }
    const name = typeof raw.name === "string" && raw.name ? raw.name : fallbackName;
    if (expandedRows.length > 0 && (name === "Fields" || name === "TypeDef")) {
      continue;
    }
    const refs = Array.isArray(raw.refs)
      ? raw.refs.map((value) => String(value || "")).filter(Boolean)
      : [];
    const value = formatPipelineNodeFieldValue(raw.value, refs, name);
    if (!name || !value) {
      continue;
    }
    rawItems.push({ name: humanizePipelineFieldLabel(name), value });
  }

  rawItems.sort((a, b) => a.name.localeCompare(b.name));
  items.push(...rawItems);
  const previewLimit = expandedRows.length > 0 ? 5 : 4;
  return {
    items: items.slice(0, previewLimit),
    hiddenCount: Math.max(0, items.length - previewLimit),
  };
}

function previewPipelineNestedFields(fields) {
  const nested = fields?.Fields?.value;
  const typedefFields = Array.isArray(fields?.TypeDef?.value?.Fields)
    ? fields.TypeDef.value.Fields.filter((item) => item && typeof item === "object")
    : [];
  const metaByKey = new Map();
  for (const field of typedefFields) {
    for (const key of [field.OriginalName, field.Name]) {
      const text = String(key || "").trim();
      if (text) {
        metaByKey.set(text, field);
      }
    }
  }

  if (nested && typeof nested === "object" && !Array.isArray(nested) && Object.keys(nested).length > 0) {
    return Object.entries(nested)
      .map(([key, value]) => {
        const meta = metaByKey.get(key);
        return {
          name: humanizePipelineFieldLabel(key),
          value: formatPipelineNestedFieldValue(value, meta),
          rank: rankPipelineNestedFieldValue(value, meta),
        };
      })
      .sort((a, b) => {
        if (a.rank !== b.rank) {
          return a.rank - b.rank;
        }
        return a.name.localeCompare(b.name);
      })
      .map(({ name, value }) => ({ name, value }));
  }

  const functions = Array.isArray(fields?.TypeDef?.value?.Functions)
    ? fields.TypeDef.value.Functions.filter((item) => item && typeof item === "object")
    : [];
  if (functions.length === 0) {
    return [];
  }

  return functions.map((fn) => ({
    name: humanizePipelineFieldLabel(fn.OriginalName || fn.Name || ""),
    value: formatPipelineFunctionValue(fn),
  }));
}

function renderPipelineNodeFields(fieldPreview) {
  const items = Array.isArray(fieldPreview?.items) ? fieldPreview.items : [];
  const hiddenCount = Number(fieldPreview?.hiddenCount || 0);
  if (!items.length && hiddenCount <= 0) {
    return "";
  }

  const rows = items
    .map(
      (field) => `
        <div class="v3-pipeline-node-field">
          <span class="v3-pipeline-node-field-name">${escapeHTML(field.name)}</span>
          <span class="v3-pipeline-node-field-value">${escapeHTML(field.value)}</span>
        </div>
      `,
    )
    .join("");
  const overflow = hiddenCount > 0 ? `<div class="v3-pipeline-node-field v3-pipeline-node-field-more">+${hiddenCount} more</div>` : "";
  return `<div class="v3-pipeline-node-fields">${rows}${overflow}</div>`;
}

function formatPipelineNodeFieldValue(value, refs, fieldName = "") {
  const refList = Array.isArray(refs) ? refs.filter(Boolean) : [];
  const semanticSummary = summarizePipelineSemanticField(fieldName, value);
  if (semanticSummary) {
    return semanticSummary;
  }
  if (value === null) {
    return summarizePipelineNodeRefs(refList) || "null";
  }
  if (value === undefined) {
    return summarizePipelineNodeRefs(refList);
  }
  if (typeof value === "string") {
    if (value === "") {
      return summarizePipelineNodeRefs(refList) || '""';
    }
    if (looksLikeDigest(value)) {
      return shortID(value, 18);
    }
    return truncateText(value, 32);
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return "[]";
    }
    return `[${value.length}]`;
  }
  if (typeof value === "object") {
    if (typeof value.error === "string" && value.error) {
      return `error: ${truncateText(value.error, 24)}`;
    }
    const keys = Object.keys(value);
    if (keys.length === 0) {
      return "{}";
    }
    return `{${truncateText(keys.join(", "), 24)}}`;
  }
  return String(value);
}

function formatPipelineNestedFieldValue(value, meta) {
  const typeLabel = formatPipelineTypeLabel(meta?.TypeDef);
  if (value === null || value === undefined) {
    return typeLabel || "unset";
  }
  if (typeof value === "boolean" || typeof value === "number") {
    return String(value);
  }
  if (typeof value === "string") {
    if (value === "") {
      return typeLabel || "empty";
    }
    if (looksLikeDigest(value)) {
      return shortID(value, 18);
    }
    if (looksLikeOpaqueBase64(value)) {
      return typeLabel || "encoded";
    }
    return truncateText(value, 24);
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return typeLabel || "[]";
    }
    return typeLabel ? `${typeLabel} [${value.length}]` : `[${value.length}]`;
  }
  if (typeof value === "object") {
    if (typeof value.error === "string" && value.error) {
      return `error: ${truncateText(value.error, 18)}`;
    }
    if (typeLabel) {
      return typeLabel;
    }
    const keys = Object.keys(value);
    if (keys.length === 0) {
      return "{}";
    }
    return `{${truncateText(keys.join(", "), 18)}}`;
  }
  return String(value);
}

function formatPipelineFunctionValue(fn) {
  const returnLabel = formatPipelineTypeLabel(fn?.ReturnType) || "void";
  const argCount = Array.isArray(fn?.Args) ? fn.Args.filter((arg) => arg && typeof arg === "object").length : 0;
  if (argCount <= 0) {
    return returnLabel;
  }
  return `${returnLabel} · ${argCount} arg${argCount === 1 ? "" : "s"}`;
}

function rankPipelineNestedFieldValue(value, meta) {
  if (typeof value === "boolean" || typeof value === "number") {
    return 0;
  }
  if (typeof value === "string") {
    if (value && !looksLikeOpaqueBase64(value)) {
      return 0;
    }
    return meta ? 1 : 2;
  }
  if (value && typeof value === "object") {
    return Object.keys(value).length > 0 ? 1 : 2;
  }
  return meta ? 2 : 3;
}

function summarizePipelineSemanticField(fieldName, value) {
  const name = String(fieldName || "");
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return "";
  }
  if (name === "Fields") {
    const count = Object.keys(value).length;
    return count > 0 ? `${count} entries` : "empty";
  }
  if (name === "TypeDef") {
    const parts = [];
    if (typeof value.Name === "string" && value.Name) {
      parts.push(value.Name);
    }
    const fieldCount = Array.isArray(value.Fields) ? value.Fields.filter((item) => item && typeof item === "object").length : 0;
    const functionCount = Array.isArray(value.Functions) ? value.Functions.filter((item) => item && typeof item === "object").length : 0;
    if (fieldCount > 0) {
      parts.push(`${fieldCount} fields`);
    }
    if (functionCount > 0) {
      parts.push(`${functionCount} fns`);
    }
    return parts.join(" · ");
  }
  if (name === "Module") {
    const parts = [];
    if (typeof value.NameField === "string" && value.NameField) {
      parts.push(value.NameField);
    } else if (typeof value.Name === "string" && value.Name) {
      parts.push(value.Name);
    }
    const dependencyCount = Array.isArray(value?.Deps?.Mods) ? value.Deps.Mods.length : 0;
    if (dependencyCount > 0) {
      parts.push(`${dependencyCount} deps`);
    }
    return parts.join(" · ");
  }
  return "";
}

function formatPipelineTypeLabel(typeDef) {
  if (!typeDef || typeof typeDef !== "object") {
    return "";
  }
  const kind = String(typeDef.Kind || typeDef.kind || "");
  const optional = typeDef.Optional ? "?" : "";
  if (kind === "LIST_KIND") {
    const elementType = formatPipelineTypeLabel(typeDef?.AsList?.Value?.ElementTypeDef);
    return elementType ? `[${elementType}]${optional}` : `list${optional}`;
  }
  if (kind === "OBJECT_KIND") {
    return `object${optional}`;
  }
  if (kind === "SCALAR_KIND") {
    const name = String(typeDef?.AsScalar?.Value?.Name || "").trim();
    return `${name || "scalar"}${optional}`;
  }
  if (kind === "ENUM_KIND") {
    const name = String(typeDef?.AsEnum?.Value?.Name || "").trim();
    return `${name || "enum"}${optional}`;
  }
  if (kind === "INPUT_KIND") {
    const name = String(typeDef?.AsInput?.Value?.Name || "").trim();
    return `${name || "input"}${optional}`;
  }
  if (kind === "INTERFACE_KIND") {
    const name = String(typeDef?.AsInterface?.Value?.Name || "").trim();
    return `${name || "interface"}${optional}`;
  }
  return optional ? `value${optional}` : "";
}

function humanizePipelineFieldLabel(name) {
  const text = String(name || "").trim();
  if (!text) {
    return "";
  }
  return text
    .replace(/[-_]+/g, " ")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/\s+/g, " ")
    .trim();
}

function looksLikeOpaqueBase64(value) {
  const text = String(value || "");
  return text.length >= 24 && /^[A-Za-z0-9+/=]+$/.test(text) && !looksLikeDigest(text);
}

function summarizePipelineNodeRefs(refs) {
  if (!refs.length) {
    return "";
  }
  if (refs.length === 1) {
    return looksLikeDigest(refs[0]) ? shortID(refs[0], 18) : truncateText(refs[0], 32);
  }
  return `${refs.length} refs`;
}

function renderSessionDetail(entity, row) {
  const cards = renderSessionDomainCards(row);
  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-session-recap">
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(sessionStatusLabel(row)))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.firstSeenUnixNano, row.lastSeenUnixNano, row.open ? "running" : row.status)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Device", deviceSummaryForEntity("sessions", row))}
          ${pipelineRecapItem("Root Client", row.rootClientID ? clientLinkByID(row.rootClientID) : "Unknown")}
        </div>
      </section>
      ${cards}
    </div>
  `;
}

function renderSessionDomainCards(sessionRow) {
  const cards = sessionHubEntityIDs
    .map((entityID) => materializedEntityByID(entityID))
    .filter(Boolean)
    .map((domain) => ({
      domain,
      items: sessionDomainItems(sessionRow, domain),
    }))
    .filter((entry) => entry.items.length > 0)
    .map(({ domain, items }) => renderSessionDomainCard(domain, items))
    .join("");

  if (!cards) {
    return "";
  }
  return `<div class="v3-session-hub-grid">${cards}</div>`;
}

function renderSessionDomainCard(entity, items) {
  return `
    <section class="v3-detail-card v3-session-hub-card">
      <div class="v3-session-hub-card-head">
        <a class="v3-session-hub-card-title" href="${escapeHTML(entityPath(entity.id))}" data-route-path="${escapeHTML(entityPath(entity.id))}">${escapeHTML(entity.label)}</a>
        <span class="v3-session-hub-card-count">${escapeHTML(String(items.length))}</span>
      </div>
      <div class="v3-session-hub-list v3-session-hub-list-${escapeHTML(entity.id)}">
        ${items.map((item) => renderSessionDomainRow(entity, item)).join("")}
      </div>
    </section>
  `;
}

function renderSessionDomainRow(entity, item) {
  const href = overviewItemHref(entity, item);
  if (entity.id === "repls") {
    return sessionHubRow(
      href,
      "v3-session-hub-row v3-session-hub-row-repls",
      `
        <span class="v3-session-hub-main">${escapeHTML(sessionReplModuleLabel(item))}</span>
        <span class="v3-session-hub-time">${escapeHTML(sessionDomainItemTime(item))}</span>
        <span class="v3-session-hub-number">${escapeHTML(String(sessionReplPipelineCount(item)))}</span>
        <span class="v3-session-hub-orb-cell">${sessionStatusOrb(item.status)}</span>
      `,
    );
  }
  if (entity.id === "pipelines") {
    return sessionHubRow(
      href,
      "v3-session-hub-row v3-session-hub-row-pipelines",
      `
        <span class="v3-session-hub-main">
          <span class="v3-session-hub-label">${escapeHTML(sessionDomainItemLabel(entity, item))}</span>
          ${sessionDomainItemSubtitle(entity, item) ? `<span class="v3-session-hub-subtle">${escapeHTML(sessionDomainItemSubtitle(entity, item))}</span>` : ""}
        </span>
        <span class="v3-session-hub-time">${escapeHTML(sessionDomainItemTime(item))}</span>
        <span class="v3-session-hub-orb-cell">${sessionStatusOrb(item.status)}</span>
      `,
    );
  }
  return sessionHubRow(
    href,
    "v3-session-hub-row",
    `
      <span class="v3-session-hub-main">
        <span class="v3-session-hub-label">${escapeHTML(sessionDomainItemLabel(entity, item))}</span>
        ${sessionDomainItemSubtitle(entity, item) ? `<span class="v3-session-hub-subtle">${escapeHTML(sessionDomainItemSubtitle(entity, item))}</span>` : ""}
      </span>
      <span class="v3-session-hub-time">${escapeHTML(sessionDomainItemTime(item))}</span>
      <span class="v3-session-hub-orb-cell">${sessionDomainItemStatus(entity, item) ? sessionStatusOrb(sessionDomainItemStatus(entity, item)) : ""}</span>
    `,
  );
}

function sessionHubRow(href, className, inner) {
  const attrs = href ? ` href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}"` : "";
  return `<a class="${className}"${attrs}>${inner}</a>`;
}

function sessionDomainItems(sessionRow, entity) {
  if (!entity || !Array.isArray(entity.liveItems)) {
    return [];
  }
  return entity.liveItems.filter((item) => sessionOwnsEntity(sessionRow, entity.id, item));
}

function availableSessionRows() {
  const sessions = materializedEntityByID("sessions");
  const rows = Array.isArray(sessions?.liveItems) ? sessions.liveItems.slice() : [];
  const workspaceRow = currentWorkspaceFilterRow();
  const visible = workspaceRow ? rows.filter((row) => workspaceOwnsEntity(workspaceRow, "sessions", row)) : rows;
  return visible.sort((a, b) => Number(b.firstSeenUnixNano || 0) - Number(a.firstSeenUnixNano || 0));
}

function sanitizeSessionFilterSelection() {
  const selected = currentSessionFilterID();
  if (!selected) {
    return;
  }
  if (state.entityID === "sessions" && state.detailID === selected) {
    return;
  }
  if (!availableSessionRows().some((row) => row.routeID === selected)) {
    state.sessionFilterID = "";
  }
}

function workspaceOwnsEntity(workspaceRow, entityID, row) {
  if (!workspaceRow || !row) {
    return false;
  }
  switch (entityID) {
    case "workspaces":
      return workspaceRow.routeID === row.routeID;
    case "devices":
      return deviceOwnsEntity(row, "workspaces", workspaceRow);
    case "sessions":
      return workspaceScopeData(workspaceRow).sessionIDs.has(String(row.id || ""));
    case "objects":
    case "functions":
    case "object-types":
    case "modules":
      return workspaceOwnsScopeArrays(workspaceRow, row);
    case "git-remotes":
      return Array.isArray(row?.pipelines) && row.pipelines.some((pipeline) => workspaceOwnsDirectRow(workspaceRow, pipeline));
    case "registries":
      return Array.isArray(row?.activities) && row.activities.some((activity) => workspaceOwnsDirectRow(workspaceRow, activity));
    default:
      return workspaceOwnsDirectRow(workspaceRow, row);
  }
}

function workspaceOwnsDirectRow(workspaceRow, row) {
  if (!workspaceRow || !row) {
    return false;
  }
  const scope = workspaceScopeData(workspaceRow);
  const pipelineIDs = [row.id, row.pipelineID]
    .map((value) => String(value || ""))
    .filter(Boolean);
  if (pipelineIDs.some((value) => scope.pipelineIDs.has(value))) {
    return true;
  }

  const clientIDs = [row.clientID, row.rootClientID, row.pipelineClientID]
    .map((value) => String(value || ""))
    .filter(Boolean);
  if (clientIDs.some((value) => scope.clientIDs.has(value))) {
    return true;
  }

  return false;
}

function workspaceScopeData(workspaceRow) {
  if (workspaceScopeCache.has(workspaceRow)) {
    return workspaceScopeCache.get(workspaceRow);
  }
  const data = {
    sessionIDs: new Set(),
    clientIDs: new Set(),
    pipelineIDs: new Set(),
    traceIDs: new Set(),
  };
  for (const sessionID of Array.isArray(workspaceRow?.sessionIDs) ? workspaceRow.sessionIDs : []) {
    const key = String(sessionID || "");
    if (key) {
      data.sessionIDs.add(key);
    }
  }
  for (const clientID of Array.isArray(workspaceRow?.clientIDs) ? workspaceRow.clientIDs : []) {
    const key = String(clientID || "");
    if (key) {
      data.clientIDs.add(key);
    }
  }
  for (const pipelineID of Array.isArray(workspaceRow?.pipelineIDs) ? workspaceRow.pipelineIDs : []) {
    const key = String(pipelineID || "");
    if (key) {
      data.pipelineIDs.add(key);
    }
  }
  for (const op of Array.isArray(workspaceRow?.ops) ? workspaceRow.ops : []) {
    const sessionID = String(op?.sessionID || "");
    const clientID = String(op?.clientID || "");
    const pipelineClientID = String(op?.pipelineClientID || "");
    const pipelineID = String(op?.pipelineID || "");
    const traceID = String(op?.traceID || "");
    if (sessionID) {
      data.sessionIDs.add(sessionID);
    }
    if (clientID) {
      data.clientIDs.add(clientID);
    }
    if (pipelineClientID) {
      data.clientIDs.add(pipelineClientID);
    }
    if (pipelineID) {
      data.pipelineIDs.add(pipelineID);
    }
    if (traceID) {
      data.traceIDs.add(traceID);
    }
  }
  workspaceScopeCache.set(workspaceRow, data);
  return data;
}

function deviceOwnedRows(deviceRow, entityID) {
  const entity = materializedEntityByID(entityID);
  if (!entity || !Array.isArray(entity.liveItems)) {
    return [];
  }
  return entity.liveItems
    .filter((item) => deviceOwnsEntity(deviceRow, entityID, item))
    .slice()
    .sort((a, b) => overviewItemUnixNano(b) - overviewItemUnixNano(a));
}

function deviceOwnsEntity(deviceRow, entityID, row) {
  if (!deviceRow || !row) {
    return false;
  }
  switch (entityID) {
    case "devices":
      return deviceRow.routeID === row.routeID;
    case "sessions":
      return deviceScopeData(deviceRow).sessionIDs.has(String(row.id || "")) || deviceOwnsDirectRow(deviceRow, row);
    case "objects":
    case "functions":
    case "object-types":
    case "modules":
      return deviceOwnsScopeArrays(deviceRow, row);
    case "workspaces":
      return Array.isArray(row?.ops) && row.ops.some((op) => deviceOwnsDirectRow(deviceRow, op));
    case "git-remotes":
      return Array.isArray(row?.pipelines) && row.pipelines.some((pipeline) => deviceOwnsDirectRow(deviceRow, pipeline));
    case "registries":
      return Array.isArray(row?.activities) && row.activities.some((activity) => deviceOwnsDirectRow(deviceRow, activity));
    default:
      return deviceOwnsDirectRow(deviceRow, row);
  }
}

function deviceOwnsDirectRow(deviceRow, row) {
  if (!deviceRow || !row) {
    return false;
  }
  const scope = deviceScopeData(deviceRow);
  if (String(row.deviceID || "") === String(deviceRow.id || "")) {
    return true;
  }
  const sessionIDs = [row.sessionID, row.pipelineSessionID]
    .map((value) => String(value || ""))
    .filter(Boolean);
  if (sessionIDs.some((value) => scope.sessionIDs.has(value))) {
    return true;
  }
  const clientIDs = [row.rootClientID, row.clientID, row.pipelineClientID]
    .map((value) => String(value || ""))
    .filter(Boolean);
  if (clientIDs.some((value) => scope.clientIDs.has(value))) {
    return true;
  }
  return false;
}

function deviceScopeData(deviceRow) {
  if (deviceScopeCache.has(deviceRow)) {
    return deviceScopeCache.get(deviceRow);
  }
  const data = {
    sessionIDs: new Set(),
    clientIDs: new Set(),
    traceIDs: new Set(),
  };
  for (const sessionID of Array.isArray(deviceRow?.sessionIDs) ? deviceRow.sessionIDs : []) {
    const key = String(sessionID || "");
    if (key) {
      data.sessionIDs.add(key);
    }
  }
  for (const clientID of Array.isArray(deviceRow?.clientIDs) ? deviceRow.clientIDs : []) {
    const key = String(clientID || "");
    if (key) {
      data.clientIDs.add(key);
    }
  }
  for (const traceID of Array.isArray(deviceRow?.traceIDs) ? deviceRow.traceIDs : []) {
    const key = String(traceID || "");
    if (key) {
      data.traceIDs.add(key);
    }
  }
  for (const client of Array.isArray(deviceRow?.clients) ? deviceRow.clients : []) {
    const clientID = String(client?.id || "");
    const sessionID = String(client?.sessionID || "");
    const traceID = String(client?.traceID || "");
    if (clientID) {
      data.clientIDs.add(clientID);
    }
    if (sessionID) {
      data.sessionIDs.add(sessionID);
    }
    if (traceID) {
      data.traceIDs.add(traceID);
    }
  }
  deviceScopeCache.set(deviceRow, data);
  return data;
}

function deviceOwnsScopeArrays(deviceRow, row) {
  if (!deviceRow || !row) {
    return false;
  }
  const scope = deviceScopeData(deviceRow);
  return rowMatchesScopeSets(row, scope);
}

function sessionOwnsEntity(sessionRow, entityID, row) {
  switch (entityID) {
    case "devices":
      return deviceOwnsEntity(row, "sessions", sessionRow);
    case "objects":
    case "functions":
    case "object-types":
    case "modules":
      return sessionOwnsScopeArrays(sessionRow, row);
    case "workspaces":
      return Array.isArray(row?.ops) && row.ops.some((op) => sessionOwnsDirectRow(sessionRow, op));
    case "git-remotes":
      return Array.isArray(row?.pipelines) && row.pipelines.some((pipeline) => sessionOwnsDirectRow(sessionRow, pipeline));
    case "registries":
      return Array.isArray(row?.activities) && row.activities.some((activity) => sessionOwnsDirectRow(sessionRow, activity));
    default:
      return sessionOwnsDirectRow(sessionRow, row);
  }
}

function sessionOwnsDirectRow(sessionRow, row) {
  if (!sessionRow || !row) {
    return false;
  }
  const sessionID = String(sessionRow.id || "");
  const traceID = String(sessionRow.traceID || "");
  const rootClientID = String(sessionRow.rootClientID || "");

  if (sessionID) {
    const directSessionIDs = [
      row.sessionID,
      row.pipelineSessionID,
    ]
      .map((value) => String(value || ""))
      .filter(Boolean);
    if (directSessionIDs.includes(sessionID)) {
      return true;
    }
  }

  if (!traceID || String(row.traceID || "") !== traceID) {
    return false;
  }

  if (rootClientID) {
    const clientIDs = [row.rootClientID, row.clientID, row.pipelineClientID]
      .map((value) => String(value || ""))
      .filter(Boolean);
    if (clientIDs.includes(rootClientID)) {
      return true;
    }
  }

  return !sessionID;
}

function sessionOwnsScopeArrays(sessionRow, row) {
  if (!sessionRow || !row) {
    return false;
  }
  const sessionID = String(sessionRow.id || "");
  const traceID = String(sessionRow.traceID || "");
  const rootClientID = String(sessionRow.rootClientID || "");
  const sessionIDs = new Set([sessionID].filter(Boolean));
  const clientIDs = new Set([rootClientID].filter(Boolean));
  const traceIDs = new Set([traceID].filter(Boolean));
  return rowMatchesScopeSets(row, { sessionIDs, clientIDs, traceIDs });
}

function workspaceOwnsScopeArrays(workspaceRow, row) {
  if (!workspaceRow || !row) {
    return false;
  }
  const scope = workspaceScopeData(workspaceRow);
  return rowMatchesScopeSets(row, scope);
}

function rowMatchesScopeSets(row, scope) {
  if (!row || !scope) {
    return false;
  }
  const scopeSessionIDs = scope.sessionIDs instanceof Set ? scope.sessionIDs : new Set();
  const scopeClientIDs = scope.clientIDs instanceof Set ? scope.clientIDs : new Set();
  const scopeTraceIDs = scope.traceIDs instanceof Set ? scope.traceIDs : new Set();
  const sessionIDs = Array.isArray(row?.sessionIDs) ? row.sessionIDs : [];
  if (sessionIDs.some((value) => scopeSessionIDs.has(String(value || "")))) {
    return true;
  }
  const clientIDs = Array.isArray(row?.clientIDs) ? row.clientIDs : [];
  if (clientIDs.some((value) => scopeClientIDs.has(String(value || "")))) {
    return true;
  }
  const traceIDs = Array.isArray(row?.traceIDs) ? row.traceIDs : [];
  return traceIDs.some((value) => scopeTraceIDs.has(String(value || "")));
}

function sessionDomainItemLabel(entity, row) {
  switch (entity.id) {
    case "clients":
      return clientCommandText(row);
    case "pipelines":
      return row.command || row.name || "Pipeline";
    case "repls":
      return row.command || row.name || "Repl";
    case "shells":
      return row.name || row.command || "Shell";
    case "terminals":
      return row.name || row.entryLabel || row.callName || "Terminal";
    case "services":
      return row.name || row.imageRef || "Service";
    case "checks":
      return row.name || row.spanName || "Check";
    case "workspaces":
      return row.name || row.root || "Workspace";
    case "workspace-ops":
      return row.name || row.callName || "Workspace Op";
    case "git-remotes":
      return row.ref || row.name || "Git Remote";
    case "registries":
      return row.ref || row.name || "Registry";
    default:
      return row.name || row.id || entity.label;
  }
}

function sessionDomainItemStatus(entity, row) {
  if (entity.id === "workspaces" || entity.id === "git-remotes" || entity.id === "registries") {
    return "";
  }
  if (entity.id === "sessions") {
    return sessionStatusLabel(row);
  }
  return row.status || "";
}

function sessionDomainItemSubtitle(entity, row) {
  switch (entity.id) {
    case "clients":
      return clientCommandSubtitle(row) || "";
    case "pipelines":
      return row.terminalReturnType || "";
    case "repls":
      return row.commandCount ? `${row.commandCount} commands` : "";
    case "shells":
      return row.mode || row.clientName || "";
    case "terminals":
      return row.callName || row.entryLabel || "";
    case "services":
      return row.kind || row.createdByCallName || "";
    case "checks":
      return row.spanName || "";
    case "workspaces":
      return row.root || "";
    case "workspace-ops":
      return row.path || row.kind || "";
    case "git-remotes":
      return row.host || "";
    case "registries":
      return row.host || row.lastOperation || "";
    default:
      return "";
  }
}

function sessionDomainItemTime(row) {
  const ts = overviewItemUnixNano(row);
  if (ts <= 0) {
    return "";
  }
  return relativeTimeFromNow(ts);
}

function sessionReplModuleLabel(row) {
  const candidates = [row?.command, row?.firstCommand, row?.lastCommand];
  for (const candidate of candidates) {
    const label = extractModuleLabel(candidate);
    if (label) {
      return label;
    }
  }
  return row?.command || row?.name || "Repl";
}

function extractModuleLabel(text) {
  const raw = String(text || "");
  if (!raw) {
    return "";
  }
  let match = raw.match(/(?:^|\s)(?:-m|--module)\s+(\S+)/);
  if (match && match[1]) {
    return match[1];
  }
  match = raw.match(/DAGGER_MODULE=(\S+)/);
  if (match && match[1]) {
    return match[1];
  }
  return "";
}

function sessionReplPipelineCount(row) {
  const ids = new Set();
  for (const command of row?.commands || []) {
    if (command?.pipelineID) {
      ids.add(command.pipelineID);
    }
  }
  return ids.size;
}

function statusTone(status) {
  const normalized = String(status || "").toLowerCase();
  if (["ready", "passing", "loaded", "protected", "warm", "completed", "success", "healthy"].includes(normalized)) {
    return "good";
  }
  if (["failed", "error", "degraded", "flaky", "drifted"].includes(normalized)) {
    return "bad";
  }
  if (["running", "loading", "ingesting", "warming", "live", "open"].includes(normalized)) {
    return "active";
  }
  return "muted";
}

function statusOrb(status) {
  const tone = statusTone(status);
  return `<span class="v3-status-orb v3-status-orb-${escapeHTML(tone)}" aria-label="${escapeHTML(String(status || tone))}" title="${escapeHTML(String(status || tone))}"></span>`;
}

function statusOrbCell(status) {
  if (!status) {
    return "";
  }
  return `<span class="v3-status-cell">${statusOrb(status)}</span>`;
}

function sessionStatusOrb(status) {
  return statusOrb(status);
}

function renderTerminalDetail(entity, row) {
  const activityTable = renderTableHTML({
    columns: [
      { label: "Activity", render: (item) => primaryCell(item.name, item.kind || "activity") },
      { label: "Status", render: (item) => statusOrbCell(item.status) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
      { label: "Duration", render: (item) => escapeHTML(durationLabel(item.startUnixNano, item.endUnixNano, item.status)) },
    ],
    rows: row.activities || [],
    emptyMessage: "No terminal activity recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Terminal</p>
          <strong>${escapeHTML(row.name || row.entryLabel || "Terminal")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)))}
          ${pipelineRecapItem("Session", terminalSessionSummary(row))}
          ${pipelineRecapItem("Call", detailCode(row.callName || "Container.terminal"))}
          ${pipelineRecapItem("Activities", escapeHTML(String(row.activityCount || 0)))}
          ${pipelineRecapItem("Input Container", row.receiverDagqlID ? objectSummaryLink(row.receiverDagqlID) : "Unknown")}
          ${pipelineRecapItem("Output Container", row.outputDagqlID ? objectSummaryLink(row.outputDagqlID) : "Unknown")}
        </div>
      </section>
      ${detailSection("Activity", activityTable)}
    </div>
  `;
}

function renderReplDetail(entity, row) {
  const commandTable = renderTableHTML({
    columns: [
      { label: "Command", render: (item) => replPipelineCell(item) },
      { label: "Status", render: (item) => statusOrbCell(item.status) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
      { label: "Duration", render: (item) => escapeHTML(durationLabel(item.startUnixNano, item.endUnixNano, item.status)) },
    ],
    rows: row.commands || [],
    emptyMessage: "No REPL commands recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Repl</p>
          <strong>${escapeHTML(row.command || row.name || "REPL")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)))}
          ${pipelineRecapItem("Session", replSessionSummary(row))}
          ${pipelineRecapItem("Mode", tonePill("neutral", row.mode || "history"))}
          ${pipelineRecapItem("Commands", escapeHTML(String(row.commandCount || 0)))}
          ${pipelineRecapItem("First", row.firstCommand ? detailCode(row.firstCommand) : "Unknown")}
          ${pipelineRecapItem("Last", row.lastCommand ? detailCode(row.lastCommand) : "Unknown")}
        </div>
      </section>
      ${detailSection("Command History", commandTable)}
    </div>
  `;
}

function renderCheckDetail(entity, row) {
  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Check</p>
          <strong>${escapeHTML(row.name || "Check")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.status))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)))}
          ${pipelineRecapItem("Session", checkSessionSummary(row))}
          ${pipelineRecapItem("Client", row.clientID ? clientLinkByID(row.clientID) : "Unknown")}
          ${pipelineRecapItem("Span", row.spanName ? detailCode(row.spanName) : "Unknown")}
        </div>
      </section>
    </div>
  `;
}

function renderWorkspaceDetail(entity, row) {
  const opsTable = renderTableHTML({
    columns: [
      { label: "Op", render: (item) => linkedPrimaryCell(item.callName || item.name, item.path || item.kind || "", entityPath("workspace-ops", item.routeID)) },
      { label: "Direction", render: (item) => tonePill("neutral", item.direction || "op") },
      { label: "Session", render: (item) => workspaceOpSessionCell(item) },
      { label: "Pipeline", render: (item) => workspaceOpPipelineCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: row.ops || [],
    emptyMessage: "No workspace ops recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Workspace</p>
          <strong>${escapeHTML(row.root || row.name || "Workspace")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Devices", deviceSummaryForEntity("workspaces", row, "None"))}
          ${pipelineRecapItem("Sessions", escapeHTML(String(row.sessionCount || 0)))}
          ${pipelineRecapItem("Ops", escapeHTML(String(row.opCount || 0)))}
          ${pipelineRecapItem("Reads", escapeHTML(String(row.readCount || 0)))}
          ${pipelineRecapItem("Writes", escapeHTML(String(row.writeCount || 0)))}
          ${pipelineRecapItem("Pipelines", escapeHTML(String(row.pipelineCount || 0)))}
          ${pipelineRecapItem("First Seen", escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)))}
        </div>
      </section>
      ${detailSection("Recent Ops", opsTable)}
    </div>
  `;
}

function renderDeviceDetail(entity, row) {
  const topLevelClients = Array.isArray(row?.clients)
    ? row.clients.slice().sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0))
    : [];
  const sessionRows = deviceOwnedRows(row, "sessions");
  const pipelineRows = deviceOwnedRows(row, "pipelines");
  const workspaceRows = deviceOwnedRows(row, "workspaces");
  const workspaceOpRows = deviceOwnedRows(row, "workspace-ops");
  const clientTable = renderTableHTML({
    columns: [
      { label: "Top-Level Client", render: (item) => linkedPrimaryCell(item.name || shortID(item.id), deviceClientPlatform(item) || item.clientKind || "top-level", entityPath("clients", item.id || "")) },
      { label: "Session", render: (item) => deviceClientSessionCell(item) },
      { label: "Kind", render: (item) => tonePill("neutral", item.clientKind || "top-level") },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.firstSeenUnixNano)) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: topLevelClients,
    emptyMessage: "No top-level client records recorded for this device.",
  });
  const sessionTable = renderTableHTML({
    columns: [
      { label: "Session", render: (item) => linkedPrimaryCell(sessionDisplayName(item), sessionDisplaySubtitle(item), entityPath("sessions", item.routeID)) },
      { label: "Status", render: (item) => statusOrbCell(sessionStatusLabel(item)) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.firstSeenUnixNano)) },
      { label: "Duration", render: (item) => escapeHTML(durationLabel(item.firstSeenUnixNano, item.lastSeenUnixNano, item.open ? "running" : item.status)) },
      { label: "Trace", render: (item) => detailCode(shortID(item.traceID)) },
    ],
    rows: sessionRows,
    emptyMessage: "No sessions are attached to this device.",
  });
  const pipelineTable = renderTableHTML({
    columns: [
      { label: "Pipeline", render: (item) => linkedPrimaryCell(item.command || item.name || "Pipeline", "", entityPath("pipelines", item.routeID)) },
      { label: "Status", render: (item) => statusOrbCell(item.status) },
      { label: "Session", render: (item) => pipelineSessionCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
      { label: "Output Type", render: (item) => escapeHTML(pipelineOutputTypeLabel(item)) },
    ],
    rows: pipelineRows,
    emptyMessage: "No pipelines were submitted from this device.",
  });
  const workspaceTable = renderTableHTML({
    columns: [
      { label: "Workspace", render: (item) => linkedPrimaryCell(item.name || item.root || "Workspace", item.root || "", entityPath("workspaces", item.routeID)) },
      { label: "Sessions", render: (item) => escapeHTML(String(item.sessionCount || 0)) },
      { label: "Ops", render: (item) => workspaceCountsCell(item) },
      { label: "Pipelines", render: (item) => escapeHTML(String(item.pipelineCount || 0)) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: workspaceRows,
    emptyMessage: "No local workspaces were observed from this device.",
  });
  const workspaceOpTable = renderTableHTML({
    columns: [
      { label: "Operation", render: (item) => linkedPrimaryCell(item.name || item.callName || "Workspace Op", item.callName || "", entityPath("workspace-ops", item.routeID)) },
      { label: "Direction", render: (item) => tonePill("neutral", item.direction || "op") },
      { label: "Target", render: (item) => item.path ? detailCode(item.path) : "Unknown" },
      { label: "Pipeline", render: (item) => workspaceOpPipelineCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: workspaceOpRows,
    emptyMessage: "No host operations were attributed to this device.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Device</p>
          <strong>${escapeHTML(deviceTitle(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Machine ID", detailCode(shortMachineID(row.machineID, 12) || row.machineID || "Unknown"))}
          ${pipelineRecapItem("Platform", escapeHTML(deviceSubtitle(row) || "Unknown"))}
          ${pipelineRecapItem("Sessions", escapeHTML(String(row.sessionCount || 0)))}
          ${pipelineRecapItem("Top-Level Clients", escapeHTML(String(row.clientCount || 0)))}
          ${pipelineRecapItem("Traces", escapeHTML(String(row.traceCount || 0)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("First Seen", escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)))}
          ${pipelineRecapItem("Latest Command", topLevelClients[0]?.name ? detailCode(topLevelClients[0].name) : "Unknown")}
        </div>
      </section>
      ${detailSection("Top-Level Clients", clientTable)}
      ${detailSection("Sessions", sessionTable)}
      ${detailSection("Pipelines", pipelineTable)}
      ${detailSection("Workspaces", workspaceTable)}
      ${detailSection("Host Ops", workspaceOpTable)}
    </div>
  `;
}

function renderClientDetail(entity, row) {
  const callEntry = ensureClientCallEntry(row);
  const calls = clientCallRows(row, callEntry);
  const nested = clientIsNested(row);
  const moduleRuntime = clientLooksLikeModule(row);
  const childRows = clientRows()
    .filter((item) => String(item?.parentClientID || "") === String(row?.id || ""))
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  let callEmptyMessage = "No calls were attached to this client row.";
  if (Number(row?.callCount || 0) > 0 && callEntry.status === "loading" && calls.length === 0) {
    callEmptyMessage = "Loading calls for this client...";
  } else if (Number(row?.callCount || 0) > 0 && callEntry.status === "error" && calls.length === 0) {
    callEmptyMessage = `Could not load calls for this client: ${callEntry.error || "unknown error"}`;
  }
  const callTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => linkedCallCell(callSignatureText(item), callTableSubtitle(item), entityPath("calls", item.routeID)) },
      { label: "Function", render: (item) => callFunctionCell(item) },
      { label: "Return Type", render: (item) => objectTypeLinkFromName(item.returnType, item.returnTypeID) },
      { label: "Return value", render: (item) => callReturnValueCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: calls,
    emptyMessage: callEmptyMessage,
  });
  const childTable = renderTableHTML({
    columns: [
      { label: "Client", render: (item) => linkedPrimaryCell(clientCommandText(item), clientCommandSubtitle(item), entityPath("clients", item.routeID)) },
      { label: "SDK", render: (item) => clientSDKCell(item) },
      { label: "Module", render: (item) => clientYesNoCell(clientLooksLikeModule(item)) },
      { label: "Calls", render: (item) => clientCallsCell(item) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: childRows,
    emptyMessage: "No nested child clients were attached to this client.",
  });
  const sessionHref = row?.sessionID ? entityPath("sessions", sessionRouteID(row.traceID, row.sessionID)) : "";

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Client</p>
          <strong>${escapeHTML(clientCommandText(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("SDK", clientSDKCell(row))}
          ${pipelineRecapItem("Nested", clientYesNoCell(nested))}
          ${pipelineRecapItem("Module", clientYesNoCell(moduleRuntime))}
          ${pipelineRecapItem("Device", clientDeviceCell(row))}
          ${pipelineRecapItem("Session", sessionHref ? sessionCellByID(row.sessionID, row.traceID) : "Unknown")}
          ${pipelineRecapItem("Platform", escapeHTML(deviceClientPlatform(row) || "Unknown"))}
          ${pipelineRecapItem("Calls", escapeHTML(String(row.callCount || 0)))}
          ${pipelineRecapItem("Top-Level Calls", escapeHTML(String(row.topLevelCallCount || 0)))}
          ${pipelineRecapItem("First Seen", escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Service", row.serviceName ? detailCode(row.serviceName) : "Unknown")}
          ${pipelineRecapItem("Scope", row.scopeName ? detailCode(row.scopeName) : "Unknown")}
        </div>
      </section>
      ${detailCard(
        "Relationships",
        detailList([
          ["Parent Client", nested && row.parentClientID ? clientLinkByID(row.parentClientID) : "None"],
          ["Root Client", row.rootClientID && row.rootClientID !== row.id ? clientLinkByID(row.rootClientID) : "Self"],
          ["Primary Module", clientPrimaryModuleCell(row)],
        ]),
      )}
      ${detailSection("Calls", callTable)}
      ${detailSection("Nested Clients", childTable)}
    </div>
  `;
}

function renderCallDetail(entity, row) {
  const parentCall = callRowByID(row.parentCallID);
  const receiver = objectRowByID(row.receiverDagqlID);
  const output = objectRowByID(row.outputDagqlID);
  const functionRow = functionRowForCall(row);
  const argRows = callArgumentRows(row);
  const moduleCell = functionRow ? functionModuleCell(functionRow) : detailLinkList(moduleRowsForCall(row).map((item) => entityInlineLink("modules", item.routeID, item.ref)), "None");
  const argTable = renderTableHTML({
    columns: [
      { label: "Argument", render: (item) => primaryCell(item.name || "arg", callArgumentKindLabel(item)) },
      { label: "Value", render: (item) => callArgumentValueCell(item) },
      { label: "Type", render: (item) => callArgumentTypeCell(item) },
    ],
    rows: argRows,
    emptyMessage: "No arguments were recorded for this call.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Function Call</p>
          <strong>${escapeHTML(callTitle(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(row.statusCode || "ok"))}
          ${pipelineRecapItem("Return Type", objectTypeLinkFromName(row.returnType, row.returnTypeID))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.statusCode || "completed")))}
          ${pipelineRecapItem("Device", deviceSummaryForEntity("calls", row))}
          ${pipelineRecapItem("Session", row.sessionID ? pipelineSessionSummary({ traceID: row.traceID, sessionID: row.sessionID }) : "None")}
          ${pipelineRecapItem("Client", row.clientID ? clientLinkByID(row.clientID) : "Unknown")}
          ${pipelineRecapItem("Function", functionRow ? entityInlineLink("functions", functionRow.routeID, functionTitle(functionRow)) : detailCode(row.name || "Unknown"))}
          ${pipelineRecapItem("Return Value", output ? entityInlineLink("objects", output.routeID, objectTitle(output)) : "None")}
          ${pipelineRecapItem("Module", moduleCell)}
        </div>
      </section>
      ${detailCard(
        "Relationships",
        detailList([
          ["Function", functionRow ? entityInlineLink("functions", functionRow.routeID, functionTitle(functionRow)) : "None"],
          ["Parent Call", parentCall ? entityInlineLink("calls", parentCall.routeID, callTitle(parentCall)) : "None"],
          ["Receiver", receiver ? entityInlineLink("objects", receiver.routeID, objectTitle(receiver)) : row.receiverIsQuery ? tonePill("neutral", "Query") : "None"],
          ["Return Value", output ? entityInlineLink("objects", output.routeID, objectTitle(output)) : "None"],
          ["Client", row.clientID ? clientLinkByID(row.clientID) : "Unknown"],
          ["Module", moduleCell],
        ]),
      )}
      ${detailSection("Arguments", argTable)}
    </div>
  `;
}

function renderFunctionDetail(entity, row) {
  const callEntry = ensureFunctionCallEntry(row);
  const calls = functionCallRows(row, callEntry);
  const relationshipItems = [
    ["Module", functionModuleCell(row)],
    ["Receiver Type", functionReceiverTypeCell(row)],
    ["Return Type", objectTypeLinkFromName(row.returnType, row.returnTypeID)],
  ];
  if (row.originalName && row.originalName !== row.name) {
    relationshipItems.push(["Original Name", detailCode(row.originalName)]);
  }
  let callEmptyMessage = "No calls were attached to this function row.";
  if (Number(row?.callCount || 0) > 0 && callEntry.status === "loading" && calls.length === 0) {
    callEmptyMessage = "Loading calls for this function...";
  } else if (Number(row?.callCount || 0) > 0 && callEntry.status === "error" && calls.length === 0) {
    callEmptyMessage = `Could not load calls for this function: ${callEntry.error || "unknown error"}`;
  }
  const callTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => linkedCallCell(callSignatureText(item), callTableSubtitle(item), entityPath("calls", item.routeID)) },
      { label: "Return value", render: (item) => callReturnValueCell(item) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: calls,
    emptyMessage: callEmptyMessage,
  });
  const snapshotTable = renderTableHTML({
    columns: [
      { label: "Snapshot", render: (item) => linkedPrimaryCell(objectTitle(item), item.typeName || "Function", entityPath("objects", item.routeID)) },
      { label: "Produced By", render: (item) => objectProducedBySummary(item) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: functionSnapshotRows(row),
    emptyMessage: "No Function metadata snapshots were attached to this function row.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Function</p>
          <strong>${escapeHTML(functionTitle(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Module", functionModuleCell(row))}
          ${pipelineRecapItem("Receiver Type", functionReceiverTypeCell(row))}
          ${pipelineRecapItem("Return Type", objectTypeLinkFromName(row.returnType, row.returnTypeID))}
          ${pipelineRecapItem("Calls", escapeHTML(String(row.callCount || 0)))}
          ${pipelineRecapItem("Snapshots", escapeHTML(String(row.snapshotCount || 0)))}
          ${pipelineRecapItem("Arguments", escapeHTML(String(row.argCount || 0)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
        </div>
      </section>
      ${detailCard(
        "Relationships",
        detailList(relationshipItems),
      )}
      ${row.description ? detailCard("Description", `<p>${escapeHTML(row.description)}</p>`) : ""}
      ${detailSection("Calls", callTable)}
      ${detailSection("Function Snapshots", snapshotTable)}
    </div>
  `;
}

function renderObjectDetail(entity, row) {
  const fields = objectFieldRows(row);
  const typeRow = objectTypeRowByID(row.typeID) || objectTypeRowByName(row.typeName);
  const canonicalModuleRow = moduleSnapshotCanonicalRow(row);
  const canonicalFunctionRow = functionSnapshotCanonicalRow(row);
  const relationshipItems = [
    ["Type", typeRow ? entityInlineLink("object-types", typeRow.routeID, typeRow.name) : detailCode(row.typeName || "Unknown")],
  ];
  if (String(row?.typeName || "").trim() === "Module") {
    relationshipItems.push(["Canonical Module", canonicalModuleRow ? entityInlineLink("modules", canonicalModuleRow.routeID, canonicalModuleRow.ref) : "None"]);
  }
  if (String(row?.typeName || "").trim() === "Function") {
    relationshipItems.push(["Canonical Function", canonicalFunctionRow ? entityInlineLink("functions", canonicalFunctionRow.routeID, functionTitle(canonicalFunctionRow)) : "None"]);
  }
  relationshipItems.push(
    ["Modules", typeRow ? detailLinkList(objectTypeModuleLinks(typeRow), "None") : "None"],
    [
      "Produced By",
      detailLinkList(
        (Array.isArray(row?.producedByCallIDs) ? row.producedByCallIDs : [])
          .map((id) => callRowByID(id))
          .filter(Boolean)
          .map((call) => entityInlineLink("calls", call.routeID, callSignatureText(call))),
        "None",
      ),
    ],
  );
  const fieldTable = renderTableHTML({
    columns: [
      { label: "Field", render: (item) => primaryCell(item.name, item.type || "") },
      { label: "Value", render: (item) => renderObjectFieldValue(item.value) },
      { label: "Refs", render: (item) => detailLinkList(item.refs.map((ref) => objectSummaryLink(ref, shortDagqlID(ref))), "None") },
    ],
    rows: fields,
    emptyMessage: "No structured field state was recorded for this snapshot.",
  });
  const producedByTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => linkedPrimaryCell(callTitle(item), callSubtitle(item), entityPath("calls", item.routeID)) },
      { label: "Return Type", render: (item) => objectTypeLinkFromName(item.returnType, item.returnTypeID) },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: (Array.isArray(row?.producedByCallIDs) ? row.producedByCallIDs : []).map((id) => callRowByID(id)).filter(Boolean),
    emptyMessage: "No producing calls were attached to this snapshot.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Object Snapshot</p>
          <strong>${escapeHTML(objectTitle(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Type", typeRow ? entityInlineLink("object-types", typeRow.routeID, typeRow.name) : detailCode(row.typeName || "Object"))}
          ${pipelineRecapItem("DAGQL ID", detailCode(row.dagqlID || "Unknown"))}
          ${pipelineRecapItem("Device", deviceSummaryForEntity("objects", row))}
          ${pipelineRecapItem("Produced By", objectProducedBySummary(row))}
          ${pipelineRecapItem("Refs", escapeHTML(String(Array.isArray(row.fieldRefs) ? row.fieldRefs.length : 0)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
        </div>
      </section>
      ${detailCard(
        "Relationships",
        detailList(relationshipItems),
      )}
      ${detailSection("Fields", fieldTable)}
      ${detailSection("Produced By Calls", producedByTable)}
    </div>
  `;
}

function renderObjectTypeDetail(entity, row) {
  const snapshots = objectRowsForType(row);
  const functions = objectTypeRelatedFunctionRows(row);
  const snapshotTable = renderTableHTML({
    columns: [
      { label: "Snapshot", render: (item) => linkedPrimaryCell(objectTitle(item), item.typeName || "Object", entityPath("objects", item.routeID)) },
      { label: "Produced By", render: (item) => objectProducedBySummary(item) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: snapshots,
    emptyMessage: "No concrete snapshots of this type were recorded.",
  });
  const functionTable = renderTableHTML({
    columns: [
      { label: "Function", render: (item) => linkedPrimaryCell(functionTitle(item), functionSubtitle(item), entityPath("functions", item.routeID)) },
      { label: "Relationship", render: (item) => tonePill("neutral", item.typeRole || "related") },
      { label: "Module", render: (item) => functionModuleCell(item) },
      { label: "Calls", render: (item) => escapeHTML(String(item.callCount || 0)) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: functions,
    emptyMessage: "No functions currently point at this type.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Object Type</p>
          <strong>${escapeHTML(row.name || "Type")}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Module", detailLinkList(objectTypeModuleLinks(row), "Core"))}
          ${pipelineRecapItem("Snapshots", escapeHTML(String(row.snapshotCount || 0)))}
          ${pipelineRecapItem("Functions", escapeHTML(String(functions.length)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
        </div>
      </section>
      ${detailCard(
        "Relationships",
        detailList([
          ["Module", detailLinkList(objectTypeModuleLinks(row), "Core")],
        ]),
      )}
      ${detailSection("Snapshots", snapshotTable)}
      ${detailSection("Functions", functionTable)}
    </div>
  `;
}

function renderModuleDetail(entity, row) {
  const preludeCalls = modulePreludeCallRows(row);
  const typeRows = moduleTypeRows(row);
  const functionRows = moduleFunctionRows(row);
  const snapshotRows = moduleSnapshotRows(row);
  const clientRowsForModuleRef = moduleClientRows(row);
  const resolvedRefs = Array.isArray(row?.resolvedRefs)
    ? Array.from(new Set(row.resolvedRefs.map((value) => String(value || "").trim()).filter(Boolean)))
    : [];
  const extraResolvedRefs = resolvedRefs.filter((value) => value !== String(row?.ref || "").trim());
  const preludeTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => linkedPrimaryCell(callTitle(item), callSubtitle(item), entityPath("calls", item.routeID)) },
      { label: "Output", render: (item) => objectSummaryLink(item.outputDagqlID) || "None" },
      { label: "Started", render: (item) => escapeHTML(relativeTimeFromNow(item.startUnixNano)) },
    ],
    rows: preludeCalls,
    emptyMessage: "No module-prelude calls were attached to this module row.",
  });
  const typeTable = renderTableHTML({
    columns: [
      { label: "Type", render: (item) => linkedPrimaryCell(item.name || "Type", `${item.snapshotCount || 0} snapshots`, entityPath("object-types", item.routeID)) },
      { label: "Functions", render: (item) => escapeHTML(String(item.functionCount || 0)) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: typeRows,
    emptyMessage: "No object types currently depend on this module.",
  });
  const snapshotTable = renderTableHTML({
    columns: [
      { label: "Snapshot", render: (item) => linkedPrimaryCell(objectTitle(item), item.typeName || "Module", entityPath("objects", item.routeID)) },
      { label: "Produced By", render: (item) => objectProducedBySummary(item) },
      { label: "Last Seen", render: (item) => escapeHTML(relativeTimeFromNow(item.lastSeenUnixNano)) },
    ],
    rows: snapshotRows,
    emptyMessage: "No concrete Module snapshots could be mapped back to this module row.",
  });
  const functionTable = renderTableHTML({
    columns: [
      { label: "Function", render: (item) => linkedPrimaryCell(functionTitle(item), functionSubtitle(item), entityPath("functions", item.routeID)) },
      { label: "Return Type", render: (item) => objectTypeLinkFromName(item.returnType, item.returnTypeID) },
      { label: "Calls", render: (item) => escapeHTML(String(item.callCount || 0)) },
    ],
    rows: functionRows,
    emptyMessage: "No functions currently depend on this module.",
  });
  const clientTable = renderTableHTML({
    columns: [
      { label: "Client", render: (item) => linkedPrimaryCell(clientCommandText(item), clientCommandSubtitle(item), entityPath("clients", item.routeID)) },
      { label: "SDK", render: (item) => clientSDKCell(item) },
      { label: "Nested", render: (item) => clientYesNoCell(clientIsNested(item)) },
      { label: "Calls", render: (item) => clientCallsCell(item) },
    ],
    rows: clientRowsForModuleRef,
    emptyMessage: "No clients currently declare this as their primary module.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <section class="v3-detail-card v3-pipeline-recap">
        <div class="v3-pipeline-recap-command">
          <p class="v3-foot-label">Module</p>
          <strong>${escapeHTML(moduleTitle(row))}</strong>
        </div>
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Ref", detailCode(row.ref || "Unknown"))}
          ${extraResolvedRefs.length > 0 ? pipelineRecapItem("Resolved", detailInlineList(extraResolvedRefs, "None")) : ""}
          ${pipelineRecapItem("Prelude Calls", escapeHTML(String(Array.isArray(row.callIDs) ? row.callIDs.length : 0)))}
          ${pipelineRecapItem("Snapshots", escapeHTML(String(snapshotRows.length)))}
          ${pipelineRecapItem("Sessions", escapeHTML(String(row.sessionCount || 0)))}
          ${pipelineRecapItem("Types", escapeHTML(String(typeRows.length)))}
          ${pipelineRecapItem("Functions", escapeHTML(String(functionRows.length)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
        </div>
      </section>
      ${extraResolvedRefs.length > 0
        ? detailCard(
            "Relationships",
            detailList([
              ["Resolved Refs", detailInlineList(extraResolvedRefs, "None")],
            ]),
          )
        : ""}
      ${detailSection("Concrete Module Snapshots", snapshotTable)}
      ${detailSection("Prelude Calls", preludeTable)}
      ${detailSection("Clients", clientTable)}
      ${detailSection("Functions", functionTable)}
      ${detailSection("Dependent Object Types", typeTable)}
    </div>
  `;
}

function renderShellDetail(entity, row) {
  const evidenceTable = renderTableHTML({
    columns: [
      { label: "Kind", key: "kind" },
      { label: "Confidence", render: (item) => confidencePill(item.confidence) },
      { label: "Source", key: "source" },
      { label: "Note", key: "note" },
    ],
    rows: row.evidence || [],
    emptyMessage: "No shell evidence recorded.",
  });
  const relationTable = renderTableHTML({
    columns: [
      { label: "Relation", render: (item) => tonePill("neutral", item.relation) },
      { label: "Target", render: (item) => primaryCell(item.target, item.targetKind) },
      { label: "Detail", key: "note" },
    ],
    rows: row.relations || [],
    emptyMessage: "No shell relations recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <div class="v3-detail-grid">
        ${detailCard(
          "Summary",
          detailList([
            ["Status", statusPill(row.status)],
            ["Mode", tonePill("neutral", row.mode || "interactive")],
            ["Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status))],
            ["Entry", detailCode(row.entryLabel || row.command || row.name)],
            ["Trace", detailCode(row.traceID)],
            ["Session", row.sessionID ? sessionInlineLinkByID(row.sessionID, row.traceID) : "Unknown"],
            ["Client", detailCode(row.clientID || "Unknown")],
          ]),
        )}
        ${detailCard(
          "Activity",
          detailList([
            ["Owned calls", escapeHTML(String(row.callCount || 0))],
            ["Descendant clients", escapeHTML(String(row.childClientCount || 0))],
            ["Owned spans", escapeHTML(String(row.spanCount || 0))],
            ["Activities", detailInlineList(row.activityNames, "None")],
            ["Child clients", detailInlineList(row.childClientIDs, "None")],
            ["Call IDs", detailInlineList(row.callIDs, "None")],
          ]),
        )}
      </div>
      ${detailSection("Evidence", evidenceTable)}
      ${detailSection("Relations", relationTable)}
    </div>
  `;
}

function detailSection(title, body) {
  return `
    <section class="v3-detail-card">
      <p class="v3-foot-label">${escapeHTML(title)}</p>
      <div class="v3-table-shell">${body}</div>
    </section>
  `;
}

function detailCard(title, body) {
  return `
    <section class="v3-detail-card">
      <p class="v3-foot-label">${escapeHTML(title)}</p>
      ${body}
    </section>
  `;
}

function detailList(rows) {
  return `
    <dl class="v3-detail-list">
      ${rows
        .map(
          ([label, value]) => `
            <div class="v3-detail-row">
              <dt>${escapeHTML(label)}</dt>
              <dd>${value}</dd>
            </div>`,
        )
        .join("")}
    </dl>
  `;
}

function detailCode(value) {
  return `<code class="v3-detail-code">${escapeHTML(value || "")}</code>`;
}

function detailInlineList(values, emptyLabel) {
  if (!Array.isArray(values) || values.length === 0) {
    return `<span>${escapeHTML(emptyLabel)}</span>`;
  }
  return `
    <div class="v3-detail-tags">
      ${values.map((value) => `<code class="v3-detail-code">${escapeHTML(value)}</code>`).join("")}
    </div>
  `;
}

function backLink(entity) {
  const href = entityPath(entity.id);
  return `<a class="v3-back-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(entity.label)}</a>`;
}

function tableModel(entity, sectionID) {
  if (entity.dynamicKind === "terminals") {
    return terminalsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "repls") {
    return replsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "checks") {
    return checksTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "workspaces") {
    return workspacesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "devices") {
    return devicesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "clients") {
    return clientsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "calls") {
    return callsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "functions") {
    return functionsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "objects") {
    return objectsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "object-types") {
    return objectTypesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "modules") {
    return modulesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "services") {
    return servicesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "sessions") {
    return sessionsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "pipelines") {
    return pipelinesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "shells") {
    return shellsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "workspace-ops") {
    return workspaceOpsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "git-remotes") {
    return gitRemotesTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "registries") {
    return registriesTableModel(entity, sectionID);
  }
  switch (sectionID) {
    case "inventory":
      return {
        eyebrow: "Inventory",
        title: `${entity.label} Inventory`,
        meta: `${entity.inventory.length} mock rows`,
        emptyMessage: "No inventory rows yet.",
        columns: [
          { label: "Entity", render: (row) => primaryCell(row.name, row.scope) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Owner", key: "owner" },
          { label: "Scope", key: "scope" },
          { label: "Updated", key: "updated" },
        ],
        rows: entity.inventory,
      };
    case "evidence":
      return {
        eyebrow: "Evidence",
        title: `${entity.label} Discovery Evidence`,
        meta: `${entity.evidence.length} mock signals`,
        emptyMessage: "No evidence rows yet.",
        columns: [
          { label: "Kind", key: "kind" },
          { label: "Confidence", render: (row) => confidencePill(row.confidence) },
          { label: "Source", key: "source" },
          { label: "Note", key: "note" },
        ],
        rows: entity.evidence,
      };
    case "relations":
      return {
        eyebrow: "Relations",
        title: `${entity.label} Connections`,
        meta: `${entity.relations.length} mock edges`,
        emptyMessage: "No relations yet.",
        columns: [
          { label: "Source", key: "source" },
          { label: "Relation", render: (row) => tonePill("neutral", row.relation) },
          { label: "Target", key: "target" },
          { label: "Detail", key: "note" },
        ],
        rows: entity.relations,
      };
    case "overview":
    default:
      return {
        eyebrow: "Overview",
        title: `${entity.label} Current Surface`,
        meta: `top ${Math.min(3, entity.inventory.length)} mock entries`,
        emptyMessage: "No overview rows yet.",
        columns: [
          { label: "Entity", render: (row) => primaryCell(row.name, row.owner) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Scope", key: "scope" },
          { label: "Updated", key: "updated" },
        ],
        rows: entity.inventory.slice(0, 3),
      };
  }
}

function sessionsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Session Inventory",
        meta: `${rows.length} real sessions`,
        emptyMessage: "No sessions detected yet.",
        columns: [
          {
            label: "Session",
            render: (row) => linkedPrimaryCell(sessionDisplayName(row), sessionDisplaySubtitle(row), entityPath(entity.id, row.routeID)),
            sortValue: (row) => sessionDisplayName(row),
            filterValue: (row) => [sessionDisplayName(row), row?.name, row?.rootClientID].filter(Boolean).join(" "),
          },
          { label: "Status", render: (row) => statusOrbCell(sessionStatusLabel(row)) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.firstSeenUnixNano, row.lastSeenUnixNano, row.open ? "running" : row.status)) },
          { label: "Root Client", render: (row) => row.rootClientID ? clientLinkByID(row.rootClientID) : "Unknown" },
          { label: "Trace", render: (row) => detailCode(shortID(row.traceID)) },
        ],
        rows,
      };
  }
}

function terminalsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Terminals",
        meta: `${rows.length} real terminals`,
        emptyMessage: "No terminal sessions detected yet.",
        columns: [
          { label: "Terminal", render: (row) => linkedPrimaryCell(row.name || row.entryLabel || "Terminal", row.callName || "Container.terminal", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)) },
          { label: "Session", render: (row) => terminalSessionCell(row) },
          { label: "Activity", render: (row) => terminalActivityCell(row) },
        ],
        rows,
      };
  }
}

function replsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Repls",
        meta: `${rows.length} real repls`,
        emptyMessage: "No repl history detected yet.",
        columns: [
          { label: "Repl", render: (row) => linkedPrimaryCell(row.command || row.name, row.mode || row.firstCommand || "", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)) },
          { label: "Session", render: (row) => replSessionCell(row) },
          { label: "Commands", render: (row) => replCommandHistoryCell(row) },
        ],
        rows,
      };
  }
}

function checksTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Checks",
        meta: `${rows.length} real checks`,
        emptyMessage: "No checks detected in the current dataset.",
        columns: [
          { label: "Check", render: (row) => linkedPrimaryCell(row.name || "Check", row.spanName || "", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)) },
          { label: "Session", render: (row) => checkSessionCell(row) },
        ],
        rows,
      };
  }
}

function workspacesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Workspaces",
        meta: `${rows.length} real workspaces`,
        emptyMessage: "No observed workspaces detected yet.",
        columns: [
          { label: "Workspace", render: (row) => linkedPrimaryCell(row.name || row.root || "Workspace", row.root || "", entityPath(entity.id, row.routeID)) },
          { label: "Sessions", render: (row) => escapeHTML(String(row.sessionCount || 0)) },
          { label: "Ops", render: (row) => workspaceCountsCell(row) },
          { label: "Pipelines", render: (row) => escapeHTML(String(row.pipelineCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function devicesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Devices",
        meta: `${rows.length} real devices`,
        emptyMessage: "No top-level client devices detected yet.",
        columns: [
          { label: "Device", render: (row) => linkedPrimaryCell(deviceTitle(row), deviceSubtitle(row), entityPath(entity.id, row.routeID)) },
          { label: "Sessions", render: (row) => escapeHTML(String(row.sessionCount || 0)) },
          { label: "Top-Level Clients", render: (row) => escapeHTML(String(row.clientCount || 0)) },
          { label: "Traces", render: (row) => escapeHTML(String(row.traceCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function clientsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Clients",
        meta: `${rows.length} real clients`,
        emptyMessage: "No execution clients detected yet.",
        columns: [
          {
            label: "Client Command",
            render: (row) => linkedPrimaryCell(clientCommandText(row), clientCommandSubtitle(row), entityPath(entity.id, row.routeID)),
            sortValue: (row) => clientCommandText(row),
            filterValue: (row) => [clientCommandText(row), clientCommandSubtitle(row), row?.serviceName, row?.clientKind].filter(Boolean).join(" "),
          },
          {
            label: "SDK",
            render: (row) => clientSDKCell(row),
            sortValue: (row) => clientSDKText(row),
            filterValue: (row) => clientSDKText(row),
          },
          {
            label: "Nested",
            render: (row) => clientYesNoCell(clientIsNested(row)),
            sortValue: (row) => (clientIsNested(row) ? 1 : 0),
          },
          {
            label: "Module",
            render: (row) => clientYesNoCell(clientLooksLikeModule(row)),
            sortValue: (row) => (clientLooksLikeModule(row) ? 1 : 0),
          },
          {
            label: "Device",
            render: (row) => clientDeviceCell(row),
          },
          {
            label: "Calls",
            render: (row) => clientCallsCell(row),
            sortValue: (row) => Number(row?.callCount || 0),
            filterValue: (row) => `${row?.callCount || 0} ${row?.topLevelCallCount || 0}`,
          },
        ],
        rows,
      };
  }
}

function callsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Calls",
        meta: `${rows.length} real calls`,
        emptyMessage: "No calls detected yet.",
        columns: [
          {
            label: "Call",
            render: (row) => linkedCallCell(callSignatureText(row), callTableSubtitle(row), entityPath(entity.id, row.routeID)),
            sortValue: (row) => callSignatureText(row),
            filterValue: (row) => `${callSignatureText(row)} ${callTableSubtitle(row)}`,
          },
          {
            label: "Function",
            render: (row) => callFunctionCell(row),
            sortValue: (row) => callFunctionLabel(row),
            filterValue: (row) => callFunctionLabel(row),
          },
          { label: "Return Type", render: (row) => objectTypeLinkFromName(row.returnType, row.returnTypeID) },
          { label: "Return value", render: (row) => callReturnValueCell(row) },
          { label: "Receiver", render: (row) => objectSummaryLink(row.receiverDagqlID) || (row.receiverIsQuery ? tonePill("neutral", "Query") : "None") },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
        ],
        rows,
      };
  }
}

function functionsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Functions",
        meta: `${rows.length} real functions`,
        emptyMessage: "No function identities detected yet.",
        columns: [
          {
            label: "Receiver Type",
            render: (row) => functionReceiverTypeCell(row),
            sortValue: (row) => row.receiverType || "",
            filterValue: (row) => row.receiverType || "",
          },
          {
            label: "Name",
            render: (row) => linkedPrimaryCell(row.name || functionTitle(row), row.originalName && row.originalName !== row.name ? `original ${row.originalName}` : "", entityPath(entity.id, row.routeID)),
            sortValue: (row) => row.name || functionTitle(row),
            filterValue: (row) => `${row.name || functionTitle(row)} ${row.callName || ""} ${row.originalName || ""} ${row.description || ""}`,
          },
          { label: "Module", render: (row) => functionModuleCell(row) },
          { label: "Return Type", render: (row) => objectTypeLinkFromName(row.returnType, row.returnTypeID) },
          { label: "Calls", render: (row) => escapeHTML(String(row.callCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function objectsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Objects",
        meta: `${rows.length} real snapshots`,
        emptyMessage: "No object snapshots detected yet.",
        columns: [
          { label: "Object", render: (row) => linkedPrimaryCell(objectTitle(row), row.typeName || "snapshot", entityPath(entity.id, row.routeID)) },
          { label: "Type", render: (row) => objectTypeLinkFromName(row.typeName, row.typeID) },
          { label: "Produced By", render: (row) => objectProducedBySummary(row) },
          { label: "Refs", render: (row) => escapeHTML(String(Array.isArray(row.fieldRefs) ? row.fieldRefs.length : 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function objectTypesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Object Types",
        meta: `${rows.length} real types`,
        emptyMessage: "No object types detected yet.",
        columns: [
          { label: "Type", render: (row) => linkedPrimaryCell(row.name || "Type", moduleRefsSummaryText(row), entityPath(entity.id, row.routeID)) },
          { label: "Module", render: (row) => detailLinkList(objectTypeModuleLinks(row), "Core") },
          { label: "Snapshots", render: (row) => escapeHTML(String(row.snapshotCount || 0)) },
          { label: "Functions", render: (row) => escapeHTML(String(row.functionCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function modulesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Modules",
        meta: `${rows.length} real modules`,
        emptyMessage: "No loaded modules detected yet.",
        columns: [
          { label: "Module", render: (row) => linkedPrimaryCell(moduleTitle(row), moduleSubtitle(row), entityPath(entity.id, row.routeID)) },
          { label: "Prelude Calls", render: (row) => escapeHTML(String(Array.isArray(row.callIDs) ? row.callIDs.length : 0)) },
          { label: "Sessions", render: (row) => escapeHTML(String(row.sessionCount || 0)) },
          { label: "Traces", render: (row) => escapeHTML(String(row.traceCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function servicesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Services",
        meta: `${rows.length} real services`,
        emptyMessage: "No services detected yet.",
        columns: [
          { label: "Service", render: (row) => linkedPrimaryCell(row.name || "Service", servicePrimarySubtitle(row), entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Kind", render: (row) => tonePill("neutral", row.kind || "service") },
          { label: "Created By", render: (row) => serviceCreatedByCell(row) },
          { label: "Session", render: (row) => serviceSessionCell(row) },
          { label: "Last Activity", render: (row) => escapeHTML(relativeTimeFromNow(row.lastActivityUnixNano)) },
        ],
        rows,
      };
  }
}

function pipelinesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
      return {
        eyebrow: "Inventory",
        title: "Pipelines",
        meta: `${rows.length} real pipelines`,
        emptyMessage: "No pipelines detected yet.",
        columns: [
          { label: "Pipeline", render: (row) => linkedPrimaryCell(row.command || row.name, "", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Session", render: (row) => pipelineSessionCell(row) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(pipelineDurationLabel(row)) },
          { label: "Output Type", render: (row) => escapeHTML(pipelineOutputTypeLabel(row)) },
        ],
        rows,
      };
    case "evidence":
      return {
        eyebrow: "Evidence",
        title: "Pipeline Discovery Evidence",
        meta: `${entity.evidence.length} real evidence rows`,
        emptyMessage: "No pipeline evidence rows yet.",
        columns: [
          { label: "Pipeline", render: (row) => primaryCell(row.runName, row.source) },
          { label: "Kind", key: "kind" },
          { label: "Confidence", render: (row) => confidencePill(row.confidence) },
          { label: "Source", key: "source" },
          { label: "Note", key: "note" },
        ],
        rows: entity.evidence,
      };
    case "relations":
      return {
        eyebrow: "Relations",
        title: "Pipeline Relations",
        meta: `${entity.relations.length} derived relations`,
        emptyMessage: "No pipeline relations yet.",
        columns: [
          { label: "Pipeline", key: "source" },
          { label: "Relation", render: (row) => tonePill("neutral", row.relation) },
          { label: "Target", render: (row) => primaryCell(row.target, row.targetKind) },
          { label: "Detail", key: "note" },
        ],
        rows: entity.relations,
      };
    case "overview":
    default:
      return {
        eyebrow: "Overview",
        title: "Pipeline Current Surface",
        meta: `${Math.min(3, rows.length)} of ${rows.length} real pipelines`,
        emptyMessage: "No pipelines detected yet.",
        columns: [
          { label: "Pipeline", render: (row) => primaryCell(row.name, row.command || row.chainLabel) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Output", render: (row) => pipelineOutputCell(row) },
          { label: "Follow-up", render: (row) => pipelineFollowupCell(row) },
          { label: "Session", render: (row) => pipelineSessionCell(row) },
        ],
        rows: rows.slice(0, 3),
      };
  }
}

function shellsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
      return {
        eyebrow: "Inventory",
        title: "Shell Inventory",
        meta: `${rows.length} real shells`,
        emptyMessage: "No shell sessions detected yet.",
        columns: [
          { label: "Shell", render: (row) => linkedPrimaryCell(row.name, row.command || row.entryLabel, entityPath(entity.id, row.routeID)) },
          { label: "Mode", render: (row) => tonePill("neutral", row.mode || "interactive") },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)) },
          { label: "Activity", render: (row) => shellActivityCell(row) },
          { label: "Descendants", render: (row) => shellDescendantCell(row) },
          { label: "Scope", render: (row) => shellScopeCell(row) },
        ],
        rows,
      };
    case "evidence":
      return {
        eyebrow: "Evidence",
        title: "Shell Discovery Evidence",
        meta: `${entity.evidence.length} real evidence rows`,
        emptyMessage: "No shell evidence rows yet.",
        columns: [
          { label: "Shell", render: (row) => primaryCell(row.shellName, row.source) },
          { label: "Kind", key: "kind" },
          { label: "Confidence", render: (row) => confidencePill(row.confidence) },
          { label: "Source", key: "source" },
          { label: "Note", key: "note" },
        ],
        rows: entity.evidence,
      };
    case "relations":
      return {
        eyebrow: "Relations",
        title: "Shell Relations",
        meta: `${entity.relations.length} derived relations`,
        emptyMessage: "No shell relations yet.",
        columns: [
          { label: "Shell", key: "source" },
          { label: "Relation", render: (row) => tonePill("neutral", row.relation) },
          { label: "Target", render: (row) => primaryCell(row.target, row.targetKind) },
          { label: "Detail", key: "note" },
        ],
        rows: entity.relations,
      };
    case "overview":
    default:
      return {
        eyebrow: "Overview",
        title: "Shell Current Surface",
        meta: `${Math.min(3, rows.length)} of ${rows.length} real shells`,
        emptyMessage: "No shell sessions detected yet.",
        columns: [
          { label: "Shell", render: (row) => primaryCell(row.name, row.command || row.entryLabel) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Activity", render: (row) => shellActivityCell(row) },
          { label: "Descendants", render: (row) => shellDescendantCell(row) },
          { label: "Scope", render: (row) => shellScopeCell(row) },
        ],
        rows: rows.slice(0, 3),
      };
  }
}

function workspaceOpsTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Workspace Ops",
        meta: `${rows.length} real workspace ops`,
        emptyMessage: "No workspace ops detected yet.",
        columns: [
          { label: "Operation", render: (row) => linkedPrimaryCell(row.name, row.callName, entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(row.status) },
          { label: "Target", render: (row) => row.path ? detailCode(row.path) : "Unknown" },
          { label: "Device", render: (row) => deviceSummaryForEntity("workspace-ops", row) },
          { label: "Pipeline", render: (row) => workspaceOpPipelineCell(row) },
          { label: "Session", render: (row) => workspaceOpSessionCell(row) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
        ],
        rows,
      };
  }
}

function gitRemotesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Git Remotes",
        meta: `${rows.length} real git remotes`,
        emptyMessage: "No git remotes detected yet.",
        columns: [
          { label: "Remote", render: (row) => linkedPrimaryCell(row.ref || row.name, row.host || "", entityPath(entity.id, row.routeID)) },
          { label: "Host", render: (row) => row.host ? detailCode(row.host) : "Unknown" },
          { label: "Pipelines", render: (row) => gitRemotePipelineCountCell(row) },
          { label: "Sessions", render: (row) => escapeHTML(String(row.sessionCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function registriesTableModel(entity, sectionID) {
  const rows = visibleEntityRows(entity);
  switch (sectionID) {
    case "inventory":
    default:
      return {
        eyebrow: "Inventory",
        title: "Registries",
        meta: `${rows.length} real registries`,
        emptyMessage: "No registries detected yet.",
        columns: [
          { label: "Registry", render: (row) => linkedPrimaryCell(row.ref || row.name, row.latestRef || "", entityPath(entity.id, row.routeID)) },
          { label: "Host", render: (row) => row.host ? detailCode(row.host) : "Unknown" },
          { label: "Pipelines", render: (row) => registryPipelineCountCell(row) },
          { label: "Requests", render: (row) => escapeHTML(String(row.activityCount || 0)) },
          { label: "Last Seen", render: (row) => escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)) },
        ],
        rows,
      };
  }
}

function currentEntity() {
  return materializeEntity(findEntity(state.entityID) || entities[0]);
}

function materializedEntityByID(entityID) {
  const entity = findEntity(entityID);
  if (!entity) {
    return null;
  }
  return materializeEntity(entity);
}

function setPanelHeadHidden(hidden) {
  if (els.panelHead) {
    els.panelHead.style.display = hidden ? "none" : "";
  }
}

function currentNavEntityID() {
  if (isOverviewRoute()) {
    return "";
  }
  const entity = findEntity(state.entityID) || entities[0];
  return entity.navOwnerID || entity.id;
}

function isOverviewRoute(entityID = state.entityID) {
  return entityID === OVERVIEW_ROUTE_ID;
}

function currentWorkspaceFilterID() {
  return String(state.workspaceFilterID || "");
}

function currentWorkspaceFilterRow() {
  const filterID = currentWorkspaceFilterID();
  if (!filterID) {
    return null;
  }
  const workspaces = materializedEntityByID("workspaces");
  if (!workspaces || !Array.isArray(workspaces.liveItems)) {
    return null;
  }
  return workspaces.liveItems.find((row) => row.routeID === filterID) || null;
}

function currentSessionFilterID() {
  return String(state.sessionFilterID || "");
}

function currentSessionFilterRow() {
  const filterID = currentSessionFilterID();
  if (!filterID) {
    return null;
  }
  const scoped = availableSessionRows().find((row) => row.routeID === filterID);
  if (scoped) {
    return scoped;
  }
  if (state.entityID === "sessions" && state.detailID === filterID) {
    const sessions = materializedEntityByID("sessions");
    if (!sessions || !Array.isArray(sessions.liveItems)) {
      return null;
    }
    return sessions.liveItems.find((row) => row.routeID === filterID) || null;
  }
  return null;
}

function visibleEntityRows(entity, rows = entity?.liveItems) {
  if (!entity || !Array.isArray(rows)) {
    return [];
  }
  const workspaceRow = currentWorkspaceFilterRow();
  let visible = rows;
  if (workspaceRow) {
    visible = visible.filter((row) => workspaceOwnsEntity(workspaceRow, entity.id, row));
  }
  const sessionRow = currentSessionFilterRow();
  if (!sessionRow) {
    return visible;
  }
  if (entity.id === "sessions") {
    return visible.filter((row) => row.routeID === sessionRow.routeID);
  }
  return visible.filter((row) => sessionOwnsEntity(sessionRow, entity.id, row));
}

function sessionFilterOptionLabel(row) {
  return shortID(row.id) || "Session";
}

function sessionFilterOptionMeta(row) {
  const pieces = [];
  const timeLabel = relativeTimeFromNow(Number(row.lastSeenUnixNano || row.firstSeenUnixNano || 0));
  if (timeLabel) {
    pieces.push(timeLabel);
  }
  if (row.rootClientID) {
    pieces.push(`root ${shortID(row.rootClientID)}`);
  } else if (row.traceID) {
    pieces.push(`trace ${shortID(row.traceID)}`);
  }
  return pieces.join(" · ");
}

function workspaceFilterOptionLabel(row) {
  const root = String(row?.root || row?.name || "Workspace").trim();
  const qualifier = String(row?.hostQualifier || "").trim();
  if (!qualifier) {
    return root;
  }
  return `${root} · ${qualifier}`;
}

function sessionFilterMatches(row, query) {
  if (!query) {
    return true;
  }
  const haystack = [
    shortID(row?.id),
    row?.id,
    shortID(row?.traceID),
    row?.traceID,
    shortID(row?.rootClientID),
    row?.rootClientID,
  ]
    .map((value) => String(value || "").toLowerCase())
    .filter(Boolean)
    .join(" ");
  return haystack.includes(query);
}

function workspaceHostQualifier(workspaceRow) {
  if (!workspaceRootNeedsHostQualifier(workspaceRow?.root)) {
    return "";
  }
  const clientMap = clientRowsByID();
  const machineIDs = new Set();
  const candidateClientIDs = [];
  if (Array.isArray(workspaceRow?.clientIDs)) {
    candidateClientIDs.push(...workspaceRow.clientIDs);
  }
  for (const op of Array.isArray(workspaceRow?.ops) ? workspaceRow.ops : []) {
    candidateClientIDs.push(op?.clientID, op?.pipelineClientID);
  }
  for (const clientID of candidateClientIDs) {
    const key = String(clientID || "");
    if (!key) {
      continue;
    }
    const client = clientMap.get(key);
    const machineID = String(client?.clientMachineID || "").trim();
    if (machineID) {
      machineIDs.add(machineID);
    }
  }
  const ids = Array.from(machineIDs).sort();
  if (!ids.length) {
    return "";
  }
  const primary = shortMachineID(ids[0]);
  if (ids.length === 1) {
    return `@${primary}`;
  }
  return `@${primary}+${ids.length - 1}`;
}

function clientRowsByID() {
  const byID = new Map();
  for (const row of clientRows()) {
    const id = String(row?.id || "");
    if (!id) {
      continue;
    }
    byID.set(id, row);
  }
  return byID;
}

function deviceRows() {
  const rows = materializedEntityByID("devices")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function sessionRows() {
  const rows = materializedEntityByID("sessions")?.liveItems;
  if (Array.isArray(rows)) {
    return rows;
  }
  const liveRows = state.live.sessions?.items;
  return Array.isArray(liveRows) ? liveRows : [];
}

function sessionRowByID(sessionID) {
  const id = String(sessionID || "").trim();
  if (!id) {
    return null;
  }
  return sessionRows().find((row) => String(row?.id || "") === id) || null;
}

function clientRows() {
  const rows = materializedEntityByID("clients")?.liveItems;
  if (Array.isArray(rows)) {
    return rows;
  }
  const liveRows = state.live.clients?.items;
  return Array.isArray(liveRows) ? liveRows : [];
}

function deviceRowsForEntity(entityID, row) {
  return deviceRows().filter((device) => deviceOwnsEntity(device, entityID, row));
}

function callRows() {
  const rows = materializedEntityByID("calls")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function functionRows() {
  const rows = materializedEntityByID("functions")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function objectRows() {
  const rows = materializedEntityByID("objects")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function objectTypeRows() {
  const rows = materializedEntityByID("object-types")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function moduleRows() {
  const rows = materializedEntityByID("modules")?.liveItems;
  return Array.isArray(rows) ? rows : [];
}

function moduleClientRows(moduleRow) {
  const ref = String(moduleRow?.ref || "").trim();
  if (!ref) {
    return [];
  }
  return clientRows()
    .filter((row) => String(row?.primaryModuleRef || "").trim() === ref)
    .slice()
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function clientRowByID(clientID) {
  return clientRows().find((row) => String(row?.id || "") === String(clientID || "")) || null;
}

function callRowByID(callID) {
  return callRows().find((row) => String(row.id || "") === String(callID || "")) || null;
}

function functionRowByID(functionID) {
  return functionRows().find((row) => String(row.id || "") === String(functionID || "")) || null;
}

function objectRowByID(dagqlID) {
  return objectRows().find((row) => String(row.dagqlID || "") === String(dagqlID || "")) || null;
}

function objectTypeRowByID(typeID) {
  return objectTypeRows().find((row) => String(row.id || "") === String(typeID || "")) || null;
}

function sessionCellByID(sessionID, traceID, fallback = "Unknown") {
  if (!sessionID) {
    return fallback;
  }
  const href = entityPath("sessions", sessionRouteID(traceID, sessionID));
  const session = sessionRowByID(sessionID);
  return linkedPrimaryCell(session ? sessionDisplayName(session) : shortID(sessionID), session ? sessionDisplaySubtitle(session) : "", href);
}

function sessionInlineLinkByID(sessionID, traceID, fallback = "None") {
  if (!sessionID) {
    return fallback;
  }
  const href = entityPath("sessions", sessionRouteID(traceID, sessionID));
  const session = sessionRowByID(sessionID);
  const label = session ? sessionDisplayName(session) : shortID(sessionID);
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(label)}</a>`;
}

function objectTypeRowByName(name) {
  const matches = objectTypeRows().filter((row) => String(row.name || "") === String(name || ""));
  return matches.length === 1 ? matches[0] : null;
}

function moduleRowByRef(ref) {
  return moduleRows().find((row) => String(row.ref || "") === String(ref || "")) || null;
}

function entityInlineLink(entityID, routeID, label) {
  const text = String(label || "").trim() || entityID;
  if (!routeID) {
    return escapeHTML(text);
  }
  const href = entityPath(entityID, routeID);
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(text)}</a>`;
}

function detailLinkList(links, emptyLabel = "None") {
  if (!Array.isArray(links) || links.length === 0) {
    return emptyLabel;
  }
  return `<div class="v3-detail-tags">${links.map((item) => `<span>${item}</span>`).join("")}</div>`;
}

function callTitle(row) {
  return row?.name || "Call";
}

function functionTitle(row) {
  const callName = String(row?.callName || row?.name || "").trim();
  if (callName) {
    return callFieldName({ name: callName });
  }
  return "Function";
}

function functionSubtitle(row) {
  const pieces = [];
  const moduleLabel = functionModuleText(row);
  if (moduleLabel) {
    pieces.push(moduleLabel);
  }
  if (row?.returnType) {
    pieces.push(`returns ${row.returnType}`);
  }
  if (Number(row?.callCount || 0) > 0) {
    pieces.push(`${row.callCount} calls`);
  } else if (Number(row?.snapshotCount || 0) > 0) {
    pieces.push(`${row.snapshotCount} snapshots`);
  }
  return pieces.join(" · ");
}

function callFieldName(row) {
  const raw = String(row?.name || "").trim();
  if (!raw) {
    return "call";
  }
  const dot = raw.lastIndexOf(".");
  if (dot < 0) {
    return raw;
  }
  const receiver = raw.slice(0, dot).trim();
  const field = raw.slice(dot + 1).trim();
  if (!receiver || !field) {
    return raw;
  }
  if (receiver === "Query") {
    return field;
  }
  return `${receiver}.${field}`;
}

function callSubtitle(row) {
  return [row?.derivedOperation, row?.returnType].filter(Boolean).join(" · ");
}

function callSignatureText(row, maxArgs = 4) {
  const name = callFieldName(row);
  const args = callArgumentRows(row);
  if (args.length === 0) {
    return `${name}()`;
  }
  const rendered = args.slice(0, maxArgs).map((arg) => `${arg.name}: ${callSignatureArgText(arg)}`);
  if (args.length > maxArgs) {
    rendered.push("...");
  }
  return `${name}(${rendered.join(", ")})`;
}

function callTableSubtitle(row) {
  const receiver = callReceiverSummary(row);
  const pieces = [receiver, row?.derivedOperation, row?.returnType].filter(Boolean);
  return pieces.join(" · ");
}

function callReceiverSummary(row) {
  if (row?.receiverIsQuery) {
    return "Query";
  }
  const receiver = objectRowByID(row?.receiverDagqlID);
  if (receiver) {
    return objectTitle(receiver);
  }
  return row?.receiverDagqlID ? shortDagqlID(row.receiverDagqlID) : "";
}

function callSignatureArgText(arg) {
  const dagqlID = String(arg?.dagqlID || "").trim();
  if (dagqlID) {
    return `<${shortDagqlID(dagqlID)}>`;
  }
  return callSignatureLiteralText(arg?.value);
}

function callSignatureLiteralText(value) {
  if (value == null) {
    return "null";
  }
  if (typeof value === "string") {
    return truncateText(JSON.stringify(value), 48);
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return truncateText(JSON.stringify(value), 64);
  } catch (_error) {
    return truncateText(String(value), 64);
  }
}

function shortDagqlID(value) {
  return shortID(value, 18);
}

function objectTitle(row) {
  const typeName = String(row?.typeName || "").trim() || "Object";
  const suffix = shortDagqlID(row?.dagqlID || "");
  return suffix ? `${typeName} ${suffix}` : typeName;
}

function objectSubtitle(row) {
  return String(row?.typeName || "").trim() || "snapshot";
}

function moduleTitle(row) {
  const ref = String(row?.ref || "").trim();
  if (ref === "core") {
    return "Core";
  }
  if (!ref) {
    return "Module";
  }
  const parts = ref.split("/").filter(Boolean);
  return parts[parts.length - 1] || ref;
}

function moduleSubtitle(row) {
  const ref = String(row?.ref || "").trim();
  if (ref === "core") {
    return "Built-in Dagger module";
  }
  const latestResolved = Array.isArray(row?.resolvedRefs) && row.resolvedRefs.length ? row.resolvedRefs[row.resolvedRefs.length - 1] : "";
  return latestResolved || ref || "";
}

function functionModuleText(row) {
  const ref = String(row?.moduleRef || "").trim();
  if (!ref || ref === "core") {
    return "Core";
  }
  return ref;
}

function functionModuleCell(row) {
  const ref = String(row?.moduleRef || "").trim();
  if (!ref) {
    return tonePill("neutral", "Core");
  }
  if (ref === "core") {
    return entityInlineLink("modules", "core", "Core");
  }
  const module = moduleRowByRef(ref);
  return module ? entityInlineLink("modules", module.routeID, module.ref) : detailCode(ref);
}

function functionReceiverTypeCell(row) {
  const text = String(row?.receiverType || "").trim();
  if (!text) {
    return "Unknown";
  }
  if (text === "Query") {
    return tonePill("neutral", "Query");
  }
  return objectTypeLinkFromName(text, row?.receiverTypeID || "");
}

function objectTypeModuleLinks(row) {
  const ref = objectTypeModuleRef(row);
  if (ref === "core") {
    return [entityInlineLink("modules", "core", "Core")];
  }
  const module = moduleRowByRef(ref);
  return module ? [entityInlineLink("modules", module.routeID, module.ref)] : [];
}

function objectTypeModuleRef(row) {
  const direct = String(row?.moduleRef || "").trim();
  if (direct) {
    return direct;
  }
  const refs = Array.isArray(row?.moduleRefs) ? row.moduleRefs.filter(Boolean) : [];
  return refs[0] || "";
}

function moduleRefsSummaryText(rowOrModuleRefs) {
  if (Array.isArray(rowOrModuleRefs)) {
    return rowOrModuleRefs.length > 0 ? rowOrModuleRefs[0] : "Core";
  }
  return objectTypeModuleRef(rowOrModuleRefs) || "Core";
}

function modulePreludeCallRows(row) {
  const ids = new Set(Array.isArray(row?.callIDs) ? row.callIDs : []);
  return callRows().filter((call) => ids.has(String(call.id || "")));
}

function moduleTypeRows(row) {
  const ref = String(row?.ref || "");
  if (!ref) {
    return [];
  }
  return objectTypeRows().filter((item) => objectTypeModuleRef(item) === ref);
}

function moduleFunctionRows(row) {
  const ref = String(row?.ref || "");
  if (!ref) {
    return [];
  }
  return functionRows()
    .filter((item) => String(item?.moduleRef || "") === ref)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function moduleRowsForCall(row) {
  const callID = String(row?.id || "");
  const sessionID = String(row?.sessionID || "");
  const clientID = String(row?.clientID || "");
  return moduleRows().filter((module) => {
    if (Array.isArray(module?.callIDs) && module.callIDs.includes(callID)) {
      return true;
    }
    if (clientID && Array.isArray(module?.clientIDs) && module.clientIDs.includes(clientID)) {
      return true;
    }
    return sessionID && Array.isArray(module?.sessionIDs) && module.sessionIDs.includes(sessionID);
  });
}

function directModuleRowsForCall(row) {
  const callID = String(row?.id || "").trim();
  if (!callID) {
    return [];
  }
  return moduleRows().filter((module) => Array.isArray(module?.callIDs) && module.callIDs.includes(callID));
}

function scopedModuleRowsForCall(row) {
  const sessionID = String(row?.sessionID || "").trim();
  const clientID = String(row?.clientID || "").trim();
  if (!sessionID && !clientID) {
    return [];
  }
  return moduleRows().filter((module) => {
    if (clientID && Array.isArray(module?.clientIDs) && module.clientIDs.includes(clientID)) {
      return true;
    }
    return sessionID && Array.isArray(module?.sessionIDs) && module.sessionIDs.includes(sessionID);
  });
}

function uniqueRow(rows) {
  if (!Array.isArray(rows) || rows.length !== 1) {
    return null;
  }
  return rows[0] || null;
}

function dedupeByKey(rows, keyFn) {
  const seen = new Set();
  const out = [];
  for (const row of Array.isArray(rows) ? rows : []) {
    const key = String(keyFn?.(row) || "");
    if (!key || seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(row);
  }
  return out;
}

function canonicalModuleRowForCall(row) {
  const direct = uniqueRow(directModuleRowsForCall(row));
  if (direct) {
    return direct;
  }
  return uniqueRow(scopedModuleRowsForCall(row));
}

function functionRowForCall(row) {
  const direct = functionRowByID(row?.functionID);
  if (direct) {
    return direct;
  }
  return null;
}

function rowsShareScope(a, b) {
  const sessionIDsA = new Set(Array.isArray(a?.sessionIDs) ? a.sessionIDs.map((value) => String(value || "")).filter(Boolean) : []);
  const clientIDsA = new Set(Array.isArray(a?.clientIDs) ? a.clientIDs.map((value) => String(value || "")).filter(Boolean) : []);
  const traceIDsA = new Set(Array.isArray(a?.traceIDs) ? a.traceIDs.map((value) => String(value || "")).filter(Boolean) : []);
  const sessionIDsB = Array.isArray(b?.sessionIDs) ? b.sessionIDs : [];
  if (sessionIDsB.some((value) => sessionIDsA.has(String(value || "")))) {
    return true;
  }
  const clientIDsB = Array.isArray(b?.clientIDs) ? b.clientIDs : [];
  if (clientIDsB.some((value) => clientIDsA.has(String(value || "")))) {
    return true;
  }
  const traceIDsB = Array.isArray(b?.traceIDs) ? b.traceIDs : [];
  return traceIDsB.some((value) => traceIDsA.has(String(value || "")));
}

function moduleSnapshotDefinedTypeNames(row) {
  const defs = row?.outputState?.fields?.ObjectDefs?.value;
  if (!Array.isArray(defs) || defs.length === 0) {
    return [];
  }
  const names = new Set();
  for (const item of defs) {
    const asObject = item?.AsObject;
    if (asObject?.Valid === false) {
      continue;
    }
    const value = asObject?.Value || item || {};
    const originalName = String(value?.OriginalName || "").trim();
    const name = String(value?.Name || "").trim();
    if (originalName) {
      names.add(originalName);
      continue;
    }
    if (name) {
      names.add(name);
    }
  }
  return Array.from(names.values()).sort();
}

function moduleSnapshotRowsByMutationReceiver(row) {
  const dagqlID = String(row?.dagqlID || "").trim();
  if (!dagqlID) {
    return [];
  }
  return callRows()
    .filter((call) => String(call?.receiverDagqlID || "").trim() === dagqlID)
    .map((call) => objectRowByID(call?.outputDagqlID))
    .filter((item) => String(item?.typeName || "").trim() === "Module");
}

function canonicalModuleRowForModuleSnapshotByTypes(row) {
  const typeNames = moduleSnapshotDefinedTypeNames(row);
  if (typeNames.length === 0) {
    return null;
  }
  const candidates = moduleRows().filter((module) => {
    if (!rowsShareScope(module, row)) {
      return false;
    }
    const names = new Set(moduleTypeRows(module).map((item) => String(item?.name || "").trim()).filter(Boolean));
    if (names.size === 0) {
      return false;
    }
    return typeNames.every((name) => names.has(name));
  });
  return uniqueRow(candidates);
}

function moduleSnapshotCanonicalRow(row, visited = new Set()) {
  if (!row || String(row.typeName || "").trim() !== "Module") {
    return null;
  }
  if (moduleSnapshotCanonicalCache.has(row)) {
    return moduleSnapshotCanonicalCache.get(row);
  }
  const dagqlID = String(row.dagqlID || "").trim();
  if (!dagqlID || visited.has(dagqlID)) {
    return null;
  }
  visited.add(dagqlID);

  const directMatches = new Map();
  const producedBy = Array.isArray(row?.producedByCallIDs) ? row.producedByCallIDs : [];
  for (const callID of producedBy) {
    const call = callRowByID(callID);
    const direct = canonicalModuleRowForCall(call);
    if (direct?.routeID) {
      directMatches.set(direct.routeID, direct);
    }
  }
  if (directMatches.size === 1) {
    const match = Array.from(directMatches.values())[0];
    moduleSnapshotCanonicalCache.set(row, match);
    return match;
  }
  if (directMatches.size > 1) {
    moduleSnapshotCanonicalCache.set(row, null);
    return null;
  }

  const typeMatch = canonicalModuleRowForModuleSnapshotByTypes(row);
  if (typeMatch) {
    moduleSnapshotCanonicalCache.set(row, typeMatch);
    return typeMatch;
  }

  const receiverMatches = new Map();
  for (const callID of producedBy) {
    const call = callRowByID(callID);
    const receiver = objectRowByID(call?.receiverDagqlID);
    const candidate = moduleSnapshotCanonicalRow(receiver, visited);
    if (candidate?.routeID) {
      receiverMatches.set(candidate.routeID, candidate);
    }
  }
  if (receiverMatches.size === 1) {
    const match = Array.from(receiverMatches.values())[0];
    moduleSnapshotCanonicalCache.set(row, match);
    return match;
  }

  const successorMatches = new Map();
  for (const successor of moduleSnapshotRowsByMutationReceiver(row)) {
    const candidate = moduleSnapshotCanonicalRow(successor, visited);
    if (candidate?.routeID) {
      successorMatches.set(candidate.routeID, candidate);
    }
  }
  if (successorMatches.size === 1) {
    const match = Array.from(successorMatches.values())[0];
    moduleSnapshotCanonicalCache.set(row, match);
    return match;
  }
  moduleSnapshotCanonicalCache.set(row, null);
  return null;
}

function moduleSnapshotRows(moduleRow) {
  const targetRef = String(moduleRow?.ref || "").trim();
  if (!targetRef) {
    return [];
  }
  return objectRows()
    .filter((row) => String(row?.typeName || "").trim() === "Module")
    .filter((row) => String(moduleSnapshotCanonicalRow(row)?.ref || "") === targetRef)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function callArgumentRows(row) {
  const args = Array.isArray(row?.args) ? row.args : [];
  if (args.length > 0) {
    return args.map((arg, index) => ({
      name: String(arg?.name || "").trim() || `arg ${index + 1}`,
      kind: String(arg?.kind || "").trim() || (arg?.dagqlID ? "object" : "literal"),
      dagqlID: String(arg?.dagqlID || "").trim(),
      value: arg?.value,
    }));
  }
  return (Array.isArray(row?.argDagqlIDs) ? row.argDagqlIDs : [])
    .filter(Boolean)
    .map((dagqlID, index) => ({
      name: `input ${index + 1}`,
      kind: "object",
      dagqlID: String(dagqlID || "").trim(),
      value: null,
    }));
}

function callArgumentKindLabel(arg) {
  const kind = String(arg?.kind || "").trim();
  switch (kind) {
    case "object":
      return "object ref";
    case "object-literal":
      return "object";
    case "bool":
      return "bool";
    case "enum":
      return "enum";
    case "int":
      return "int";
    case "float":
      return "float";
    case "list":
      return "list";
    case "null":
      return "null";
    case "string":
      return "string";
    default:
      return kind || "literal";
  }
}

function callArgumentValueCell(arg) {
  const dagqlID = String(arg?.dagqlID || "").trim();
  if (dagqlID) {
    const object = objectRowByID(dagqlID);
    if (object) {
      return entityInlineLink("objects", object.routeID, objectTitle(object));
    }
    return detailCode(shortDagqlID(dagqlID));
  }
  if (String(arg?.kind || "").trim() === "null") {
    return detailCode("null");
  }
  return detailCode(callArgumentValueText(arg?.value));
}

function callArgumentTypeCell(arg) {
  const dagqlID = String(arg?.dagqlID || "").trim();
  if (dagqlID) {
    const object = objectRowByID(dagqlID);
    return object ? objectTypeLinkFromName(object.typeName, object.typeID) : escapeHTML("Object");
  }
  return escapeHTML(callArgumentKindLabel(arg));
}

function callArgumentValueText(value) {
  if (value == null) {
    return "null";
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch (_error) {
    return String(value);
  }
}

function objectRowsForType(typeRow) {
  const ids = new Set(Array.isArray(typeRow?.snapshotDagqlIDs) ? typeRow.snapshotDagqlIDs : []);
  return objectRows().filter((row) => ids.has(String(row?.dagqlID || "")));
}

function objectTypeRelatedFunctionRows(typeRow) {
  const typeID = String(typeRow?.id || "").trim();
  const typeName = String(typeRow?.name || "").trim();
  return functionRows()
    .map((row) => {
      const returnsType = (typeID && String(row?.returnTypeID || "") === typeID) || (typeName && String(row?.returnType || "") === typeName);
      const receivesType = (typeID && String(row?.receiverTypeID || "") === typeID) || (typeName && String(row?.receiverType || "") === typeName);
      if (!returnsType && !receivesType) {
        return null;
      }
      let typeRole = "related";
      if (returnsType && receivesType) {
        typeRole = "receiver + return";
      } else if (returnsType) {
        typeRole = "return";
      } else if (receivesType) {
        typeRole = "receiver";
      }
      return {
        ...row,
        typeRole,
      };
    })
    .filter(Boolean)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function functionCallRows(row, entry = null) {
  const ids = new Set(Array.isArray(row?.callIDs) ? row.callIDs : []);
  const combined = dedupeByKey(
    [...callRows(), ...(Array.isArray(entry?.items) ? entry.items : [])].map((call) => ({
      ...call,
      routeID: String(call?.routeID || call?.id || ""),
    })),
    (call) => String(call?.routeID || call?.id || ""),
  );
  return combined
    .filter((call) => {
      const callID = String(call?.routeID || call?.id || "");
      if (ids.size > 0) {
        return ids.has(callID);
      }
      return String(call?.functionID || "") === String(row?.id || "");
    })
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
}

function clientCallRows(row, entry = null) {
  const clientID = String(row?.id || "");
  if (!clientID) {
    return [];
  }
  const combined = dedupeByKey(
    [...callRows(), ...(Array.isArray(entry?.items) ? entry.items : [])].map((call) => ({
      ...call,
      routeID: String(call?.routeID || call?.id || ""),
    })),
    (call) => String(call?.routeID || call?.id || ""),
  );
  return combined.filter((call) => String(call?.clientID || "") === clientID).sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
}

function functionSnapshotRows(row) {
  const ids = new Set(Array.isArray(row?.snapshotDagqlIDs) ? row.snapshotDagqlIDs : []);
  return objectRows()
    .filter((item) => ids.has(String(item?.dagqlID || "")))
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function functionSnapshotCanonicalRow(row) {
  const dagqlID = String(row?.dagqlID || "").trim();
  if (!dagqlID) {
    return null;
  }
  return functionRows().find((item) => Array.isArray(item?.snapshotDagqlIDs) && item.snapshotDagqlIDs.includes(dagqlID)) || null;
}

function functionReturnTypeName(row) {
  return functionSnapshotReturnTypeName(row?.outputState);
}

function functionSnapshotName(row) {
  return functionSnapshotFieldString(row?.outputState, "Name");
}

function functionSnapshotFieldString(outputState, fieldName) {
  const field = outputState?.fields?.[fieldName];
  return String(field?.value || "").trim();
}

function functionSnapshotReturnTypeName(outputState) {
  if (!outputState || String(outputState.type || "").trim() !== "Function") {
    return "";
  }
  return String(outputState?.fields?.ReturnType?.value?.AsObject?.Value?.Name || "").trim();
}

function objectTypeLinkFromName(typeName, typeID = "") {
  const text = String(typeName || "").trim();
  if (!text) {
    return "Unknown";
  }
  const directID = String(typeID || "").trim();
  if (directID) {
    const row = objectTypeRowByID(directID);
    if (row) {
      return entityInlineLink("object-types", row.routeID, row.name || text);
    }
    if (isNonPageObjectTypeName(text)) {
      return detailCode(text);
    }
  }
  const row = objectTypeRowByName(text);
  if (!row) {
    return detailCode(text);
  }
  return entityInlineLink("object-types", row.routeID, row.name);
}

function isNonPageObjectTypeName(typeName) {
  const text = String(typeName || "").trim();
  if (!text) {
    return false;
  }
  return nonPageObjectTypeNames.has(text);
}

function objectSummaryLink(dagqlID, label = "") {
  const row = objectRowByID(dagqlID);
  if (!row) {
    return dagqlID ? detailCode(label || shortDagqlID(dagqlID)) : "";
  }
  return entityInlineLink("objects", row.routeID, label || objectTitle(row));
}

function callReturnValueCell(row) {
  const dagqlID = String(row?.outputDagqlID || "").trim();
  if (!dagqlID) {
    return "None";
  }
  const output = objectRowByID(dagqlID);
  if (output) {
    return entityInlineLink("objects", output.routeID, objectTitle(output));
  }
  return detailCode(shortDagqlID(dagqlID));
}

function callFunctionLabel(row) {
  const functionRow = functionRowForCall(row);
  if (functionRow) {
    return functionTitle(functionRow);
  }
  return callFieldName(row);
}

function callFunctionCell(row) {
  const functionRow = functionRowForCall(row);
  if (functionRow) {
    return entityInlineLink("functions", functionRow.routeID, functionTitle(functionRow));
  }
  const fallback = callFieldName(row);
  return fallback ? detailCode(fallback) : "None";
}

function callSummaryLink(callID, label = "") {
  const row = callRowByID(callID);
  if (!row) {
    return callID ? detailCode(label || shortID(callID)) : "";
  }
  return entityInlineLink("calls", row.routeID, label || callTitle(row));
}

function moduleSummaryLink(ref) {
  if (String(ref || "").trim() === "core") {
    return entityInlineLink("modules", "core", "Core");
  }
  const row = moduleRowByRef(ref);
  if (!row) {
    return ref ? detailCode(ref) : "";
  }
  return entityInlineLink("modules", row.routeID, row.ref);
}

function clientIsNested(row) {
  const id = String(row?.id || "").trim();
  const parentClientID = String(row?.parentClientID || "").trim();
  const rootClientID = String(row?.rootClientID || "").trim();
  return Boolean(parentClientID || (id && rootClientID && rootClientID !== id));
}

function clientLooksLikeModule(row) {
  if (!clientIsNested(row)) {
    return false;
  }
  const serviceName = String(row?.serviceName || "").trim();
  if (serviceName && serviceName !== "dagger-cli") {
    return true;
  }
  const sdkName = String(row?.sdkName || "").trim();
  const commandArgs = Array.isArray(row?.commandArgs) ? row.commandArgs.filter(Boolean) : [];
  return String(row?.clientKind || "").trim() === "nested" && !commandArgs.length && Boolean(sdkName);
}

function clientCommandText(row) {
  const name = String(row?.name || "").trim();
  if (name) {
    return name;
  }
  const args = Array.isArray(row?.commandArgs) ? row.commandArgs.filter(Boolean) : [];
  if (args.length) {
    return args.join(" ");
  }
  const serviceName = String(row?.serviceName || "").trim();
  if (serviceName) {
    return serviceName;
  }
  return shortID(row?.id) || "Client";
}

function clientCommandMode(row) {
  const args = Array.isArray(row?.commandArgs) ? row.commandArgs.map((item) => String(item || "").trim()).filter(Boolean) : [];
  if (args.includes("-c")) {
    return "inline";
  }
  const known = ["call", "check", "query", "shell", "session"];
  for (const token of args) {
    if (known.includes(token)) {
      return token;
    }
  }
  if (clientLooksLikeModule(row)) {
    return "module runtime";
  }
  if (clientIsNested(row)) {
    return "nested runtime";
  }
  return String(row?.clientKind || "").trim() || "client";
}

function clientCommandSubtitle(row) {
  return [clientCommandMode(row), deviceClientPlatform(row)].filter(Boolean).join(" · ");
}

function clientSDKText(row) {
  const sdkName = String(row?.sdkName || "").trim();
  const sdkVersion = String(row?.sdkVersion || "").trim();
  if (!sdkName) {
    return "";
  }
  return sdkVersion ? `${sdkName} ${sdkVersion}` : sdkName;
}

function clientSDKCell(row) {
  const text = clientSDKText(row);
  return text ? detailCode(text) : "None";
}

function clientYesNoCell(value) {
  return tonePill(value ? "good" : "neutral", value ? "Yes" : "No");
}

function clientDeviceRow(row) {
  const ownMachineID = String(row?.clientMachineID || "").trim();
  if (ownMachineID) {
    const direct = deviceRows().find((device) => String(device?.machineID || "").trim() === ownMachineID);
    if (direct) {
      return direct;
    }
  }
  const rootClientID = String(row?.rootClientID || row?.id || "").trim();
  if (!rootClientID) {
    return null;
  }
  const rootClient = clientRowByID(rootClientID);
  const rootMachineID = String(rootClient?.clientMachineID || "").trim();
  if (!rootMachineID) {
    return null;
  }
  return deviceRows().find((device) => String(device?.machineID || "").trim() === rootMachineID) || null;
}

function clientDeviceCell(row) {
  const device = clientDeviceRow(row);
  if (device) {
    return entityInlineLink("devices", device.routeID, deviceTitle(device));
  }
  const machineID = String(row?.clientMachineID || clientRowByID(row?.rootClientID)?.clientMachineID || "").trim();
  if (machineID) {
    return detailCode(shortMachineID(machineID));
  }
  return "None";
}

function clientCallsCell(row) {
  return primaryCell(String(row?.callCount || 0), `${row?.topLevelCallCount || 0} top-level`);
}

function clientPrimaryModuleRows(row) {
  const refs = new Set();
  const primaryRef = String(row?.primaryModuleRef || "").trim();
  if (primaryRef) {
    refs.add(primaryRef);
  }
  for (const module of moduleRows()) {
    if (Array.isArray(module?.clientIDs) && module.clientIDs.includes(String(row?.id || ""))) {
      refs.add(String(module.ref || "").trim());
    }
  }
  return Array.from(refs)
    .filter(Boolean)
    .map((ref) => moduleRowByRef(ref) || { ref, routeID: "" })
    .sort((a, b) => String(a?.ref || "").localeCompare(String(b?.ref || "")));
}

function clientPrimaryModuleCell(row) {
  const modules = clientPrimaryModuleRows(row);
  if (!modules.length) {
    return "None";
  }
  if (modules.length === 1) {
    return moduleSummaryLink(modules[0].ref);
  }
  return detailLinkList(modules.map((module) => moduleSummaryLink(module.ref)), "None");
}

function clientLinkByID(clientID, fallback = "Unknown") {
  const row = clientRowByID(clientID);
  const label = row ? clientCommandText(row) : String(clientID || "").trim();
  if (!row) {
    return label ? detailCode(label) : fallback;
  }
  return entityInlineLink("clients", String(row.routeID || row.id || ""), label);
}

function objectProducedBySummary(row) {
  const ids = Array.isArray(row?.producedByCallIDs) ? row.producedByCallIDs : [];
  if (!ids.length) {
    return "None";
  }
  const groups = [];
  const byLabel = new Map();
  for (const id of ids) {
    const call = callRowByID(id);
    const label = call ? callTitle(call) : shortID(id);
    if (byLabel.has(label)) {
      byLabel.get(label).ids.push(id);
      continue;
    }
    const entry = { label, ids: [id] };
    byLabel.set(label, entry);
    groups.push(entry);
  }
  if (groups.length === 1) {
    const group = groups[0];
    const label = group.ids.length > 1 ? `${group.label} ×${group.ids.length}` : group.label;
    return callSummaryLink(group.ids[0], label);
  }
  return detailLinkList(
    groups.slice(0, 3).map((group) => {
      const label = group.ids.length > 1 ? `${group.label} ×${group.ids.length}` : group.label;
      return callSummaryLink(group.ids[0], label);
    }),
    `${ids.length} calls`,
  );
}

function objectFieldRows(row) {
  const fields = row?.outputState?.fields;
  if (!fields || typeof fields !== "object" || Array.isArray(fields)) {
    return [];
  }
  return Object.entries(fields)
    .map(([key, field]) => ({
      name: humanizePipelineFieldLabel(key),
      rawName: key,
      type: String(field?.type || "").trim(),
      value: field?.value,
      refs: Array.isArray(field?.refs) ? field.refs.filter(Boolean) : [],
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

function renderObjectFieldValue(value) {
  const { text, block } = objectFieldValueText(value);
  if (block) {
    return `<pre class="v3-detail-code v3-detail-code-block">${escapeHTML(text)}</pre>`;
  }
  return detailCode(text);
}

function objectFieldValueText(value) {
  if (value === null) {
    return { text: "null", block: false };
  }
  if (value === undefined) {
    return { text: "undefined", block: false };
  }
  if (typeof value === "string") {
    return { text: value === "" ? '""' : value, block: false };
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return { text: String(value), block: false };
  }
  if (Array.isArray(value) || typeof value === "object") {
    const compact = safeJSONStringify(value);
    if (compact.length <= 96 && !compact.includes("\\n")) {
      return { text: compact, block: false };
    }
    return { text: safeJSONStringify(value, 2), block: true };
  }
  return { text: String(value), block: false };
}

function safeJSONStringify(value, spacing = 0) {
  try {
    return JSON.stringify(value, null, spacing) || String(value);
  } catch (_error) {
    return String(value);
  }
}

function deviceSummaryForEntity(entityID, row, fallback = "Unknown") {
  const devices = deviceRowsForEntity(entityID, row);
  if (!devices.length) {
    return fallback;
  }
  const links = devices.map((device) => {
    const href = entityPath("devices", device.routeID);
    return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(deviceTitle(device))}</a>`;
  });
  if (links.length === 1) {
    return links[0];
  }
  return `<div class="v3-detail-tags">${links.map((link) => `<span>${link}</span>`).join("")}</div>`;
}

function deviceClientPlatform(client) {
  const os = String(client?.clientOS || "").trim();
  const arch = String(client?.clientArch || "").trim();
  if (os && arch) {
    return `${os} ${arch}`;
  }
  return os || arch || "";
}

function deviceClientSessionCell(client) {
  return sessionCellByID(client?.sessionID, client?.traceID, "");
}

function shortMachineID(value, width = 6) {
  const text = String(value || "").toLowerCase().replaceAll(/[^a-z0-9]+/g, "");
  if (!text) {
    return "";
  }
  return text.slice(0, width);
}

function deviceTitle(row) {
  const label = String(row?.name || "").trim();
  if (label) {
    return label;
  }
  const machineID = shortMachineID(row?.machineID || row?.id || "");
  if (machineID) {
    return `Device ${machineID}`;
  }
  return "Device";
}

function deviceClientRows(row) {
  const rows = Array.isArray(row?.clients) ? row.clients.slice() : [];
  return rows.sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
}

function devicePlatforms(row) {
  const values = new Set();
  for (const client of deviceClientRows(row)) {
    const os = String(client?.clientOS || "").trim();
    const arch = String(client?.clientArch || "").trim();
    const value = [os, arch].filter(Boolean).join(" ");
    if (value) {
      values.add(value);
    }
  }
  return Array.from(values);
}

function deviceSubtitle(row) {
  const platforms = devicePlatforms(row);
  if (platforms.length === 1) {
    return platforms[0];
  }
  if (platforms.length > 1) {
    return `${platforms[0]} +${platforms.length - 1}`;
  }
  const latest = deviceClientRows(row)[0];
  if (latest?.name) {
    return latest.name;
  }
  return "";
}

function workspaceRootNeedsHostQualifier(root) {
  const text = String(root || "").trim();
  return text.startsWith("/") || /^[a-z]:[\\/]/i.test(text);
}

function overviewEntities() {
  return entities.filter((entity) => !entity.navHidden && Boolean(liveDomainConfigs[entity.id]));
}

function overviewCards() {
  return overviewEntities().map((entity) => {
    const materialized = materializeEntity(entity);
    const config = liveDomainConfigs[entity.id];
    const live = state.live[config.stateKey];
    const items = visibleEntityRows(materialized)
      .slice()
      .sort((left, right) => overviewItemUnixNano(right) - overviewItemUnixNano(left));
    const recentItems = items
      .slice()
      .slice(0, 3)
      .map((row) => ({
        href: overviewItemHref(materialized, row),
        label: overviewItemLabel(materialized, row),
        status: overviewItemStatus(materialized, row),
        timeLabel: relativeTimeFromNow(overviewItemUnixNano(row)),
      }));
    return {
      id: entity.id,
      label: entity.label,
      href: entityPath(entity.id),
      state: live.status,
      count: live.status === "loaded" ? items.length : 0,
      items: recentItems,
      emptyCopy: overviewEmptyCopy(entity.label, live),
      copy: overviewCardCopy(entity.label, live, items.length),
    };
  });
}

function overviewCardCopy(label, live, count) {
  if (live.status === "error") {
    return live.error || `${label} failed to load.`;
  }
  if (live.status === "idle" || live.status === "loading") {
    return `Loading live ${label.toLowerCase()} from the API.`;
  }
  if (count === 0) {
    return `No ${label.toLowerCase()} detected in the current dataset.`;
  }
  return `${count} live ${label.toLowerCase()} detected.`;
}

function overviewEmptyCopy(label, live) {
  if (live.status === "error") {
    return live.error || `${label} are unavailable right now.`;
  }
  if (live.status === "idle" || live.status === "loading") {
    return `Loading ${label.toLowerCase()}...`;
  }
  return `No ${label.toLowerCase()} detected.`;
}

function overviewDomainStatePill(card) {
  switch (card.state) {
    case "loaded":
      return `<span class="v3-overview-card-state">${statusOrb("live")}</span>`;
    case "error":
      return `<span class="v3-overview-card-state">${statusOrb("error")}</span>`;
    default:
      return `<span class="v3-overview-card-state">${statusOrb("loading")}</span>`;
  }
}

function overviewItemUnixNano(row) {
  const keys = [
    "lastActivityUnixNano",
    "lastSeenUnixNano",
    "updatedUnixNano",
    "endUnixNano",
    "startUnixNano",
    "firstSeenUnixNano",
  ];
  for (const key of keys) {
    const value = Number(row?.[key] || 0);
    if (value > 0) {
      return value;
    }
  }
  return 0;
}

function overviewItemHref(entity, row) {
  if (row?.routeID && supportsDetailRoute(entity.id)) {
    return entityPath(entity.id, row.routeID);
  }
  return entityPath(entity.id);
}

function overviewItemLabel(entity, row) {
  switch (entity.id) {
    case "pipelines":
      return row.command || row.name || "Pipeline";
    case "calls":
      return callTitle(row);
    case "functions":
      return functionTitle(row);
    case "devices":
      return deviceTitle(row);
    case "objects":
      return objectTitle(row);
    case "object-types":
      return row.name || "Type";
    case "modules":
      return moduleTitle(row);
    case "sessions":
      return sessionDisplayName(row);
    case "workspaces":
      return row.name || row.root || "Workspace";
    case "git-remotes":
      return row.ref || row.name || "Git Remote";
    case "registries":
      return row.ref || row.name || "Registry";
    case "terminals":
      return row.name || row.entryLabel || row.callName || "Terminal";
    case "repls":
      return row.command || row.name || "Repl";
    case "checks":
      return row.name || row.spanName || "Check";
    case "services":
      return row.name || row.imageRef || "Service";
    default:
      return row.name || row.id || entity.label;
  }
}

function sessionDisplayName(row) {
  const explicit = String(row?.name || "").trim();
  if (explicit) {
    return explicit;
  }
  return shortID(row?.id) || "Session";
}

function sessionDisplaySubtitle(row) {
  const clientCount = Number(row?.clientCount || 0);
  if (clientCount > 0) {
    return `${clientCount} client${clientCount === 1 ? "" : "s"}`;
  }
  return "";
}

function overviewItemStatus(entity, row) {
  if (entity.id === "sessions") {
    return sessionStatusLabel(row);
  }
  if (entity.id === "calls") {
    return row.statusCode || "";
  }
  return row.status || "";
}

function detailPageTitle(entity, row) {
  switch (entity.id) {
    case "calls":
      return callSignatureText(row);
    case "functions":
      return functionTitle(row);
    case "clients":
      return clientCommandText(row);
    default:
      return overviewItemLabel(entity, row);
  }
}

function detailPageMeta(entity, row) {
  switch (entity.id) {
    case "calls":
      return callTableSubtitle(row) || (row.returnType ? `returns ${row.returnType}` : "Function call");
    case "functions":
      return functionSubtitle(row) || "Function";
    case "objects":
      return row.typeName || "Object snapshot";
    case "object-types":
      return objectTypeModuleRef(row) || "Object type";
    case "modules":
      return moduleSubtitle(row) || "Module";
    case "clients":
      return clientCommandSubtitle(row) || "Client";
    case "sessions":
      return sessionStatusLabel(row) || "Session";
    case "devices":
      return deviceSubtitle(row) || "Device";
    default:
      return overviewItemStatus(entity, row) || entity.label;
  }
}

function currentDetailItem(entity) {
  if (!state.detailID || !entity || !Array.isArray(entity.liveItems)) {
    return null;
  }
  return entity.liveItems.find((item) => item.routeID === state.detailID) || null;
}

function supportsDetailRoute(entityID) {
  return Boolean(liveDomainConfigs[entityID]);
}

function materializeEntity(entity) {
  if (!entity || (entity.id !== "terminals" && entity.id !== "repls" && entity.id !== "checks" && entity.id !== "workspaces" && entity.id !== "devices" && entity.id !== "clients" && entity.id !== "calls" && entity.id !== "functions" && entity.id !== "objects" && entity.id !== "object-types" && entity.id !== "modules" && entity.id !== "services" && entity.id !== "sessions" && entity.id !== "pipelines" && entity.id !== "shells" && entity.id !== "workspace-ops" && entity.id !== "git-remotes" && entity.id !== "registries")) {
    return entity;
  }
  const config = liveDomainConfigs[entity.id];
  const live = state.live[config.stateKey];
  if (live.status === "loaded") {
    if (entity.id === "terminals") {
      return buildLiveTerminalsEntity(entity, live.items);
    }
    if (entity.id === "repls") {
      return buildLiveReplsEntity(entity, live.items);
    }
    if (entity.id === "checks") {
      return buildLiveChecksEntity(entity, live.items);
    }
    if (entity.id === "workspaces") {
      return buildLiveWorkspacesEntity(entity, live.items);
    }
    if (entity.id === "devices") {
      return buildLiveDevicesEntity(entity, live.items);
    }
    if (entity.id === "clients") {
      return buildLiveClientsEntity(entity, live.items);
    }
    if (entity.id === "calls") {
      return buildLiveCallsEntity(entity, live.items);
    }
    if (entity.id === "functions") {
      return buildLiveFunctionsEntity(entity, live.items);
    }
    if (entity.id === "objects") {
      return buildLiveObjectsEntity(entity, live.items);
    }
    if (entity.id === "object-types") {
      return buildLiveObjectTypesEntity(entity, live.items);
    }
    if (entity.id === "modules") {
      return buildLiveModulesEntity(entity, live.items);
    }
    if (entity.id === "services") {
      return buildLiveServicesEntity(entity, live.items);
    }
    if (entity.id === "sessions") {
      return buildLiveSessionsEntity(entity, live.items);
    }
    if (entity.id === "pipelines") {
      return buildLivePipelinesEntity(entity, live.items);
    }
    if (entity.id === "workspace-ops") {
      return buildLiveWorkspaceOpsEntity(entity, live.items);
    }
    if (entity.id === "git-remotes") {
      return buildLiveGitRemotesEntity(entity, live.items);
    }
    if (entity.id === "registries") {
      return buildLiveRegistriesEntity(entity, live.items);
    }
    return buildLiveShellsEntity(entity, live.items);
  }
  if (live.status === "idle" || live.status === "loading") {
    const pendingLabel = live.status === "idle" ? "Pending" : "Loading";
    return {
      ...entity,
      dynamicKind: entity.id,
      liveItems: [],
      blurb: `Fetching live ${config.label.toLowerCase()} from ${config.endpoint}. The shell stays entity-first while this domain hydrates from real API data.`,
      metrics: [
        { label: "Live fetch", value: pendingLabel, detail: `Requesting ${config.label} from the real API.` },
        { label: "Entity mode", value: "Loading", detail: `${config.label} are hydrating from real data.` },
        { label: "Next step", value: "Render", detail: "Wire the current domain end-to-end before generalizing further." },
      ],
      highlights: [{ title: config.label, value: pendingLabel, note: "Waiting for live entity payload." }],
      signals: [{ label: "API state", value: pendingLabel, tone: "neutral", detail: `${config.label} are still loading.` }],
      evidence: [],
      relations: [],
      inventory: [],
    };
  }
  if (live.status === "error") {
    return {
      ...entity,
      dynamicKind: entity.id,
      liveItems: [],
      blurb: `Live ${config.label.toLowerCase()} fetch failed (${live.error}). Falling back to the entity shell while the API connection is unavailable.`,
      metrics: [
        { label: "Live fetch", value: "Error", detail: live.error || "Unknown failure" },
        { label: "Fallback", value: "Inventory shell", detail: `${config.label} stayed isolated while the rest of the shell remained usable.` },
        { label: "Recovery", value: "Retry", detail: "Reload the page after the server/API is healthy." },
      ],
      highlights: [{ title: config.label, value: "Error", note: live.error || "Unknown failure" }],
      signals: [{ label: "API state", value: "Unavailable", tone: "warn", detail: `The shell could not reach ${config.endpoint}.` }],
      evidence: [],
      relations: [],
      inventory: [],
    };
  }
  return entity;
}

function buildLiveTerminalsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.callID || item.id),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const failedCount = liveItems.filter((item) => item.status === "failed").length;
  const execCount = liveItems.reduce((sum, item) => sum + Number(item.execCount || 0), 0);
  const activityCount = liveItems.reduce((sum, item) => sum + Number(item.activityCount || 0), 0);

  return {
    ...base,
    dynamicKind: "terminals",
    liveItems,
    blurb:
      "This domain is now live. Each row is one explicit Container.terminal call, with contained exec activity attached directly to the terminal instead of being flattened into generic span noise.",
    metrics: [
      {
        label: "Detected terminals",
        value: String(liveItems.length),
        detail: `${failedCount} with failed contained activity`,
      },
      {
        label: "Exec activity",
        value: String(execCount),
        detail: `${activityCount} total attached terminal activity spans`,
      },
      {
        label: "Sessions",
        value: String(new Set(liveItems.map((item) => item.sessionID).filter(Boolean)).size),
        detail: "Distinct sessions currently represented by terminal entities.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name || item.entryLabel || "Terminal",
      value: String(item.status || "ready").toUpperCase(),
      note: `${item.execCount || 0} exec spans · ${item.callName || "Container.terminal"}`,
    })),
    signals: [
      {
        label: "Failures",
        value: String(failedCount),
        tone: failedCount > 0 ? "warn" : "good",
        detail: "Terminal entities with failing contained activity.",
      },
      {
        label: "Exec spans",
        value: String(execCount),
        tone: execCount > 0 ? "neutral" : "good",
        detail: "Descendant exec spans captured inside terminal windows.",
      },
      {
        label: "Container reuse",
        value: String(liveItems.filter((item) => item.receiverDagqlID && item.receiverDagqlID === item.outputDagqlID).length),
        tone: "neutral",
        detail: "Terminal calls whose output container identity stayed the same.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLivePipelinesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID("", item.id || "") || shortRouteID(item.traceID, item.terminalCallID || item.clientID),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const durations = liveItems.map((item) => Math.max(0, Number(item.endUnixNano || 0) - Number(item.startUnixNano || 0)));
  const failedCount = liveItems.filter((item) => item.status === "failed").length;
  const runningCount = liveItems.filter((item) => item.status === "running").length;
  const followupTotal = liveItems.reduce((sum, item) => sum + Number(item.followupSpanCount || 0), 0);
  const attachedCount = liveItems.filter((item) => Number(item.followupSpanCount || 0) > 0).length;
  const changesetCount = liveItems.filter((item) => item.terminalReturnType === "Changeset").length;
  const objectReturnCount = liveItems.filter((item) => item.terminalOutputDagqlID).length;
  const evidence = [];
  const relations = [];

  for (const item of liveItems) {
    for (const row of item.evidence || []) {
      evidence.push({
        runName: item.name,
        kind: row.kind,
        confidence: row.confidence,
        source: row.source,
        note: row.note,
      });
    }
    for (const rel of item.relations || []) {
      relations.push({
        source: item.name,
        relation: rel.relation,
        target: rel.target,
        targetKind: rel.targetKind,
        note: rel.note || rel.targetKind || "",
      });
    }
  }

  return {
    ...base,
    dynamicKind: "pipelines",
    liveItems,
    blurb:
      "This domain is now live. Each row is one submitted pipeline: either a `dagger call` chain or one shell-submitted command line, with terminal output and attached follow-up behavior kept together.",
    metrics: [
      {
        label: "Detected pipelines",
        value: String(liveItems.length),
        detail: `${failedCount} failed | ${runningCount} running`,
      },
      {
        label: "Median duration",
        value: formatDuration(median(durations)),
        detail: "Based on real client-owned call chains.",
      },
      {
        label: "Follow-up spans",
        value: String(followupTotal),
        detail: `${attachedCount} pipelines with attached CLI follow-up`,
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name,
      value: item.status.toUpperCase(),
      note: `${item.terminalReturnType || "Unknown"} output; ${summarizeKinds(item.postProcessKinds)}`,
    })),
    signals: [
      {
        label: "Changeset returns",
        value: String(changesetCount),
        tone: changesetCount > 0 ? "neutral" : "good",
        detail: "These are the runs most likely to generate CLI-managed follow-up spans.",
      },
      {
        label: "Attachment rate",
        value: `${attachedCount}/${liveItems.length || 0}`,
        tone: attachedCount > 0 ? "good" : "neutral",
        detail: "Runs with post-processing or follow-up spans still attached to the same entity.",
      },
      {
        label: "Object outputs",
        value: String(objectReturnCount),
        tone: "neutral",
        detail: "Runs ending in object-valued outputs rather than plain values.",
      },
    ],
    evidence,
    relations,
    inventory: liveItems,
  };
}

function buildLiveReplsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: sessionRouteID(item.traceID, item.sessionID || item.rootClientID || item.clientID || item.id),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const failedCount = liveItems.filter((item) => item.status === "failed").length;
  const commandTotal = liveItems.reduce((sum, item) => sum + Number(item.commandCount || 0), 0);
  const sessionCount = new Set(liveItems.map((item) => item.sessionID).filter(Boolean)).size;
  const durations = liveItems.map((item) => Math.max(0, Number(item.endUnixNano || 0) - Number(item.startUnixNano || 0)));

  return {
    ...base,
    dynamicKind: "repls",
    liveItems,
    blurb:
      "This domain is now live. Each row is one shell-command history surface, discovered directly from `dagger.io/shell.handler.args` spans grouped into one derived session.",
    metrics: [
      {
        label: "Detected repls",
        value: String(liveItems.length),
        detail: `${failedCount} failed | ${commandTotal} commands`,
      },
      {
        label: "Sessions",
        value: String(sessionCount || liveItems.length),
        detail: "Derived execution sessions represented by the current REPL set.",
      },
      {
        label: "Median duration",
        value: formatDuration(median(durations)),
        detail: "Based on real shell handler history windows.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.command || item.name,
      value: String(item.commandCount || 0),
      note: item.lastCommand || item.firstCommand || "No commands",
    })),
    signals: [
      {
        label: "Failures",
        value: String(failedCount),
        tone: failedCount > 0 ? "warn" : "good",
        detail: "REPL histories containing a failed submitted command.",
      },
      {
        label: "Command volume",
        value: String(commandTotal),
        tone: "neutral",
        detail: "Submitted shell handler commands in the current result set.",
      },
      {
        label: "Session spread",
        value: String(sessionCount || liveItems.length),
        tone: "neutral",
        detail: "Derived sessions represented by these REPL histories.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveChecksEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.spanID || item.id),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const failedCount = liveItems.filter((item) => item.status === "failed").length;

  return {
    ...base,
    dynamicKind: "checks",
    liveItems,
    blurb:
      "This domain is now live. Each row is one authoritative check span surfaced only when telemetry explicitly sets `dagger.io/check.name`.",
    metrics: [
      {
        label: "Detected checks",
        value: String(liveItems.length),
        detail: `${failedCount} failed`,
      },
      {
        label: "Sessions",
        value: String(new Set(liveItems.map((item) => item.sessionID).filter(Boolean)).size),
        detail: "Derived sessions represented by explicit check spans.",
      },
      {
        label: "Signal source",
        value: "Explicit attrs",
        detail: "No name-only heuristics are used for checks.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name,
      value: String(item.status || "ready").toUpperCase(),
      note: item.spanName || item.name,
    })),
    signals: [
      {
        label: "Failures",
        value: String(failedCount),
        tone: failedCount > 0 ? "warn" : "good",
        detail: "Explicit check spans reporting failure.",
      },
      {
        label: "Empty is honest",
        value: liveItems.length === 0 ? "Yes" : "No",
        tone: liveItems.length === 0 ? "neutral" : "good",
        detail: "Checks stay empty when the dataset contains no explicit check telemetry.",
      },
      {
        label: "Heuristic drift",
        value: "None",
        tone: "good",
        detail: "Checks are only derived from explicit check attrs.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveWorkspacesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.root, item.id || item.root),
      hostQualifier: workspaceHostQualifier(item),
    }))
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const pipelineCount = liveItems.reduce((sum, item) => sum + Number(item.pipelineCount || 0), 0);
  const opCount = liveItems.reduce((sum, item) => sum + Number(item.opCount || 0), 0);
  const sessionCount = liveItems.reduce((sum, item) => sum + Number(item.sessionCount || 0), 0);

  return {
    ...base,
    dynamicKind: "workspaces",
    liveItems,
    blurb:
      "This domain is now live as inferred workspace roots. Each row is one absolute root anchored either by module-source provenance or by repeated absolute host workspace ops, with relative ops attached only when one root is unambiguous.",
    metrics: [
      {
        label: "Observed roots",
        value: String(liveItems.length),
        detail: `${opCount} attached ops across ${sessionCount} sessions`,
      },
      {
        label: "Pipelines",
        value: String(pipelineCount),
        detail: "Pipelines attached through workspace ops.",
      },
      {
        label: "Last activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently observed workspace root.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name || item.root,
      value: String(item.opCount || 0),
      note: item.root,
    })),
    signals: [
      {
        label: "Write pressure",
        value: String(liveItems.reduce((sum, item) => sum + Number(item.writeCount || 0), 0)),
        tone: "neutral",
        detail: "Export-style operations attached to observed workspace roots.",
      },
      {
        label: "Session spread",
        value: String(sessionCount),
        tone: "neutral",
        detail: "Sessions represented by current workspace roots.",
      },
      {
        label: "Observed only",
        value: "Yes",
        tone: "warn",
        detail: "These are observed roots derived from ops, not canonical Workspace objects yet.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveDevicesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.machineID || item.id || ""),
      name: deviceTitle(item),
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const sessionTotal = liveItems.reduce((sum, item) => sum + Number(item.sessionCount || 0), 0);
  const clientTotal = liveItems.reduce((sum, item) => sum + Number(item.clientCount || 0), 0);
  const osCount = new Set(
    liveItems.flatMap((item) =>
      Array.isArray(item?.clients) ? item.clients.map((client) => String(client?.clientOS || "").trim()).filter(Boolean) : [],
    ),
  ).size;

  return {
    ...base,
    dynamicKind: "devices",
    liveItems,
    blurb:
      "This domain is now live. Each row is one anonymous host identity derived only from top-level clients, then used to attach the sessions, pipelines, and local workspaces that originated from that machine.",
    metrics: [
      {
        label: "Detected devices",
        value: String(liveItems.length),
        detail: `${clientTotal} top-level clients across ${sessionTotal} sessions`,
      },
      {
        label: "Host spread",
        value: String(osCount || 0),
        detail: "Distinct client OS variants seen in current device rows.",
      },
      {
        label: "Last activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently active top-level host identity.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: deviceTitle(item),
      value: String(item.sessionCount || 0),
      note: deviceSubtitle(item) || `${item.traceCount || 0} traces`,
    })),
    signals: [
      {
        label: "Root-only derivation",
        value: "Yes",
        tone: "good",
        detail: "Nested module/runtime clients do not produce standalone device identities.",
      },
      {
        label: "Session spread",
        value: String(sessionTotal),
        tone: "neutral",
        detail: "Derived sessions attached through top-level client ownership.",
      },
      {
        label: "Client fan-in",
        value: String(clientTotal),
        tone: clientTotal > liveItems.length ? "neutral" : "good",
        detail: "Repeated top-level clients collapsed onto stable host identities.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveClientsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.id || ""),
      name: clientCommandText(item),
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const nestedCount = liveItems.filter((item) => clientIsNested(item)).length;
  const sdkCount = liveItems.filter((item) => String(item?.sdkName || "").trim()).length;
  const moduleCount = liveItems.filter((item) => clientLooksLikeModule(item)).length;
  const callTotal = liveItems.reduce((sum, item) => sum + Number(item.callCount || 0), 0);

  return {
    ...base,
    dynamicKind: "clients",
    liveItems,
    blurb:
      "This domain is now live. Each row is one execution client derived from an engine connect span, carrying command identity, parent/root ownership, SDK labels when declared, and owned DAGQL call counts.",
    metrics: [
      {
        label: "Detected clients",
        value: String(liveItems.length),
        detail: `${nestedCount} nested | ${moduleCount} module runtimes`,
      },
      {
        label: "SDK-labeled",
        value: String(sdkCount),
        detail: "Clients that declared dagger.io/sdk.name in telemetry.",
      },
      {
        label: "Owned calls",
        value: String(callTotal),
        detail: "Total DAGQL calls currently attached to client rows.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: clientCommandText(item),
      value: clientIsNested(item) ? "NESTED" : "ROOT",
      note: `${item.callCount || 0} calls · ${clientCommandSubtitle(item) || "client"}`,
    })),
    signals: [
      {
        label: "Nested fanout",
        value: String(nestedCount),
        tone: nestedCount > 0 ? "neutral" : "good",
        detail: "Child runtimes remain visible instead of collapsing into sessions only.",
      },
      {
        label: "Module runtimes",
        value: String(moduleCount),
        tone: moduleCount > 0 ? "neutral" : "good",
        detail: "Nested non-CLI runtimes that look like module execution clients.",
      },
      {
        label: "SDK coverage",
        value: `${sdkCount}/${liveItems.length || 0}`,
        tone: sdkCount > 0 ? "good" : "neutral",
        detail: "How often clients declared their SDK explicitly.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveCallsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.id || ""),
      name: callTitle(item),
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const outputCount = liveItems.filter((item) => item.outputDagqlID).length;
  const receiverCount = liveItems.filter((item) => item.receiverDagqlID).length;
  return {
    ...base,
    dynamicKind: "calls",
    liveItems,
    blurb:
      "This domain is now live. Each row is one semantic call span, with parent-call edges and receiver/output DAGQL links preserved as first-class relationships.",
    metrics: [
      {
        label: "Calls",
        value: String(liveItems.length),
        detail: `${outputCount} with output snapshots`,
      },
      {
        label: "Receivers",
        value: String(receiverCount),
        detail: "Calls that were invoked on an existing object snapshot.",
      },
      {
        label: "Latest activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].startUnixNano) : "none",
        detail: "Most recently observed call in the current result set.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: callTitle(item),
      value: item.returnType || "Void",
      note: callSubtitle(item) || "call",
    })),
    signals: [],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveFunctionsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.id || ""),
      name: functionTitle(item),
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  return {
    ...base,
    dynamicKind: "functions",
    liveItems,
    blurb:
      "This domain is now live. Each row is one canonical function identity, with module ownership, attached calls, and optional function metadata snapshots kept together instead of being split between call spans and object snapshots.",
    metrics: [
      {
        label: "Functions",
        value: String(liveItems.length),
        detail: `${liveItems.reduce((sum, item) => sum + Number(item.callCount || 0), 0)} attached calls`,
      },
      {
        label: "Snapshots",
        value: String(liveItems.reduce((sum, item) => sum + Number(item.snapshotCount || 0), 0)),
        detail: "Recorded Function object snapshots attached to canonical function rows.",
      },
      {
        label: "Latest activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently observed function activity.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: functionTitle(item),
      value: item.returnType || "Void",
      note: functionSubtitle(item),
    })),
    signals: [],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveObjectsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.dagqlID || ""),
      name: objectTitle(item),
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const fieldRefCount = liveItems.reduce((sum, item) => sum + (Array.isArray(item.fieldRefs) ? item.fieldRefs.length : 0), 0);
  return {
    ...base,
    dynamicKind: "objects",
    liveItems,
    blurb:
      "This domain is now live. Each row is one immutable DAGQL snapshot, with field refs and producing call links surfaced directly instead of being hidden inside a graph overlay.",
    metrics: [
      {
        label: "Objects",
        value: String(liveItems.length),
        detail: `${fieldRefCount} field refs across current snapshots`,
      },
      {
        label: "Typed snapshots",
        value: String(new Set(liveItems.map((item) => item.typeName).filter(Boolean)).size),
        detail: "Distinct type names represented by current snapshots.",
      },
      {
        label: "Latest activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently observed object snapshot.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: objectTitle(item),
      value: item.typeName || "Object",
      note: item.fieldRefs?.length ? `${item.fieldRefs.length} refs` : "no refs",
    })),
    signals: [],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveObjectTypesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.id || ""),
      name: String(item.name || "").trim() || "Type",
    }))
    .filter((item) => item.routeID)
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  return {
    ...base,
    dynamicKind: "object-types",
    liveItems,
    blurb:
      "This domain is now live. Each row is one aggregated type name, combining real snapshots with function metadata that returns that type, plus an optional module dependency when provenance is honest.",
    metrics: [
      {
        label: "Types",
        value: String(liveItems.length),
        detail: `${liveItems.reduce((sum, item) => sum + Number(item.functionCount || 0), 0)} function return observations`,
      },
      {
        label: "Module-backed",
        value: String(liveItems.filter((item) => Array.isArray(item.moduleRefs) && item.moduleRefs.length > 0).length),
        detail: "Types with unambiguous module dependency links.",
      },
      {
        label: "Latest activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently observed type activity.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name,
      value: String(item.snapshotCount || 0),
      note: `${item.functionCount || 0} functions`,
    })),
    signals: [],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveModulesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: String(item.ref || ""),
      name: moduleTitle(item),
    }))
    .filter((item) => item.routeID);
  if (!liveItems.some((item) => String(item.ref || "") === "core")) {
    liveItems.push(syntheticCoreModuleItem());
  }
  liveItems.sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  return {
    ...base,
    dynamicKind: "modules",
    liveItems,
    blurb:
      "This domain is now live. Each row is one loaded module reference, with prelude setup calls retained so custom types can depend on a concrete schema source page.",
    metrics: [
      {
        label: "Modules",
        value: String(liveItems.length),
        detail: `${liveItems.reduce((sum, item) => sum + (Array.isArray(item.callIDs) ? item.callIDs.length : 0), 0)} prelude calls`,
      },
      {
        label: "Traces",
        value: String(new Set(liveItems.flatMap((item) => item.traceIDs || [])).size),
        detail: "Distinct traces containing loaded modules.",
      },
      {
        label: "Latest activity",
        value: liveItems.length ? relativeTimeFromNow(liveItems[0].lastSeenUnixNano) : "none",
        detail: "Most recently seen module load lane.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: moduleTitle(item),
      value: String(item.callIDs?.length || 0),
      note: moduleSubtitle(item),
    })),
    signals: [],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function syntheticCoreModuleItem() {
  const functions = Array.isArray(state.live.functions?.items)
    ? state.live.functions.items.filter((item) => String(item?.moduleRef || "").trim() === "core")
    : [];
  const objectTypes = Array.isArray(state.live.objectTypes?.items)
    ? state.live.objectTypes.items.filter((item) => objectTypeModuleRef(item) === "core")
    : [];
  const traces = new Set();
  const sessions = new Set();
  const clients = new Set();
  let firstSeenUnixNano = 0;
  let lastSeenUnixNano = 0;
  for (const item of [...functions, ...objectTypes]) {
    for (const traceID of Array.isArray(item?.traceIDs) ? item.traceIDs : []) {
      if (traceID) {
        traces.add(String(traceID));
      }
    }
    for (const sessionID of Array.isArray(item?.sessionIDs) ? item.sessionIDs : []) {
      if (sessionID) {
        sessions.add(String(sessionID));
      }
    }
    for (const clientID of Array.isArray(item?.clientIDs) ? item.clientIDs : []) {
      if (clientID) {
        clients.add(String(clientID));
      }
    }
    const itemFirst = Number(item?.firstSeenUnixNano || 0);
    const itemLast = Number(item?.lastSeenUnixNano || 0);
    if (!firstSeenUnixNano || (itemFirst > 0 && itemFirst < firstSeenUnixNano)) {
      firstSeenUnixNano = itemFirst;
    }
    if (itemLast > lastSeenUnixNano) {
      lastSeenUnixNano = itemLast;
    }
  }
  return {
    id: "module:core",
    ref: "core",
    routeID: "core",
    name: "Core",
    resolvedRefs: [],
    traceIDs: Array.from(traces),
    sessionIDs: Array.from(sessions),
    clientIDs: Array.from(clients),
    callIDs: [],
    traceCount: traces.size,
    sessionCount: sessions.size,
    clientCount: clients.size,
    firstSeenUnixNano,
    lastSeenUnixNano,
  };
}

function buildLiveServicesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.dagqlID || item.id),
    }))
    .sort((a, b) => Number(b.lastActivityUnixNano || 0) - Number(a.lastActivityUnixNano || 0));
  const runningCount = liveItems.filter((item) => item.status === "running").length;
  const failedCount = liveItems.filter((item) => item.status === "failed").length;
  const kindCount = new Set(liveItems.map((item) => item.kind).filter(Boolean)).size;

  return {
    ...base,
    dynamicKind: "services",
    liveItems,
    blurb:
      "This domain is now live. Each row is one authoritative Service object snapshot, with service-specific activity layered on top from related DAGQL calls rather than from the deprecated generic render layer.",
    metrics: [
      {
        label: "Detected services",
        value: String(liveItems.length),
        detail: `${runningCount} running | ${failedCount} failed`,
      },
      {
        label: "Kinds",
        value: String(kindCount),
        detail: "Distinct service shapes in the current result set.",
      },
      {
        label: "Activity rows",
        value: String(liveItems.reduce((sum, item) => sum + (item.activity || []).length, 0)),
        detail: "Related service calls currently attached to service detail pages.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name,
      value: String(item.status || "created").toUpperCase(),
      note: `${item.kind || "service"} · ${item.createdByCallName || "unknown creator"}`,
    })),
    signals: [
      {
        label: "Failures",
        value: String(failedCount),
        tone: failedCount > 0 ? "warn" : "good",
        detail: "Services whose latest lifecycle activity failed.",
      },
      {
        label: "Running",
        value: String(runningCount),
        tone: runningCount > 0 ? "neutral" : "good",
        detail: "Services with open or still-running lifecycle activity.",
      },
      {
        label: "Kinds",
        value: String(kindCount),
        tone: "neutral",
        detail: "Different service shapes currently detected.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveSessionsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: sessionRouteID(item.traceID, item.id),
    }))
    .sort((a, b) => Number(b.firstSeenUnixNano || 0) - Number(a.firstSeenUnixNano || 0));
  const durations = liveItems.map((item) => Math.max(0, Number(item.lastSeenUnixNano || 0) - Number(item.firstSeenUnixNano || 0)));
  const openCount = liveItems.filter((item) => item.open).length;
  const fallbackCount = liveItems.filter((item) => item.fallback).length;

  return {
    ...base,
    dynamicKind: "sessions",
    liveItems,
    blurb:
      "This domain is now live. Each row is one derived execution session rooted at a root client, with trace retained as container context rather than as the primary UI identity.",
    metrics: [
      {
        label: "Detected sessions",
        value: String(liveItems.length),
        detail: `${openCount} open | ${fallbackCount} fallback`,
      },
      {
        label: "Median duration",
        value: formatDuration(median(durations)),
        detail: "Based on real derived sessions.",
      },
      {
        label: "Open sessions",
        value: String(openCount),
        detail: "Sessions still ingesting or otherwise active.",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: sessionDisplayName(item),
      value: String(sessionStatusLabel(item)).toUpperCase(),
      note: `${shortID(item.rootClientID)} root client in trace ${shortID(item.traceID)}`,
    })),
    signals: [
      {
        label: "Fallback sessions",
        value: String(fallbackCount),
        tone: fallbackCount > 0 ? "warn" : "good",
        detail: "Fallback sessions appear when root-client derivation is unavailable.",
      },
      {
        label: "Open pressure",
        value: String(openCount),
        tone: openCount > 0 ? "neutral" : "good",
        detail: "Sessions currently still active.",
      },
      {
        label: "Trace spread",
        value: String(new Set(liveItems.map((item) => item.traceID).filter(Boolean)).size),
        tone: "neutral",
        detail: "Distinct traces represented by the current sessions set.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveShellsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.rootClientID || item.clientID || item.id),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const durations = liveItems.map((item) => Math.max(0, Number(item.endUnixNano || 0) - Number(item.startUnixNano || 0)));
  const failedCount = liveItems.filter((item) => item.status === "failed").length;
  const runningCount = liveItems.filter((item) => item.status === "running").length;
  const childClientTotal = liveItems.reduce((sum, item) => sum + Number(item.childClientCount || 0), 0);
  const callTotal = liveItems.reduce((sum, item) => sum + Number(item.callCount || 0), 0);
  const inlineCount = liveItems.filter((item) => item.mode === "inline").length;
  const evidence = [];
  const relations = [];

  for (const item of liveItems) {
    for (const row of item.evidence || []) {
      evidence.push({
        shellName: item.name,
        kind: row.kind,
        confidence: row.confidence,
        source: row.source,
        note: row.note,
      });
    }
    for (const rel of item.relations || []) {
      relations.push({
        source: item.name,
        relation: rel.relation,
        target: rel.target,
        targetKind: rel.targetKind,
        note: rel.note || rel.targetKind || "",
      });
    }
  }

  return {
    ...base,
    dynamicKind: "shells",
    liveItems,
    blurb:
      "This domain is now live. Each row is one derived `dagger shell` session, anchored on a shell root client plus its descendant client tree rather than on one singular output value.",
    metrics: [
      {
        label: "Detected shells",
        value: String(liveItems.length),
        detail: `${failedCount} failed | ${runningCount} running`,
      },
      {
        label: "Median duration",
        value: formatDuration(median(durations)),
        detail: "Based on real shell root sessions.",
      },
      {
        label: "Descendant clients",
        value: String(childClientTotal),
        detail: `${callTotal} owned DAGQL calls across all shells`,
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.name,
      value: String(item.mode || "interactive").toUpperCase(),
      note: `${item.callCount} calls, ${item.childClientCount} child clients`,
    })),
    signals: [
      {
        label: "Inline shells",
        value: `${inlineCount}/${liveItems.length || 0}`,
        tone: inlineCount > 0 ? "neutral" : "good",
        detail: "Shell sessions started with `-c` rather than pure interactive mode.",
      },
      {
        label: "Descendant fanout",
        value: String(childClientTotal),
        tone: childClientTotal > 0 ? "good" : "neutral",
        detail: "Nested clients remain attached to their shell roots instead of becoming standalone entities.",
      },
      {
        label: "Activity density",
        value: String(callTotal),
        tone: "neutral",
        detail: "Owned DAGQL calls across all live shell sessions.",
      },
    ],
    evidence,
    relations,
    inventory: liveItems,
  };
}

function buildLiveWorkspaceOpsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.spanID || item.id),
    }))
    .sort((a, b) => Number(b.startUnixNano || 0) - Number(a.startUnixNano || 0));
  const durations = liveItems.map((item) => Math.max(0, Number(item.endUnixNano || 0) - Number(item.startUnixNano || 0)));
  const writeCount = liveItems.filter((item) => item.direction === "write").length;
  const attachedCount = liveItems.filter((item) => item.pipelineClientID).length;
  const pathCount = new Set(liveItems.map((item) => item.path).filter(Boolean)).size;

  return {
    ...base,
    dynamicKind: "workspace-ops",
    liveItems,
    blurb:
      "This domain is now live. Each row is one explicit host/export call derived from authoritative call spans, with pipeline attachment added only when the timing and client context make that association unambiguous.",
    metrics: [
      {
        label: "Detected ops",
        value: String(liveItems.length),
        detail: `${writeCount} writes | ${liveItems.length - writeCount} reads`,
      },
      {
        label: "Median duration",
        value: formatDuration(median(durations)),
        detail: "Based on real host/export calls.",
      },
      {
        label: "Pipeline attachment",
        value: `${attachedCount}/${liveItems.length || 0}`,
        detail: `${pathCount} distinct paths in the current result set`,
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.callName,
      value: String(item.direction || "op").toUpperCase(),
      note: item.path || item.kind,
    })),
    signals: [
      {
        label: "Write pressure",
        value: String(writeCount),
        tone: writeCount > 0 ? "warn" : "good",
        detail: "Export-style calls that materialize local changes.",
      },
      {
        label: "Path spread",
        value: String(pathCount),
        tone: "neutral",
        detail: "Distinct host paths referenced by current workspace ops.",
      },
      {
        label: "Pipeline links",
        value: String(attachedCount),
        tone: attachedCount > 0 ? "good" : "neutral",
        detail: "Workspace ops attached to a proven pipeline context.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveGitRemotesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      name: item.ref,
      routeID: shortRouteID(item.ref || item.id, item.host || item.ref || item.id),
    }))
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const pipelineCount = liveItems.reduce((sum, item) => sum + Number(item.pipelineCount || 0), 0);
  const sessionCount = liveItems.reduce((sum, item) => sum + Number(item.sessionCount || 0), 0);
  const hostCount = new Set(liveItems.map((item) => item.host).filter(Boolean)).size;
  const sourceKinds = new Set();
  for (const item of liveItems) {
    for (const kind of item.sourceKinds || []) {
      sourceKinds.add(kind);
    }
  }

  return {
    ...base,
    dynamicKind: "git-remotes",
    liveItems,
    blurb:
      "This domain is now live. Each row is one normalized remote repository identity discovered from authoritative module refs, load-module spans, and explicit git calls, with recent attached pipelines kept alongside it.",
    metrics: [
      {
        label: "Detected remotes",
        value: String(liveItems.length),
        detail: `${hostCount} hosts represented`,
      },
      {
        label: "Attached pipelines",
        value: String(pipelineCount),
        detail: `${sessionCount} sessions touched these remotes`,
      },
      {
        label: "Source kinds",
        value: String(sourceKinds.size),
        detail: Array.from(sourceKinds).sort().join(", ") || "No sources recorded",
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.ref,
      value: String(item.pipelineCount || 0),
      note: `${item.sessionCount || 0} sessions · ${item.host || "unknown host"}`,
    })),
    signals: [
      {
        label: "Host spread",
        value: String(hostCount),
        tone: hostCount > 1 ? "neutral" : "good",
        detail: "Distinct remote hosts in the current result set.",
      },
      {
        label: "Pipeline links",
        value: String(pipelineCount),
        tone: pipelineCount > 0 ? "good" : "neutral",
        detail: "Pipelines currently attached to a remote identity.",
      },
      {
        label: "Source diversity",
        value: String(sourceKinds.size),
        tone: sourceKinds.size > 1 ? "good" : "neutral",
        detail: "Different authoritative signals currently contributing to the domain.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function buildLiveRegistriesEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      name: item.ref,
      routeID: shortRouteID(item.ref || item.id, item.host || item.ref || item.id),
    }))
    .sort((a, b) => Number(b.lastSeenUnixNano || 0) - Number(a.lastSeenUnixNano || 0));
  const pipelineCount = liveItems.reduce((sum, item) => sum + Number(item.pipelineCount || 0), 0);
  const sessionCount = liveItems.reduce((sum, item) => sum + Number(item.sessionCount || 0), 0);
  const requestCount = liveItems.reduce((sum, item) => sum + Number(item.activityCount || 0), 0);
  const hostCount = new Set(liveItems.map((item) => item.host).filter(Boolean)).size;
  const sourceKinds = new Set();
  for (const item of liveItems) {
    for (const kind of item.sourceKinds || []) {
      sourceKinds.add(kind);
    }
  }

  return {
    ...base,
    dynamicKind: "registries",
    liveItems,
    blurb:
      "This domain is now live. Each row is one canonical registry repository identity derived from authoritative resolve spans, registry/auth requests, and explicit address-bearing calls, with activity kept attached rather than hidden behind generic object heuristics.",
    metrics: [
      {
        label: "Detected registries",
        value: String(liveItems.length),
        detail: `${hostCount} hosts represented`,
      },
      {
        label: "Attached pipelines",
        value: String(pipelineCount),
        detail: `${sessionCount} sessions touched these registries`,
      },
      {
        label: "Requests",
        value: String(requestCount),
        detail: `${sourceKinds.size} source kinds contributed to the current result set`,
      },
    ],
    highlights: liveItems.slice(0, 3).map((item) => ({
      title: item.ref,
      value: String(item.activityCount || 0),
      note: `${item.pipelineCount || 0} pipelines · ${item.lastOperation || "activity"}`,
    })),
    signals: [
      {
        label: "Host spread",
        value: String(hostCount),
        tone: hostCount > 1 ? "neutral" : "good",
        detail: "Distinct registry hosts represented in the current result set.",
      },
      {
        label: "Pipeline links",
        value: String(pipelineCount),
        tone: pipelineCount > 0 ? "good" : "neutral",
        detail: "Registry entities with at least one attached pipeline.",
      },
      {
        label: "Request volume",
        value: String(requestCount),
        tone: "neutral",
        detail: "Resolve, auth, and request activity currently attached to registry entities.",
      },
    ],
    evidence: [],
    relations: [],
    inventory: liveItems,
  };
}

function pipelineOutputCell(row) {
  const title = row.terminalReturnType || "Unknown";
  const subtitle = row.terminalOutputDagqlID || row.terminalObjectID || row.terminalCallName || "Plain value";
  return primaryCell(title, subtitle);
}

function pipelineOutputTypeLabel(row) {
  return row.terminalReturnType || "Plain value";
}

function pipelineFollowupCell(row) {
  const names = Array.isArray(row.followupSpanNames) ? row.followupSpanNames.filter(Boolean) : [];
  const title = row.followupSpanCount > 0 ? `${row.followupSpanCount} attached spans` : "No attached spans";
  const subtitle = names.length > 0 ? names.slice(0, 2).join(", ") : summarizeKinds(row.postProcessKinds);
  return primaryCell(title, subtitle);
}

function pipelineSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID, "None");
}

function gitRemotePipelineCountCell(row) {
  const count = Number(row.pipelineCount || 0);
  const latest = Array.isArray(row.pipelines) && row.pipelines.length > 0 ? row.pipelines[0] : null;
  const title = count === 1 ? "1 pipeline" : `${count} pipelines`;
  const subtitle = latest?.command || "No attached pipelines";
  return primaryCell(title, subtitle);
}

function gitRemotePipelineSummaryCell(row) {
  const href = pipelineEntityHref(row?.traceID, row?.pipelineID, row?.clientID);
  if (!href) {
    return primaryCell(row?.command || "Pipeline", "");
  }
  return linkedPrimaryCell(row.command || "Pipeline", "", href);
}

function gitRemotePipelineSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID, "None");
}

function registryPipelineCountCell(row) {
  const count = Number(row.pipelineCount || 0);
  const latest = Array.isArray(row.activities)
    ? row.activities.find((item) => item?.pipelineCommand)
    : null;
  const title = count === 1 ? "1 pipeline" : `${count} pipelines`;
  const subtitle = latest?.pipelineCommand || "No attached pipelines";
  return primaryCell(title, subtitle);
}

function registryActivityPipelineCell(row) {
  const href = pipelineEntityHref(row?.pipelineTraceID, row?.pipelineID, row?.pipelineClientID);
  if (!href) {
    return "None";
  }
  return linkedPrimaryCell(row.pipelineCommand || "Pipeline", "", href);
}

function terminalSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID);
}

function terminalSessionSummary(row) {
  return sessionInlineLinkByID(row?.sessionID, row?.traceID, "Unknown");
}

function terminalActivityCell(row) {
  const execCount = Number(row.execCount || 0);
  const count = Number(row.activityCount || 0);
  const title = execCount === 1 ? "1 exec" : `${execCount} execs`;
  const subtitle = count === execCount ? `${count} activity spans` : `${count} spans total`;
  return primaryCell(title, subtitle);
}

function serviceCreatedByCell(row) {
  return primaryCell(row.createdByCallName || "Unknown", row.producerLabel || servicePipelineLabel(row));
}

function serviceSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID, "None");
}

function serviceSessionSummary(row) {
  return sessionInlineLinkByID(row?.sessionID, row?.traceID);
}

function checkSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID, "None");
}

function checkSessionSummary(row) {
  return sessionInlineLinkByID(row?.sessionID, row?.traceID);
}

function servicePipelineSummary(row) {
  const href = pipelineEntityHref(row.traceID, row.pipelineID, row.pipelineClientID);
  if (!href) {
    return "None";
  }
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(servicePipelineLabel(row) || "Pipeline")}</a>`;
}

function servicePrimarySubtitle(row) {
  return row.imageRef || row.producerLabel || servicePipelineLabel(row);
}

function servicePipelineLabel(row) {
  return row.pipelineName || row.pipelineCommand || "";
}

function workspaceOpSessionCell(row) {
  return sessionCellByID(row?.sessionID, row?.traceID, "None");
}

function workspaceOpSessionSummary(row) {
  return sessionInlineLinkByID(row?.sessionID, row?.traceID);
}

function workspaceOpPipelineCell(row) {
  const href = pipelineEntityHref(row.traceID, row.pipelineID, row.pipelineClientID);
  if (!href) {
    return "None";
  }
  return linkedPrimaryCell(row.pipelineCommand || "Pipeline", "", href);
}

function workspaceOpPipelineSummary(row) {
  const href = pipelineEntityHref(row.traceID, row.pipelineID, row.pipelineClientID);
  if (!href) {
    return "None";
  }
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(row.pipelineCommand || "Pipeline")}</a>`;
}

function pipelineDurationLabel(row) {
  return durationLabel(row.startUnixNano, row.endUnixNano, row.status);
}

function pipelineTerminalCallID(row) {
  const text = String(row?.terminalCallID || "");
  if (!text) {
    return "";
  }
  const parts = text.split("/");
  return parts[parts.length - 1] || "";
}

function confidencePill(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized === "high") {
    return tonePill("good", value);
  }
  if (normalized === "medium") {
    return tonePill("neutral", value);
  }
  return tonePill("warn", value || "low");
}

function primaryCell(title, subtitle) {
  return `
    <div class="v3-cell-primary">
      <strong>${escapeHTML(title)}</strong>
      ${subtitle ? `<span>${escapeHTML(subtitle)}</span>` : ""}
    </div>
  `;
}

function linkedPrimaryCell(title, subtitle, href) {
  if (!href) {
    return primaryCell(title, subtitle);
  }
  return `
    <a class="v3-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">
      ${primaryCell(title, subtitle)}
    </a>
  `;
}

function callCell(title, subtitle) {
  return `
    <div class="v3-cell-primary v3-call-cell">
      <code class="v3-call-signature">${escapeHTML(title)}</code>
      ${subtitle ? `<span>${escapeHTML(subtitle)}</span>` : ""}
    </div>
  `;
}

function linkedCallCell(title, subtitle, href) {
  if (!href) {
    return callCell(title, subtitle);
  }
  return `
    <a class="v3-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">
      ${callCell(title, subtitle)}
    </a>
  `;
}

function statusPill(value) {
  const normalized = String(value || "").toLowerCase();
  const good = ["ready", "passing", "loaded", "protected", "warm", "completed"];
  const warn = ["degraded", "flaky", "drifted", "queued", "failed", "idle", "error"];
  const neutral = ["live", "warming", "ephemeral", "cooldown", "running", "attached", "light", "loading", "hybrid", "open", "ingesting"];
  const tone = good.includes(normalized) ? "good" : warn.includes(normalized) ? "warn" : neutral.includes(normalized) ? "neutral" : "neutral";
  return `<span class="v3-pill v3-pill-${tone}">${escapeHTML(value)}</span>`;
}

function tonePill(tone, label = tone) {
  return `<span class="v3-pill v3-pill-${escapeHTML(tone || "neutral")}">${escapeHTML(label || tone || "neutral")}</span>`;
}

function summarizeKinds(kinds) {
  if (!Array.isArray(kinds) || kinds.length === 0) {
    return "No attached follow-up";
  }
  return kinds.join(", ");
}

function formatDuration(nano) {
  const ns = Number(nano || 0);
  if (!Number.isFinite(ns) || ns <= 0) {
    return "0 ms";
  }
  const ms = ns / 1_000_000;
  if (ms < 1000) {
    return `${Math.round(ms)} ms`;
  }
  return `${(ms / 1000).toFixed(1)} s`;
}

function relativeTimeFromNow(unixNano) {
  const ts = Number(unixNano || 0);
  if (!Number.isFinite(ts) || ts <= 0) {
    return "Unknown";
  }
  const diffMs = Math.max(0, Date.now() - Math.floor(ts / 1_000_000));
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 10) {
    return "just now";
  }
  if (seconds < 60) {
    return `${seconds} seconds ago`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} hour${hours === 1 ? "" : "s"} ago`;
  }
  const days = Math.floor(hours / 24);
  return `${days} day${days === 1 ? "" : "s"} ago`;
}

function durationLabel(startUnixNano, endUnixNano, status) {
  const start = Number(startUnixNano || 0);
  const end = Number(endUnixNano || 0);
  if (!Number.isFinite(start) || start <= 0) {
    return status === "running" ? "Running" : "Unknown";
  }
  if (!Number.isFinite(end) || end <= start) {
    return status === "running" ? "Running" : "0 ms";
  }
  return formatDuration(end - start);
}

function median(values) {
  if (!values.length) {
    return 0;
  }
  const sorted = values.slice().sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) {
    return sorted[mid];
  }
  return (sorted[mid - 1] + sorted[mid]) / 2;
}

function shortID(raw, width = 12) {
  const text = String(raw || "");
  if (!text || text.length <= width) {
    return text;
  }
  return text.slice(0, width);
}

function looksLikeDigest(value) {
  const text = String(value || "");
  return (text.startsWith("xxh3:") || text.startsWith("sha256:")) && text.length > 12;
}

function truncateText(value, maxLen) {
  const text = String(value || "");
  if (!text || text.length <= maxLen) {
    return text;
  }
  if (maxLen <= 1) {
    return text.slice(0, maxLen);
  }
  return `${text.slice(0, maxLen - 1)}…`;
}

function shortRouteID(left, right) {
  return `${routeToken(left)}-${routeToken(right)}`;
}

function sessionRouteID(traceID, sessionID) {
  return shortRouteID(traceID, sessionID || traceID);
}

function routeToken(raw, width = 10) {
  const text = String(raw || "")
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, "");
  if (!text) {
    return "unknown";
  }
  return text.slice(0, width);
}

function sessionStatusLabel(row) {
  if (row.open) {
    return "open";
  }
  return row.status || "unknown";
}

function replSessionCell(row) {
  if (!row.sessionID) {
    return primaryCell("Trace-local", shortID(row.traceID));
  }
  return sessionCellByID(row?.sessionID, row?.traceID);
}

function replSessionSummary(row) {
  if (!row.sessionID) {
    return detailCode(shortID(row.traceID) || "Unknown");
  }
  return sessionInlineLinkByID(row?.sessionID, row?.traceID);
}

function replCommandHistoryCell(row) {
  const count = Number(row.commandCount || 0);
  return primaryCell(`${count} commands`, row.lastCommand || row.firstCommand || "No command history");
}

function replPipelineCell(row) {
  const href = pipelineEntityHref("", row?.pipelineID);
  if (!href) {
    return primaryCell(row?.command || row?.name || "Command", "");
  }
  return linkedPrimaryCell(row.command || row.name || "Command", row.pipelineCommand || "", href);
}

function workspaceCountsCell(row) {
  return primaryCell(String(row.opCount || 0), `${row.readCount || 0} reads · ${row.writeCount || 0} writes`);
}

function shellActivityCell(row) {
  const names = Array.isArray(row.activityNames) ? row.activityNames.filter(Boolean) : [];
  const title = `${row.callCount || 0} calls`;
  const subtitle = names.length > 0 ? names.slice(0, 2).join(", ") : "No DAGQL calls yet";
  return primaryCell(title, subtitle);
}

function pipelineEntityHref(traceID, pipelineID, fallbackKey) {
  const routeID = shortRouteID("", pipelineID || "") || shortRouteID(traceID, fallbackKey || "");
  if (!routeID) {
    return "";
  }
  return entityPath("pipelines", routeID);
}

function shellDescendantCell(row) {
  const childIDs = Array.isArray(row.childClientIDs) ? row.childClientIDs : [];
  const title = row.childClientCount > 0 ? `${row.childClientCount} child clients` : "Root only";
  const subtitle = childIDs.length > 0 ? childIDs.slice(0, 2).map((id) => shortID(id)).join(", ") : "No descendant clients";
  return primaryCell(title, subtitle);
}

function shellScopeCell(row) {
  const title = row.clientName || row.clientID || "unknown client";
  const session = sessionRowByID(row?.sessionID);
  const detail = session ? sessionDisplayName(session) : row.sessionID ? `session ${shortID(row.sessionID)}` : `trace ${shortID(row.traceID)}`;
  return primaryCell(title, detail);
}

function navIcon(entityID) {
  const icons = {
    terminals:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 3.5h11v9h-11z"></path><path d="M4.5 6 6.5 8 4.5 10"></path><path d="M8 10h2.5"></path></svg>',
    services:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M8 2.5 12.5 5v6L8 13.5 3.5 11V5z"></path><path d="M8 2.5V8m4.5-3L8 8 3.5 5"></path></svg>',
    calls:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M3 8h10"></path><path d="M9 4l4 4-4 4"></path><circle cx="4" cy="8" r="1.5"></circle></svg>',
    objects:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M8 2.5 12.5 5v6L8 13.5 3.5 11V5z"></path><path d="M8 2.5 3.5 5 8 7.5 12.5 5 8 2.5Z"></path></svg>',
    "object-types":
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M3.5 4.5h9"></path><path d="M8 4.5v7"></path><path d="M5 7.5h6"></path><path d="M4.5 11.5h7"></path></svg>',
    modules:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M3.5 4.5h5l2 2h2v5h-9z"></path><path d="M8.5 4.5v2h2"></path></svg>',
    repls:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 3.5h11v9h-11z"></path><path d="M4.5 6.5 6.5 8 4.5 9.5"></path><path d="M8 9.5h3"></path></svg>',
    checks:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M3.5 8.5 6.5 11 12.5 5"></path><path d="M8 14a6 6 0 1 0 0-12 6 6 0 0 0 0 12Z"></path></svg>',
    workspaces:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 5.5h4l1 1h6v5.5h-11z"></path><path d="M2.5 5.5V4.5h4l1 1"></path></svg>',
    devices:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><rect x="3" y="3.5" width="10" height="6.5" rx="1"></rect><path d="M5.5 12.5h5"></path><path d="M7 10v2.5M9 10v2.5"></path></svg>',
    clients:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><circle cx="8" cy="5.5" r="2.2"></circle><path d="M3.5 12.5c.8-2 2.5-3 4.5-3s3.7 1 4.5 3"></path></svg>',
    sessions:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><circle cx="5" cy="8" r="2.5"></circle><circle cx="11" cy="8" r="2.5"></circle><path d="M7.5 8h1"></path></svg>',
    pipelines:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><circle cx="3.5" cy="4" r="1.5"></circle><circle cx="8" cy="8" r="1.5"></circle><circle cx="12.5" cy="12" r="1.5"></circle><path d="M4.7 5.2 6.8 7.1M9.2 8.9l2.1 1.9"></path></svg>',
    "git-remotes":
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><circle cx="4" cy="4" r="1.5"></circle><circle cx="12" cy="4" r="1.5"></circle><circle cx="8" cy="12" r="1.5"></circle><path d="M5.5 4h5M8 5.5V10.5"></path></svg>',
    registries:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><ellipse cx="8" cy="4" rx="4.5" ry="1.8"></ellipse><path d="M3.5 4v6c0 1 2 1.8 4.5 1.8s4.5-.8 4.5-1.8V4"></path><path d="M3.5 7c0 1 2 1.8 4.5 1.8s4.5-.8 4.5-1.8"></path></svg>',
  };
  return icons[entityID] || '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><circle cx="8" cy="8" r="5"></circle></svg>';
}

function findEntity(entityID) {
  return entities.find((entity) => entity.id === entityID) || null;
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

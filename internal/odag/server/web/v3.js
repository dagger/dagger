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
    id: "cli-runs",
    label: "CLI Runs",
    code: "CL",
    category: "Execution-centric",
    eyebrow: "One-shot command sessions",
    blurb:
      "CLI run entities group telemetry around a concrete `dagger call` style invocation. They are execution-scoped first, with objects as outputs and evidence rather than the primary identity.",
    metrics: [
      { label: "Recent runs", value: "18", detail: "mostly one-shot module calls" },
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
      { label: "Remote coupling", value: "2 runs", tone: "warn", detail: "latest failures involved remote resources" },
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
  "cli-runs": {
    stateKey: "cliRuns",
    endpoint: "/api/v2/cli-runs?limit=100",
    label: "CLI Runs",
    singularLabel: "CLI Run",
  },
  shells: {
    stateKey: "shells",
    endpoint: "/api/v2/shells?limit=100",
    label: "Shells",
    singularLabel: "Shell",
  },
};

const state = {
  entityID: "terminals",
  detailID: "",
  live: {
    cliRuns: {
      status: "idle",
      items: [],
      error: "",
    },
    shells: {
      status: "idle",
      items: [],
      error: "",
    },
  },
};

const els = {
  pageLabel: document.getElementById("pageLabel"),
  pageTitle: document.getElementById("pageTitle"),
  entityNav: document.getElementById("entityNav"),
  shellMode: document.getElementById("shellMode"),
  shellSource: document.getElementById("shellSource"),
  tableTitle: document.getElementById("tableTitle"),
  tableMeta: document.getElementById("tableMeta"),
  tableShell: document.getElementById("tableShell"),
};

init();

function init() {
  readURLState();
  bindEvents();
  render();
  void ensureActiveEntityData();
}

function bindEvents() {
  window.addEventListener("popstate", () => {
    readURLState();
    render();
    void ensureActiveEntityData();
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
    .map((segment) => decodeURIComponent(segment).toLowerCase());
  const entityID = findEntity(segments[0] || "") ? segments[0] : legacyEntityID(search);
  const detailID = supportsDetailRoute(entityID) && segments[1] ? segments[1] : "";
  return {
    entityID: entityID || entities[0].id,
    detailID,
  };
}

function legacyEntityID(search) {
  const params = new URLSearchParams(search);
  const entityID = String(params.get("entity") || params.get("type") || "").toLowerCase();
  if (findEntity(entityID)) {
    return entityID;
  }
  return entities[0].id;
}

function navigateTo(nextPath, replace = false) {
  const url = new URL(nextPath, window.location.origin);
  const route = parseRoute(url.pathname, url.search);
  state.entityID = route.entityID;
  state.detailID = route.detailID;
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
  const config = liveDomainConfigs[state.entityID];
  if (!config) {
    return;
  }
  const entry = state.live[config.stateKey];
  if (entry.status === "loading" || entry.status === "loaded") {
    return;
  }
  entry.status = "loading";
  entry.error = "";
  render();
  try {
    const res = await fetch(config.endpoint);
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    const payload = await res.json();
    entry.items = Array.isArray(payload.items) ? payload.items : [];
    entry.status = "loaded";
    entry.error = "";
  } catch (err) {
    entry.status = "error";
    entry.error = err instanceof Error ? err.message : String(err || "unknown error");
  }
  render();
}

function render() {
  renderEntityNav();
  renderMain();
}

function renderEntityNav() {
  els.entityNav.innerHTML = entities
    .map((entity) => {
      const active = entity.id === state.entityID;
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
          <span class="v3-type-icon">${escapeHTML(entity.code)}</span>
          <span class="v3-type-copy">
            <span class="v3-type-line">
              <span class="v3-type-title">${escapeHTML(entity.label)}</span>
              ${mockBadge}
            </span>
          </span>
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
  const entity = currentEntity();
  const shellState = describeShellState(entity);

  els.shellMode.textContent = shellState.mode;
  els.shellSource.textContent = shellState.source;

  if (state.detailID && supportsDetailRoute(entity.id)) {
    renderDetail(entity);
    return;
  }

  const model = tableModel(entity, "inventory");
  document.title = `ODAG ${entity.label}`;
  els.pageLabel.textContent = "Domain";
  els.pageTitle.textContent = entity.label;
  els.tableTitle.textContent = entity.label;
  els.tableMeta.textContent = model.meta;
  renderTable(model);
}

function renderTable(model) {
  els.tableShell.innerHTML = renderTableHTML(model);
}

function renderTableHTML(model) {
  const head = model.columns.map((column) => `<th>${escapeHTML(column.label)}</th>`).join("");
  const body = model.rows.length
    ? model.rows
        .map((row) => {
          const cells = model.columns
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
    : `<tr><td colspan="${model.columns.length}">${escapeHTML(model.emptyMessage || "No rows yet.")}</td></tr>`;

  return `
    <table class="v3-table">
      <thead>
        <tr>${head}</tr>
      </thead>
      <tbody>${body}</tbody>
    </table>
  `;
}

function renderDetail(entity) {
  const config = liveDomainConfigs[entity.id];
  const live = config ? state.live[config.stateKey] : null;
  const detailLabel = config?.singularLabel || entity.label;
  const detailItem = currentDetailItem(entity);

  els.pageLabel.textContent = detailLabel;
  els.tableTitle.textContent = `${detailLabel} Details`;

  if (live && live.status !== "loaded") {
    document.title = `ODAG ${detailLabel}`;
    els.pageTitle.textContent = detailLabel;
    els.tableMeta.textContent = live.status === "error" ? "Unavailable" : "Loading";
    els.tableShell.innerHTML = renderDetailState(entity, detailLabel, live.status === "error" ? "unavailable" : "loading");
    return;
  }

  if (!detailItem) {
    document.title = `ODAG ${detailLabel}`;
    els.pageTitle.textContent = detailLabel;
    els.tableMeta.textContent = state.detailID;
    els.tableShell.innerHTML = renderDetailState(entity, detailLabel, "missing");
    return;
  }

  document.title = `ODAG ${detailLabel} ${detailItem.routeID}`;
  els.pageTitle.textContent = detailItem.name;
  els.tableMeta.textContent = detailItem.routeID;

  if (entity.dynamicKind === "cli-runs") {
    els.tableShell.innerHTML = renderCLIRunDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "shells") {
    els.tableShell.innerHTML = renderShellDetail(entity, detailItem);
    return;
  }

  els.tableShell.innerHTML = renderDetailState(entity, detailLabel, "missing");
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

function renderCLIRunDetail(entity, row) {
  const evidenceTable = renderTableHTML({
    columns: [
      { label: "Kind", key: "kind" },
      { label: "Confidence", render: (item) => confidencePill(item.confidence) },
      { label: "Source", key: "source" },
      { label: "Note", key: "note" },
    ],
    rows: row.evidence || [],
    emptyMessage: "No CLI run evidence recorded.",
  });
  const relationTable = renderTableHTML({
    columns: [
      { label: "Relation", render: (item) => tonePill("neutral", item.relation) },
      { label: "Target", render: (item) => primaryCell(item.target, item.targetKind) },
      { label: "Detail", key: "note" },
    ],
    rows: row.relations || [],
    emptyMessage: "No CLI run relations recorded.",
  });

  return `
    <div class="v3-detail-stack">
      ${backLink(entity)}
      <div class="v3-detail-grid">
        ${detailCard(
          "Summary",
          detailList([
            ["Status", statusPill(row.status)],
            ["Duration", escapeHTML(cliRunDurationLabel(row))],
            ["Command", detailCode(row.command || row.name)],
            ["Chain", detailCode(row.chainLabel || (row.chainTokens || []).join(" | ") || row.name)],
            ["Trace", detailCode(row.traceID)],
            ["Session", detailCode(row.sessionID || "Unknown")],
            ["Client", detailCode(row.clientID || "Unknown")],
          ]),
        )}
        ${detailCard(
          "Output",
          detailList([
            ["Terminal call", detailCode(row.terminalCallName || "Unknown")],
            ["Return type", detailCode(row.terminalReturnType || "Plain value")],
            ["Output object", detailCode(row.terminalOutputDagqlID || row.terminalObjectID || "None")],
            ["Post-process", detailInlineList(row.postProcessKinds, "None")],
            ["Follow-up spans", detailInlineList(row.followupSpanNames, "None")],
            ["Call chain", detailInlineList(row.chainCallIDs, "None")],
          ]),
        )}
      </div>
      ${detailSection("Evidence", evidenceTable)}
      ${detailSection("Relations", relationTable)}
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
            ["Session", detailCode(row.sessionID || "Unknown")],
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
  if (entity.dynamicKind === "cli-runs") {
    return cliRunsTableModel(entity, sectionID);
  }
  if (entity.dynamicKind === "shells") {
    return shellsTableModel(entity, sectionID);
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
          { label: "Status", render: (row) => statusPill(row.status) },
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
          { label: "Status", render: (row) => statusPill(row.status) },
          { label: "Scope", key: "scope" },
          { label: "Updated", key: "updated" },
        ],
        rows: entity.inventory.slice(0, 3),
      };
  }
}

function cliRunsTableModel(entity, sectionID) {
  switch (sectionID) {
    case "inventory":
      return {
        eyebrow: "Inventory",
        title: "CLI Run Inventory",
        meta: `${entity.liveItems.length} real runs`,
        emptyMessage: "No CLI runs detected yet.",
        columns: [
          { label: "Run", render: (row) => linkedPrimaryCell(row.command || row.name, "", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusPill(row.status) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(cliRunDurationLabel(row)) },
          { label: "Output Type", render: (row) => escapeHTML(cliRunOutputTypeLabel(row)) },
          { label: "Scope", render: (row) => cliRunScopeCell(row) },
        ],
        rows: entity.liveItems,
      };
    case "evidence":
      return {
        eyebrow: "Evidence",
        title: "CLI Run Discovery Evidence",
        meta: `${entity.evidence.length} real evidence rows`,
        emptyMessage: "No CLI run evidence rows yet.",
        columns: [
          { label: "Run", render: (row) => primaryCell(row.runName, row.source) },
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
        title: "CLI Run Relations",
        meta: `${entity.relations.length} derived relations`,
        emptyMessage: "No CLI run relations yet.",
        columns: [
          { label: "Run", key: "source" },
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
        title: "CLI Run Current Surface",
        meta: `${Math.min(3, entity.liveItems.length)} of ${entity.liveItems.length} real runs`,
        emptyMessage: "No CLI runs detected yet.",
        columns: [
          { label: "Run", render: (row) => primaryCell(row.name, row.command || row.chainLabel) },
          { label: "Status", render: (row) => statusPill(row.status) },
          { label: "Output", render: (row) => cliRunOutputCell(row) },
          { label: "Follow-up", render: (row) => cliRunFollowupCell(row) },
          { label: "Scope", render: (row) => cliRunScopeCell(row) },
        ],
        rows: entity.liveItems.slice(0, 3),
      };
  }
}

function shellsTableModel(entity, sectionID) {
  switch (sectionID) {
    case "inventory":
      return {
        eyebrow: "Inventory",
        title: "Shell Inventory",
        meta: `${entity.liveItems.length} real shells`,
        emptyMessage: "No shell sessions detected yet.",
        columns: [
          { label: "Shell", render: (row) => linkedPrimaryCell(row.name, row.command || row.entryLabel, entityPath(entity.id, row.routeID)) },
          { label: "Mode", render: (row) => tonePill("neutral", row.mode || "interactive") },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)) },
          { label: "Activity", render: (row) => shellActivityCell(row) },
          { label: "Descendants", render: (row) => shellDescendantCell(row) },
          { label: "Scope", render: (row) => shellScopeCell(row) },
        ],
        rows: entity.liveItems,
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
        meta: `${Math.min(3, entity.liveItems.length)} of ${entity.liveItems.length} real shells`,
        emptyMessage: "No shell sessions detected yet.",
        columns: [
          { label: "Shell", render: (row) => primaryCell(row.name, row.command || row.entryLabel) },
          { label: "Status", render: (row) => statusPill(row.status) },
          { label: "Activity", render: (row) => shellActivityCell(row) },
          { label: "Descendants", render: (row) => shellDescendantCell(row) },
          { label: "Scope", render: (row) => shellScopeCell(row) },
        ],
        rows: entity.liveItems.slice(0, 3),
      };
  }
}

function currentEntity() {
  return materializeEntity(findEntity(state.entityID) || entities[0]);
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
  if (!entity || (entity.id !== "cli-runs" && entity.id !== "shells")) {
    return entity;
  }
  const config = liveDomainConfigs[entity.id];
  const live = state.live[config.stateKey];
  if (live.status === "loaded") {
    if (entity.id === "cli-runs") {
      return buildLiveCLIRunsEntity(entity, live.items);
    }
    return buildLiveShellsEntity(entity, live.items);
  }
  if (live.status === "idle" || live.status === "loading") {
    const pendingLabel = live.status === "idle" ? "Pending" : "Loading";
    return {
      ...entity,
      dynamicKind: entity.id,
      liveItems: [],
      blurb: `Fetching live ${config.label.toLowerCase()} from ${config.endpoint}. Other domains remain mocked while the next entity slice settles.`,
      metrics: [
        { label: "Live fetch", value: pendingLabel, detail: `Requesting ${config.label} from the real API.` },
        { label: "Entity mode", value: "Hybrid", detail: `${config.label} are switching from mock data to live data.` },
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
        { label: "Fallback", value: "Mock shell", detail: `${config.label} stayed isolated; other domains were not affected.` },
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

function buildLiveCLIRunsEntity(base, items) {
  const liveItems = items
    .map((item) => ({
      ...item,
      routeID: shortRouteID(item.traceID, item.clientID || item.id),
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
    dynamicKind: "cli-runs",
    liveItems,
    blurb:
      "This domain is now live. Each row is one derived `dagger call` invocation, with the command chain, terminal output, and CLI-managed follow-up behavior kept together in one execution entity.",
    metrics: [
      {
        label: "Detected runs",
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
        detail: `${attachedCount} runs with attached CLI follow-up`,
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

function describeShellState(entity) {
  const currentConfig = liveDomainConfigs[entity.id];
  if (currentConfig) {
    const currentLive = state.live[currentConfig.stateKey];
    switch (currentLive.status) {
      case "loading":
        return {
          mode: "Activating",
          source: "API Loading",
          copy: `${currentConfig.label} is switching to live data. The shell is fetching ${currentConfig.endpoint} while the rest of the taxonomy stays mocked.`,
        };
      case "loaded":
        return {
          mode: "Live Domain",
          source: "Real API",
          copy: `${currentConfig.label} is live end-to-end. Other domains remain mocked until their own discovery rules and specialized views are ready.`,
        };
      case "error":
        return {
          mode: "Hybrid Degraded",
          source: "API Error",
          copy: `${currentConfig.label} is one of the live domains, but its API fetch failed. The shared shell stays up and the rest of the domains remain mocked.`,
        };
      default:
        return {
          mode: "Activating",
          source: "Pending API",
          copy: `${currentConfig.label} will switch from mocked shell state to real API data as soon as the initial fetch completes.`,
        };
    }
  }

  const loadedDomains = Object.entries(liveDomainConfigs)
    .filter(([, config]) => state.live[config.stateKey].status === "loaded")
    .map(([, config]) => config.label);
  const degradedDomains = Object.entries(liveDomainConfigs)
    .filter(([, config]) => state.live[config.stateKey].status === "error")
    .map(([, config]) => config.label);

  if (loadedDomains.length > 0) {
    return {
      mode: "Mock Domain",
      source: "Mock Data",
      copy: `This domain is still mocked. ${loadedDomains.join(" and ")} ${loadedDomains.length === 1 ? "is" : "are"} already live, which lets the shared shell evolve without forcing the rest of the taxonomy to harden too early.`,
    };
  }

  if (degradedDomains.length > 0) {
    return {
      mode: "Mock Domain",
      source: "Mock Data",
      copy: `The shell is still mostly mocked. ${degradedDomains.join(" and ")} ${degradedDomains.length === 1 ? "is" : "are"} currently degraded and not masking the rest of the taxonomy work.`,
    };
  }

  return {
    mode: "Mock Domain",
    source: "Mock Data",
    copy: "Discovery first, specialized views second. The shell starts mocked, then individual domains switch to live data as their heuristics settle.",
  };
}

function cliRunOutputCell(row) {
  const title = row.terminalReturnType || "Unknown";
  const subtitle = row.terminalOutputDagqlID || row.terminalObjectID || row.terminalCallName || "Plain value";
  return primaryCell(title, subtitle);
}

function cliRunOutputTypeLabel(row) {
  return row.terminalReturnType || "Plain value";
}

function cliRunFollowupCell(row) {
  const names = Array.isArray(row.followupSpanNames) ? row.followupSpanNames.filter(Boolean) : [];
  const title = row.followupSpanCount > 0 ? `${row.followupSpanCount} attached spans` : "No attached spans";
  const subtitle = names.length > 0 ? names.slice(0, 2).join(", ") : summarizeKinds(row.postProcessKinds);
  return primaryCell(title, subtitle);
}

function cliRunScopeCell(row) {
  const title = row.clientName || row.clientID || "unknown client";
  const detail = row.sessionID ? `session ${shortID(row.sessionID)}` : `trace ${shortID(row.traceID)}`;
  return primaryCell(title, detail);
}

function cliRunDurationLabel(row) {
  return durationLabel(row.startUnixNano, row.endUnixNano, row.status);
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

function statusPill(value) {
  const normalized = String(value || "").toLowerCase();
  const good = ["ready", "passing", "loaded", "protected", "warm", "completed"];
  const warn = ["degraded", "flaky", "drifted", "queued", "failed", "idle", "error"];
  const neutral = ["live", "warming", "ephemeral", "cooldown", "running", "attached", "light", "loading", "hybrid"];
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

function shortRouteID(left, right) {
  return `${routeToken(left)}-${routeToken(right)}`;
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

function shellActivityCell(row) {
  const names = Array.isArray(row.activityNames) ? row.activityNames.filter(Boolean) : [];
  const title = `${row.callCount || 0} calls`;
  const subtitle = names.length > 0 ? names.slice(0, 2).join(", ") : "No DAGQL calls yet";
  return primaryCell(title, subtitle);
}

function shellDescendantCell(row) {
  const childIDs = Array.isArray(row.childClientIDs) ? row.childClientIDs : [];
  const title = row.childClientCount > 0 ? `${row.childClientCount} child clients` : "Root only";
  const subtitle = childIDs.length > 0 ? childIDs.slice(0, 2).map((id) => shortID(id)).join(", ") : "No descendant clients";
  return primaryCell(title, subtitle);
}

function shellScopeCell(row) {
  const title = row.clientName || row.clientID || "unknown client";
  const detail = row.sessionID ? `session ${shortID(row.sessionID)}` : `trace ${shortID(row.traceID)}`;
  return primaryCell(title, detail);
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

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

const sessionHubEntityIDs = [
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

const state = {
  entityID: OVERVIEW_ROUTE_ID,
  detailID: "",
  workspaceFilterID: "",
  sessionFilterID: "",
  sessionFilterOpen: false,
  sessionFilterQuery: "",
  detailGraphs: {
    pipelines: {},
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
    sanitizeSessionFilterSelection();
    render();
  });

  window.addEventListener("popstate", () => {
    readURLState();
    syncWorkspaceFilterFromRoute();
    syncSessionFilterFromRoute();
    render();
    void ensureActiveEntityData();
  });

  document.addEventListener("pointerdown", (event) => {
    if (!state.sessionFilterOpen || !els.sessionFilterShell) {
      return;
    }
    if (els.sessionFilterShell.contains(event.target)) {
      return;
    }
    state.sessionFilterOpen = false;
    state.sessionFilterQuery = "";
    render();
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
  const entityID = resolveEntityID(segments[0] || "") || legacyID;
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
  await ensureLiveDomainData(liveDomainConfigs.sessions);
  await ensureLiveDomainData(liveDomainConfigs.workspaces);
  if (isOverviewRoute()) {
    await ensureOverviewData();
    return;
  }
  if (state.entityID === "sessions" && state.detailID) {
    await ensureSessionDetailData();
    return;
  }
  const config = liveDomainConfigs[state.entityID];
  if (!config) {
    return;
  }
  await ensureLiveDomainData(config);
}

async function ensureSessionDetailData() {
  const jobs = ["sessions", ...sessionHubEntityIDs]
    .map((entityID) => liveDomainConfigs[entityID])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config));
  await Promise.all(jobs);
}

async function ensureOverviewData() {
  const jobs = overviewEntities()
    .map((entity) => liveDomainConfigs[entity.id])
    .filter(Boolean)
    .map((config) => ensureLiveDomainData(config));
  await Promise.all(jobs);
}

async function ensureLiveDomainData(config) {
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
  const allLabel = status === "error" ? "Workspaces unavailable" : status === "loaded" ? "All Workspaces" : "Loading Workspaces...";
  options.push(`<option value="">${escapeHTML(allLabel)}</option>`);
  for (const row of rows) {
    options.push(`<option value="${escapeHTML(row.routeID)}">${escapeHTML(workspaceFilterOptionLabel(row))}</option>`);
  }
  els.workspaceFilter.innerHTML = options.join("");
  els.workspaceFilter.value = rows.some((row) => row.routeID === selected) ? selected : "";
  els.workspaceFilter.disabled = status !== "loaded" && rows.length === 0;
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
    });
  }

  if (state.sessionFilterOpen && input && document.activeElement !== input) {
    queueMicrotask(() => {
      input.focus();
      input.select();
    });
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

function render() {
  sanitizeSessionFilterSelection();
  renderWorkspaceFilter();
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
  els.tableTitle.textContent = entity.dynamicKind === "sessions" && detailItem ? detailItem.name : `${detailLabel} Details`;
  setPanelHeadHidden(true);

  if (live && live.status !== "loaded") {
    document.title = `ODAG ${detailLabel}`;
    els.tableMeta.textContent = live.status === "error" ? "Unavailable" : "Loading";
    els.tableShell.innerHTML = renderDetailState(entity, detailLabel, live.status === "error" ? "unavailable" : "loading");
    return;
  }

  if (!detailItem) {
    document.title = `ODAG ${detailLabel}`;
    els.tableMeta.textContent = state.detailID;
    els.tableShell.innerHTML = renderDetailState(entity, detailLabel, "missing");
    return;
  }

  document.title = `ODAG ${detailLabel} ${detailItem.routeID}`;
  els.tableMeta.textContent = detailItem.routeID;

  if (entity.dynamicKind === "pipelines") {
    const graph = ensurePipelineGraph(detailItem);
    els.tableShell.innerHTML = renderPipelineDetail(entity, detailItem, graph);
    return;
  }
  if (entity.dynamicKind === "repls") {
    els.tableShell.innerHTML = renderReplDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "checks") {
    els.tableShell.innerHTML = renderCheckDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "workspaces") {
    els.tableShell.innerHTML = renderWorkspaceDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "workspace-ops") {
    els.tableShell.innerHTML = renderWorkspaceOpDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "services") {
    els.tableShell.innerHTML = renderServiceDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "terminals") {
    els.tableShell.innerHTML = renderTerminalDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "git-remotes") {
    els.tableShell.innerHTML = renderGitRemoteDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "registries") {
    els.tableShell.innerHTML = renderRegistryDetail(entity, detailItem);
    return;
  }
  if (entity.dynamicKind === "sessions") {
    els.tableShell.innerHTML = renderSessionDetail(entity, detailItem);
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

function renderPipelineDetail(entity, row, graph) {
  const moduleItem = pipelineModuleRecapItem(graph?.data);
  return `
    <div class="v3-detail-stack">
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
          ${pipelineRecapItem("Session", pipelineSessionSummary(row))}
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
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
}

function pipelineModuleRecapItem(payload) {
  const moduleRef = payload?.module?.ref;
  if (!moduleRef) {
    return "";
  }
  return pipelineRecapItem("Module", detailCode(moduleRef));
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
          ${pipelineRecapItem("Path", row.path ? detailCode(row.path) : "Unknown")}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.startUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.startUnixNano, row.endUnixNano, row.status)))}
          ${pipelineRecapItem("Session", workspaceOpSessionSummary(row))}
          ${pipelineRecapItem("Pipeline", workspaceOpPipelineSummary(row))}
        </div>
      </section>
      <section class="v3-detail-card">
        <div class="v3-detail-list">
          ${workspaceOpDetailItem("Kind", escapeHTML(row.kind || "unknown"))}
          ${workspaceOpDetailItem("Target Type", escapeHTML(row.targetType || "Unknown"))}
          ${workspaceOpDetailItem("Receiver", row.receiverDagqlID ? detailCode(row.receiverDagqlID) : "None")}
          ${workspaceOpDetailItem("Output", row.outputDagqlID ? detailCode(row.outputDagqlID) : "None")}
          ${workspaceOpDetailItem("Client", row.clientID ? detailCode(row.clientID) : "Unknown")}
          ${workspaceOpDetailItem("Trace", row.traceID ? detailCode(row.traceID) : "Unknown")}
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
  const activityTable = renderTableHTML({
    columns: [
      { label: "Call", render: (item) => primaryCell(item.name, "") },
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
          ${pipelineRecapItem("Created By", detailCode(row.createdByCallName || "Unknown"))}
          ${pipelineRecapItem("Pipeline", servicePipelineSummary(row))}
          ${pipelineRecapItem("DAGQL ID", detailCode(row.dagqlID || "Unknown"))}
        </div>
      </section>
      <div class="v3-detail-grid">
        ${detailCard(
          "Definition",
          detailList([
            ["Image", row.imageRef ? detailCode(row.imageRef) : "Unknown"],
            ["Hostname", row.customHostname ? detailCode(row.customHostname) : "None"],
            ["Container", row.containerDagqlID ? detailCode(row.containerDagqlID) : "None"],
            ["Tunnel Upstream", row.tunnelUpstreamDagqlID ? detailCode(row.tunnelUpstreamDagqlID) : "None"],
          ]),
        )}
        ${detailCard(
          "Activity",
          detailList([
            ["Calls", escapeHTML(String((row.activity || []).length))],
            ["Started", escapeHTML(relativeTimeFromNow(row.startUnixNano))],
            ["Last activity", escapeHTML(relativeTimeFromNow(row.lastActivityUnixNano))],
            ["Client", row.clientID ? detailCode(row.clientID) : "Unknown"],
          ]),
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
  const nodeW = 246;
  const nodeH = 114;
  const colGap = objects.length <= 2 ? 88 : 60;
  const rowGap = 18;
  const padX = 24;
  const padY = 24;
  const totalColumns = layout.columns.length || 1;
  const maxRows = Math.max(1, ...layout.columns.map((column) => column.length));
  const width = padX * 2 + totalColumns * nodeW + Math.max(0, totalColumns - 1) * colGap;
  const height = padY * 2 + maxRows * nodeH + Math.max(0, maxRows - 1) * rowGap;
  const nodePositions = new Map();
  const nodeMarkup = layout.columns
    .map((column, colIndex) =>
      column
        .map((obj, rowIndex) => {
          const x = padX + colIndex * (nodeW + colGap);
          const y = padY + rowIndex * (nodeH + rowGap);
          nodePositions.set(obj.dagqlID, {
            x,
            y,
            width: nodeW,
            height: nodeH,
            centerX: x + nodeW / 2,
            centerY: y + nodeH / 2,
          });
          const title = pipelineNodeTitle(obj, aliases);
          const subtitle = pipelineNodeSubtitle(obj);
          const focusClass = obj.role === "output" || obj.dagqlID === focusObjectID ? " is-output" : "";
          const placeholderClass = obj.placeholder ? " is-placeholder" : "";
          const eyebrow = pipelineNodeEyebrow(obj, focusObjectID);
          return `
            <article class="v3-pipeline-node${focusClass}${placeholderClass}" style="left:${x}px; top:${y}px; width:${nodeW}px; height:${nodeH}px;">
              <span class="v3-pipeline-node-label">${escapeHTML(eyebrow)}</span>
              <strong>${escapeHTML(title)}</strong>
              <span>${escapeHTML(subtitle)}</span>
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
  const meta = [
    `${objects.length} object${objects.length === 1 ? "" : "s"}`,
    chainCount ? `${chainCount} chain step${chainCount === 1 ? "" : "s"}` : "",
    refCount ? `${refCount} ref${refCount === 1 ? "" : "s"}` : "",
    focusObjectID ? "output node highlighted" : `${pipelineOutputTypeLabel(row)} output`,
  ]
    .filter(Boolean)
    .join(" · ");

  return {
    nodes: objects,
    edges,
    width,
    height,
    meta,
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
  if (obj.role === "chain") {
    return "Pipeline";
  }
  return "Object";
}

function renderSessionDetail(entity, row) {
  const cards = renderSessionDomainCards(row);
  return `
    <div class="v3-detail-stack">
      <section class="v3-detail-card v3-session-recap">
        <div class="v3-pipeline-recap-grid">
          ${pipelineRecapItem("Status", statusPill(sessionStatusLabel(row)))}
          ${pipelineRecapItem("Started", escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)))}
          ${pipelineRecapItem("Duration", escapeHTML(durationLabel(row.firstSeenUnixNano, row.lastSeenUnixNano, row.open ? "running" : row.status)))}
          ${pipelineRecapItem("Last Seen", escapeHTML(relativeTimeFromNow(row.lastSeenUnixNano)))}
          ${pipelineRecapItem("Root Client", row.rootClientID ? detailCode(shortID(row.rootClientID)) : "Unknown")}
          ${pipelineRecapItem("Trace", row.traceID ? detailCode(shortID(row.traceID)) : "Unknown")}
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
    case "sessions":
      return workspaceScopeData(workspaceRow).sessionIDs.has(String(row.id || ""));
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
  };
  for (const op of Array.isArray(workspaceRow?.ops) ? workspaceRow.ops : []) {
    const sessionID = String(op?.sessionID || "");
    const clientID = String(op?.clientID || "");
    const pipelineClientID = String(op?.pipelineClientID || "");
    const pipelineID = String(op?.pipelineID || "");
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
  }
  workspaceScopeCache.set(workspaceRow, data);
  return data;
}

function sessionOwnsEntity(sessionRow, entityID, row) {
  switch (entityID) {
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

function sessionDomainItemLabel(entity, row) {
  switch (entity.id) {
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
          ${pipelineRecapItem("Input Container", row.receiverDagqlID ? detailCode(row.receiverDagqlID) : "Unknown")}
          ${pipelineRecapItem("Output Container", row.outputDagqlID ? detailCode(row.outputDagqlID) : "Unknown")}
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
          ${pipelineRecapItem("Span", row.spanName ? detailCode(row.spanName) : "Unknown")}
          ${pipelineRecapItem("Trace", row.traceID ? detailCode(shortID(row.traceID)) : "Unknown")}
        </div>
      </section>
      <section class="v3-detail-card">
        ${detailList([
          ["Client", row.clientID ? detailCode(row.clientID) : "Unknown"],
          ["Span ID", row.spanID ? detailCode(row.spanID) : "Unknown"],
        ])}
      </section>
    </div>
  `;
}

function renderWorkspaceDetail(entity, row) {
  const opsTable = renderTableHTML({
    columns: [
      { label: "Op", render: (item) => primaryCell(item.callName || item.name, item.path || item.kind || "") },
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
          { label: "Session", render: (row) => linkedPrimaryCell(shortID(row.id), "", entityPath(entity.id, row.routeID)) },
          { label: "Status", render: (row) => statusOrbCell(sessionStatusLabel(row)) },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.firstSeenUnixNano)) },
          { label: "Duration", render: (row) => escapeHTML(durationLabel(row.firstSeenUnixNano, row.lastSeenUnixNano, row.open ? "running" : row.status)) },
          { label: "Root Client", render: (row) => detailCode(row.rootClientID || "Unknown") },
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
          { label: "Service", render: (row) => linkedPrimaryCell(row.name || "Service", row.imageRef || "", entityPath(entity.id, row.routeID)) },
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
          { label: "Path", render: (row) => row.path ? detailCode(row.path) : "Unknown" },
          { label: "Started", render: (row) => escapeHTML(relativeTimeFromNow(row.startUnixNano)) },
          { label: "Pipeline", render: (row) => workspaceOpPipelineCell(row) },
          { label: "Session", render: (row) => workspaceOpSessionCell(row) },
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
  return String(row?.root || row?.name || "Workspace").trim();
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
    case "sessions":
      return shortID(row.id);
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

function overviewItemStatus(entity, row) {
  if (entity.id === "sessions") {
    return sessionStatusLabel(row);
  }
  return row.status || "";
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
  if (!entity || (entity.id !== "terminals" && entity.id !== "repls" && entity.id !== "checks" && entity.id !== "workspaces" && entity.id !== "services" && entity.id !== "sessions" && entity.id !== "pipelines" && entity.id !== "shells" && entity.id !== "workspace-ops" && entity.id !== "git-remotes" && entity.id !== "registries")) {
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
      "This domain is now live as observed workspace roots. Each row is one absolute host root repeatedly touched by authoritative workspace-op calls, with relative exports attached only when one root is unambiguous.",
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
      name: shortID(item.id),
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
      title: shortID(item.id),
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
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
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
  if (!row?.sessionID || !row?.traceID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
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
  if (!row?.sessionID) {
    return "Unknown";
  }
  const href = entityPath("sessions", shortRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
}

function terminalSessionSummary(row) {
  if (!row?.sessionID) {
    return "Unknown";
  }
  const href = entityPath("sessions", shortRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
}

function terminalActivityCell(row) {
  const execCount = Number(row.execCount || 0);
  const count = Number(row.activityCount || 0);
  const title = execCount === 1 ? "1 exec" : `${execCount} execs`;
  const subtitle = count === execCount ? `${count} activity spans` : `${count} spans total`;
  return primaryCell(title, subtitle);
}

function serviceCreatedByCell(row) {
  return primaryCell(row.createdByCallName || "Unknown", row.pipelineCommand || "");
}

function serviceSessionCell(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
}

function serviceSessionSummary(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
}

function checkSessionCell(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
}

function checkSessionSummary(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
}

function servicePipelineSummary(row) {
  const href = pipelineEntityHref(row.traceID, row.pipelineID, row.pipelineClientID);
  if (!href) {
    return "None";
  }
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(row.pipelineCommand || "Pipeline")}</a>`;
}

function workspaceOpSessionCell(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), "", href);
}

function workspaceOpSessionSummary(row) {
  if (!row.sessionID) {
    return "None";
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
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
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return linkedPrimaryCell(shortID(row.sessionID), row.rootClientID ? shortID(row.rootClientID) : "", href);
}

function replSessionSummary(row) {
  if (!row.sessionID) {
    return detailCode(shortID(row.traceID) || "Unknown");
  }
  const href = entityPath("sessions", sessionRouteID(row.traceID, row.sessionID));
  return `<a class="v3-inline-link" href="${escapeHTML(href)}" data-route-path="${escapeHTML(href)}">${escapeHTML(shortID(row.sessionID))}</a>`;
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
  const detail = row.sessionID ? `session ${shortID(row.sessionID)}` : `trace ${shortID(row.traceID)}`;
  return primaryCell(title, detail);
}

function navIcon(entityID) {
  const icons = {
    terminals:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 3.5h11v9h-11z"></path><path d="M4.5 6 6.5 8 4.5 10"></path><path d="M8 10h2.5"></path></svg>',
    services:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M8 2.5 12.5 5v6L8 13.5 3.5 11V5z"></path><path d="M8 2.5V8m4.5-3L8 8 3.5 5"></path></svg>',
    repls:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 3.5h11v9h-11z"></path><path d="M4.5 6.5 6.5 8 4.5 9.5"></path><path d="M8 9.5h3"></path></svg>',
    checks:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M3.5 8.5 6.5 11 12.5 5"></path><path d="M8 14a6 6 0 1 0 0-12 6 6 0 0 0 0 12Z"></path></svg>',
    workspaces:
      '<svg viewBox="0 0 16 16" role="presentation" focusable="false"><path d="M2.5 5.5h4l1 1h6v5.5h-11z"></path><path d="M2.5 5.5V4.5h4l1 1"></path></svg>',
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

# Composable Command TUI

## Status

In progress. The command-screen host, reusable span lists, and `dagger setup`
screen are implemented; broader frontend extraction remains follow-up work.

## Summary

Allow CLI commands to provide the outer structure of the pretty TUI while
embedding reusable, interactive views of the spans they produce.

The pretty frontend becomes a host for command screens. It continues to own the
terminal, Tuist event loop, telemetry database, forms, and lifecycle. A command
screen owns the semantic presentation of its workflow and composes trace-backed
components supplied by the host.

Commands without a custom screen continue to use the existing generic trace
frontend.

## Problem

The pretty frontend currently assumes that a trace tree is the entire screen.
This works for commands whose output is naturally a trace, but poorly for
guided workflows such as `dagger setup`:

1. **Execution structure becomes presentation structure** — commands must use
   span names and attributes to approximate headings, instructions, checklists,
   and summaries.
2. **Human messages look like operations** — representing prose as spans adds
   status icons, durations, and tree borders that have no semantic meaning.
3. **Plain output conflicts with the TUI** — writing directly to the terminal
   can race with live rendering and leave misplaced or disappearing text.
4. **Commands cannot compose trace UI** — the frontend already has interactive
   span trees, but only the monolithic frontend and test views can arrange them.
5. **Live and final output diverge accidentally** — once execution ends, the
   generic trace report can replace a command-specific workflow without a
   deliberate final presentation.

`dagger setup` should be able to render an ordinary workflow around real spans:

```text
Setting up this workspace

✓ Cloud account                          Already logged in
✓ Workspace                              Nothing to migrate

Recommended modules
  ✓ dagger install github.com/dagger/eslint       1.2s
  ⠋ dagger install github.com/dagger/go
  ○ dagger install github.com/dagger/prettier
  ○ dagger install github.com/dagger/vitest

  Enter: inspect details    Space: expand
```

The install rows should remain selectable and expandable. Their children can
show uploads, downloads, logs, and engine operations using the normal trace
presentation.

## Goals

- Let a command own the outer layout of the pretty frontend.
- Reuse the frontend's existing `dagui.DB` and live telemetry updates.
- Provide reusable span components with the current spinner, status, duration,
  navigation, expansion, logs, and child-span behavior.
- Keep terminal and focus lifecycle under frontend control.
- Make the final durable rendering an explicit mode of the command screen.
- Preserve the current generic trace experience as the default.
- Provide coherent output for interactive, report, and plain frontends.

## Non-goals

- Defining local UI layout through telemetry attributes.
- Letting commands manage the Tuist event loop or terminal directly.
- Changing the telemetry sent to Dagger Cloud.
- Making this a public GraphQL or module API.
- Replacing the generic trace frontend for commands that do not need a custom
  workflow.

## Architecture

The current `frontendPretty` responsibilities are split into a host, a default
screen, and reusable trace components:

```text
Pretty frontend host
├── screen slot
│   ├── generic trace screen (default)
│   └── command screen
│       ├── command-owned content
│       └── reusable span views
├── form and modal overlays
└── shared key bindings and frontend chrome
```

### Frontend Host

The host owns:

- the Tuist instance and root component
- the shared `dagui.DB`
- span and log exporters
- terminal sizing and shutdown
- forms, overlays, and focus restoration
- switching from live to final rendering
- the default generic trace screen

Commands do not receive the Tuist root and cannot add arbitrary siblings to
it. This keeps forms, focus, cleanup, and final rendering consistent.

### Command Screen

A command may install a screen before starting its traced work. The screen is a
normal Tuist component created with a frontend-owned view context:

```go
type ViewFactory func(ViewContext) CommandView

type CommandView interface {
 tuist.Component

 // SetFinal switches from transient progress to durable terminal output.
 SetFinal(bool)
}

type Frontend interface {
 // Existing methods omitted.
 SetView(ViewFactory) ViewHandle
}
```

If `SetView` is not called, the host installs the existing generic trace screen.

The exact API may evolve during extraction, but the ownership boundary is
fixed: the frontend owns the runtime; the command owns the screen body.

### View Context

The view context exposes frontend services without exposing mutable frontend
internals:

```go
type ViewContext interface {
 Trace() TraceStore
 Span(id dagui.SpanID, opts SpanViewOpts) tuist.Component
 SpanList(ids func() []dagui.SpanID, opts SpanListOpts) tuist.Component
}
```

`TraceStore` is a read-only view backed by the host's existing `dagui.DB`.
Access occurs on the Tuist event loop. Commands do not mutate the database or
poll it from execution goroutines.

`Span` and `SpanList` create components configured by stable span IDs. A command
chooses where the spans appear; the trace database remains the source of truth
for their names, status, timing, logs, and descendants.

### Span Components

The existing `SpanTreeView` becomes a reusable component independent of
`frontendPretty`. The existing `TestSpanChildrenView` is the closest current
precedent: it already builds a selectable container of `SpanTreeView` instances
against the shared database.

The generalized component supports:

- a supplied span ID or ordered list of IDs
- running, succeeded, failed, and pending presentation
- selection and keyboard navigation
- expansion and collapse
- child spans and inline logs
- optional depth and visibility policies
- live and final rendering

For example:

```go
type SpanListOpts struct {
 ShowChildren   bool
 CollapseOnDone bool
 MaxDepth       int
}
```

Span attributes such as reveal, passthrough, and UI message remain meaningful
to the generic trace screen. A custom command screen does not need to use them
to define its outer layout.

## State and Updates

Command screens combine two distinct sources of state.

### Trace State

Exporters continue to ingest spans and logs into `dagui.DB` on the Tuist event
loop. Mounted trace components register with the host and are invalidated when
relevant trace records change.

This replaces hard-coded update paths for individual component types with a
general mounted trace-view registry:

1. A trace component registers when mounted.
2. It unregisters when dismounted.
3. Exporters update `dagui.DB`.
4. The host invalidates affected mounted components.
5. Tuist propagates dirtiness through their parents and redraws the screen.

### Semantic State

Some workflow state is not telemetry. For example, "no workspace was found"
and "these modules were recommended" should not be encoded as fake spans.

The command maintains a small semantic model and updates it through a handle
that dispatches onto the Tuist event loop:

```go
type SetupModel struct {
 Migration MigrationState
 Installs  []dagui.SpanID
 Result    SetupResult
}

view.Update(func(model *SetupModel) {
 model.Migration = MigrationNotNeeded
})
```

The concrete handle can use a typed wrapper owned by the command. The required
property is that command goroutines do not race with component rendering.

## Focus and Input

The host retains global input ownership. Tuist focus may move into any
descendant of the active command screen, including an embedded span row.

The command screen handles navigation between its semantic sections and
delegates span navigation and expansion to the reusable span components. Forms
and modal overlays temporarily take focus through the host. Closing an overlay
restores the previously focused command component.

Commands cannot install terminal readers or bypass Tuist dispatch.

## Live and Final Rendering

A custom screen must deliberately support both phases:

- **Live mode** may contain spinners, pending rows, key hints, and expanded
  interactive details.
- **Final mode** emits concise durable output with no pending animation or
  interaction-only chrome.

At command completion, the host calls `SetFinal(true)`, performs a final render,
and then shuts down the live terminal session. It does not replace the command
screen with the generic trace report.

The same final component should be renderable without a terminal, using an
unbounded or headless Tuist context where practical. This keeps redirected and
report output aligned with the interactive result. The plain frontend may use
the same semantic model with non-interactive trace summaries when Tuist is not
available.

## `dagger setup`

The TUI host starts before Cloud login, but telemetry does not. Login is an
untraced semantic phase rendered by `SetupView`; once it succeeds or is
skipped, setup initializes telemetry and begins the traced workspace phase.
Credentials established by login therefore apply to the entire trace without
forcing login outside the composed screen.

After login, `dagger setup` installs a `SetupView` and starts its primary setup
span. The view owns:

- workspace migration status and guidance
- the recommended-module section
- the ordered list of module installation span IDs
- the final outcome and suggested next command

Each installation remains a real span named after the equivalent command:

```text
dagger install github.com/dagger/eslint
```

The screen embeds those spans as its checklist. Selecting or expanding an
installation reveals its normal trace children. Workspace guidance is ordinary
screen content, so it does not acquire a success icon, duration, or trace-tree
border.

On completion the view renders a durable summary such as:

```text
Workspace ready

Installed eslint, go, prettier, and vitest.

Try:
  dagger check
```

The trace continues to contain the setup span and all operations, and Cloud
continues to receive the full trace. Only local presentation changes.

## Failure Behavior

Failed action spans remain expandable and show their normal logs and children.
The command screen adds workflow context around the failure without copying
engine error text into a second state model.

If a custom screen cannot be constructed before execution starts, the frontend
falls back to the generic trace screen. A rendering failure after startup is a
frontend error; it must not silently hide the command result.

## Testing

Component tests use an in-memory `dagui.DB` and a headless Tuist renderer to
cover:

- pending, running, succeeded, and failed span rows
- live database updates invalidating mounted components
- selection, expansion, and focus restoration
- semantic model updates dispatched from command execution
- transition from live to final mode
- final rendering with and without a terminal
- fallback to the generic trace screen

`dagger setup` receives golden or snapshot coverage for its empty-workspace,
migration, successful-install, partial-failure, and final-summary states. A TUI
console or terminal recording test verifies the complete live transition and
guards against disappearing or interleaved output.

## Implementation Checklist

- [ ] Split `frontendPretty` into a frontend host and the default generic trace
      screen without changing existing command output.
- [x] Add a frontend-owned screen slot with the generic trace screen as its
      default component.
- [x] Define the internal `ViewFactory`, `CommandView`, `ViewContext`, and
      `ViewHandle` contracts.
- [ ] Extract `SpanTreeView` dependencies from `frontendPretty` into a reusable
      trace-view context.
- [x] Generalize the `TestSpanChildrenView` pattern into a reusable `SpanList`
      component.
- [x] Add mount and dismount registration for trace-backed components.
- [x] Replace hard-coded trace component updates with database-driven
      invalidation of mounted views.
- [x] Route semantic model mutations through the Tuist event loop.
- [x] Preserve descendant focus across renders and restore focus after forms
      and overlays close.
- [x] Add an explicit live-to-final transition for custom command screens.
- [x] Decouple the setup TUI lifetime from its traced execution lifetime so
      Cloud login can render inside the screen before telemetry starts.
- [x] Support deterministic final rendering for non-interactive output.
- [x] Implement `SetupView` with semantic workspace guidance and an embedded
      installation span checklist.
- [x] Remove setup-specific UI-message and span-surfacing workarounds that are
      no longer needed for local presentation.
- [ ] Add component, focus, exporter-update, final-render, and fallback tests.
- [ ] Add end-to-end TUI coverage for `dagger setup` success and failure flows.

# Idiomatic tuist migration for `frontend_pretty.go`

## Context

`dagql/idtui/frontend_pretty.go` was converted from Bubbletea (`tea.Program`/`tea.Model`) to tuist but the conversion was shallow — it's still one monolithic component that builds the entire UI into a `strings.Builder`, splits into lines, and returns them. A `SpanRowView` component was added for per-span render caching, but it uses a manual `spanRowKey` struct for dirty tracking, which reimplements what the component tree should provide automatically.

The goal is to restructure into idiomatic tuist components where dirty propagation flows naturally through the tree, eliminating manual tracking.

## Key design decision: no virtual scrolling

Render ALL rows — no viewport windowing. Let tuist's diff renderer handle offscreen content (unchanged lines are never written to the terminal). Users scroll their native terminal scrollback to see content above the viewport.

This eliminates the 100-line `renderLines()` viewport algorithm entirely.

## Current architecture (what we have)

```
TUI
└── frontendPretty (one Component, implements Interactive + Mounter)
    ├── fe.mu protects ALL state (DB, rows, focus, editline, etc.)
    ├── fe.Render() builds entire UI into strings.Builder
    │   ├── renderContent() → renderProgress() → renderLines() (viewport windowing)
    │   │   └── renderedRowLines() → RenderChild(SpanRowView) (per-span cache)
    │   ├── editlineView() (string)
    │   ├── formView() (string)
    │   ├── keymapView() (string)
    │   └── renderWithSidebar() (string composition)
    ├── HandleKeyPress() dispatches to form/editline/nav handlers
    ├── frameLoop() goroutine ticks at 30fps, updates fe.now
    └── exporters (separate goroutines) write to DB under fe.mu
```

Problems:
1. `fe.mu` — tuist's model is single UI goroutine, no locks needed. The mutex exists because exporters mutate DB directly.
2. `spanRowKey` — manual dirty tracking that reimplements what `Compo.Update()` propagation does automatically.
3. Monolithic `Render()` — builds everything as strings, bypassing the component tree. Changes to one region (e.g. keymap) re-render everything.
4. Global `frameLoop` — ticks all 30fps to drive spinner animation. Should be per-spinner.
5. Embedded bubbletea components (editline, huh.Form) are forwarded key events via `uvKeyToTeaKeyMsg` translation — works but is manual plumbing.
6. `renderLines()` viewport windowing — 100 lines of complex scrolling logic that can be eliminated.

## Target architecture

```
TUI
└── frontendPretty (Container-like, implements Interactive)
    ├── ZoomHeader (Component, optional — shown when zoomed into a non-primary span)
    ├── SpanRowView (Component per span, ordered — ALL rows rendered, no viewport)
    │   ├── Spinner (Component, self-ticking — only mounted when span is running)
    │   └── inline: title, logs, errors, debug (rendered by SpanRowView.Render)
    ├── ZoomedLogs (Component, optional — log tail for zoomed span)
    ├── Editline (Component, wraps bubbline — only in shell mode)
    ├── FormSlot (Slot — holds huh.Form when a prompt is active)
    └── Keymap (Component — bottom bar)
```

### Key principles

1. **No `fe.mu`** — exporters use `Dispatch()` to update state on the UI goroutine. All component state is UI-goroutine-only.
2. **No `spanRowKey`** — dirty propagation flows through the tree. Spinner.Update() → SpanRowView.Update() → frontendPretty.Update() → render.
3. **No viewport windowing** — render all rows. Tuist's diff renderer skips unchanged lines. Terminal scrollback handles what's offscreen.
4. **Spinner is a self-updating component** — has its own tick goroutine (started in OnMount, stopped on dismount). Only running spans have a Spinner child, so completed spans have zero per-frame cost.
5. **Focus uses `Focusable`** — `SpanRowView` implements `Focusable`. Navigation calls `ctx.SetFocus(targetSpanRow)`. Tuist delivers `SetFocused(true/false)` to old and new. Only two rows re-render.

## Detailed component design

### `Spinner`

```go
type Spinner struct {
    tuist.Compo
    rave *Rave
    now  time.Time
}

// OnMount starts a tick goroutine. ctx.Done() fires on dismount.
func (s *Spinner) OnMount(ctx tuist.EventContext) {
    go func() {
        ticker := time.NewTicker(33 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case t := <-ticker.C:
                ctx.Dispatch(func() {
                    s.now = t
                    s.Update() // propagates up: SpanRowView → frontendPretty → render
                })
            }
        }
    }()
}

func (s *Spinner) Render(ctx tuist.RenderContext) tuist.RenderResult {
    return tuist.RenderResult{Lines: []string{s.rave.ViewFancy(s.now)}}
}
```

When a span finishes, the SpanRowView dismounts its Spinner child → the tick goroutine stops → zero cost. No global frame ticker needed for spinner animation.

### `SpanRowView`

```go
type SpanRowView struct {
    tuist.Compo
    fe      *frontendPretty
    spanID  dagui.SpanID
    focused bool
    spinner *Spinner // nil when not running; mounted/dismounted as span state changes
}

func (s *SpanRowView) SetFocused(ctx tuist.EventContext, focused bool) {
    s.focused = focused
    s.Update()
}

func (s *SpanRowView) Render(ctx tuist.RenderContext) tuist.RenderResult {
    row := s.fe.rows.BySpan[s.spanID]
    if row == nil {
        return tuist.RenderResult{}
    }

    // ... render step title, status icon, duration, logs, errors, debug ...
    // For status icon: if spinner != nil, use RenderChild(s.spinner, ctx)
    //                  else render static icon (✔, ✘, ●, etc.)
    // For focus: use s.focused (set by SetFocused callback)

    return tuist.RenderResult{Lines: lines}
}
```

The spinner mounting/dismounting is managed by the SpanRowView when its span data changes (running → completed).

### `frontendPretty` as container

Instead of embedding `tuist.Compo` and building the entire view in one `Render()`, `frontendPretty` becomes a container-like component that manages child components:

```go
type frontendPretty struct {
    tuist.Compo

    // Component children (managed manually, rendered via RenderChild)
    zoomHeader *ZoomHeader
    spanRows   []*SpanRowView          // ordered list, matches rows.Order
    spanRowMap map[dagui.SpanID]*SpanRowView
    zoomedLogs *ZoomedLogs
    editline   *EditlineWrap           // nil when not in shell mode
    formSlot   *tuist.Slot
    keymap     *Keymap

    // Shared state (UI-goroutine-only, no mutex)
    db       *dagui.DB
    logs     *prettyLogs
    rows     *dagui.Rows
    rowsView *dagui.RowsView
    // ... other fields, but NO fe.mu ...
}
```

The `Render()` method composes children:

```go
func (fe *frontendPretty) Render(ctx tuist.RenderContext) tuist.RenderResult {
    var lines []string

    // Zoom header
    if fe.zoomHeader != nil {
        r := fe.RenderChild(fe.zoomHeader, ctx)
        lines = append(lines, r.Lines...)
    }

    // All span rows (no viewport windowing — render everything)
    for _, sr := range fe.spanRows {
        // Gap lines between rows (cheap, not cached)
        lines = append(lines, fe.gapLines(sr)...)
        r := fe.RenderChild(sr, ctx)
        lines = append(lines, r.Lines...)
    }

    // Zoomed logs
    if fe.zoomedLogs != nil {
        r := fe.RenderChild(fe.zoomedLogs, ctx)
        lines = append(lines, r.Lines...)
    }

    // Editline, form, keymap...
    // ...

    return tuist.RenderResult{Lines: lines}
}
```

### Eliminating `fe.mu`

The `dagui.DB` and `prettyLogs` are currently written by exporter goroutines under `fe.mu`. The migration:

**Before (current):**
```go
func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
    fe.mu.Lock()
    defer fe.mu.Unlock()
    fe.db.ExportSpans(ctx, spans)
    fe.recalculateViewLocked()
    fe.flush() // dispatches fe.Compo.Update()
}
```

**After:**
```go
func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
    // No lock. Just dispatch everything to the UI goroutine.
    fe.tui.Dispatch(func() {
        fe.db.ExportSpans(ctx, spans)
        fe.recalculateView()       // rebuilds rows, syncs SpanRowView children
        // Compo.Update() called automatically by child changes
    })
    return nil
}
```

All DB reads and writes happen on the UI goroutine. No mutex.

Note: `ExportSpans`/`Export` are called from otel SDK goroutines. The `Dispatch` call is safe from any goroutine. The otel SDK expects `ExportSpans` to be synchronous, but we can make the Dispatch synchronous if needed (wait on a done channel). However, the otel SDK already batches and the export contract allows async processing, so fire-and-forget Dispatch should work. If synchronous behavior is needed:

```go
func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
    done := make(chan error, 1)
    fe.tui.Dispatch(func() {
        done <- fe.db.ExportSpans(ctx, spans)
        fe.recalculateView()
    })
    return <-done
}
```

### `recalculateView()` — syncing the SpanRowView list

When the row set changes (spans arrive, zoom changes, verbosity changes), `recalculateView()` must sync the `fe.spanRows` list with `fe.rows.Order`:

```go
func (fe *frontendPretty) recalculateView() {
    fe.rowsView = fe.db.RowsView(fe.FrontendOpts)
    fe.rows = fe.rowsView.Rows(fe.FrontendOpts)

    // Build new ordered SpanRowView list, reusing existing components
    newList := make([]*SpanRowView, 0, len(fe.rows.Order))
    seen := make(map[dagui.SpanID]bool)
    for _, row := range fe.rows.Order {
        sr, exists := fe.spanRowMap[row.Span.ID]
        if !exists {
            sr = newSpanRowView(fe, row)
            fe.spanRowMap[row.Span.ID] = sr
        }
        sr.syncFromRow(row) // updates depth, expanded, chained, etc. — calls Update() if changed
        sr.syncSpinner(row) // mount/dismount spinner based on running state
        newList = append(newList, sr)
        seen[row.Span.ID] = true
    }

    // Dismount SpanRowViews that are no longer visible
    for id, sr := range fe.spanRowMap {
        if !seen[id] {
            // detach or just leave in map — it won't be rendered
        }
    }

    fe.spanRows = newList

    // Update focus
    if fe.autoFocus && len(newList) > 0 {
        last := newList[len(newList)-1]
        fe.setFocusTo(last)
    }
}
```

### Focus and navigation

Navigation (`goUp`/`goDown`/`goStart`/`goEnd`) changes which SpanRowView has focus:

```go
func (fe *frontendPretty) goDown(ctx tuist.EventContext) {
    fe.autoFocus = false
    newIdx := fe.focusedIdx + 1
    if newIdx >= len(fe.spanRows) {
        return
    }
    fe.focusedIdx = newIdx
    ctx.SetFocus(fe.spanRows[newIdx])
    // SetFocus calls SetFocused(false) on old, SetFocused(true) on new
    // Each calls Update() — only those two rows re-render
}
```

The `HandleKeyPress` on `frontendPretty` receives keys (since it's the parent in the bubble chain) and dispatches to nav/shell/form handlers. The focused SpanRowView doesn't need to handle keys — the parent does.

### Keypress highlight fading

The keymap shows pressed key highlights that fade after 500ms. This currently requires the frame ticker. Options:
- A small self-canceling timer goroutine started on each keypress (dispatch Update after 500ms)
- Or keep a minimal "animation ticker" that only runs when an animation is active, at a lower rate (e.g. 2fps just for the fade)

The first option is cleaner:

```go
func (fe *frontendPretty) HandleKeyPress(ctx tuist.EventContext, ev uv.KeyPressEvent) bool {
    fe.pressedKey = keyStr
    fe.pressedKeyAt = time.Now()
    // ... handle the key ...
    // Schedule highlight clear
    go func() {
        time.Sleep(keypressDuration)
        ctx.Dispatch(func() {
            fe.keymap.Update() // re-render keymap to clear highlight
        })
    }()
    return true
}
```

### Sidebar

The sidebar is currently composited via `renderWithSidebar()` which joins main content and sidebar horizontally using lipgloss. This could be:
- A separate component rendered via tuist's overlay system (positioned to the right)
- Or kept as inline lipgloss composition in `frontendPretty.Render()` (simpler)

The inline approach is fine — the sidebar content rarely changes and is cheap to compose.

### Editline / huh.Form

These are Bubbletea v1 components. Current approach: forward `uv.KeyPressEvent` → `tea.KeyMsg` via `uvKeyToTeaKeyMsg()`, forward resulting `tea.Cmd` via `execTeaCmd()`.

This can be wrapped in a thin tuist component:

```go
type EditlineWrap struct {
    tuist.Compo
    model *editline.Model
    // ...
}

func (e *EditlineWrap) HandleKeyPress(ctx tuist.EventContext, ev uv.KeyPressEvent) bool {
    msg := uvKeyToTeaKeyMsg(ev)
    newModel, cmd := e.model.Update(msg)
    e.model = newModel.(*editline.Model)
    e.execTeaCmd(ctx, cmd)
    e.Update()
    return true
}

func (e *EditlineWrap) Render(ctx tuist.RenderContext) tuist.RenderResult {
    lines := strings.Split(e.model.View(), "\n")
    return tuist.RenderResult{Lines: lines}
}
```

The tuist `teav1` bridge package could also be used here, but the manual wrapper gives more control over the key translation.

### `ShellHandler` interface

The `ShellHandler` interface currently uses `tea.KeyMsg` and `tea.Cmd`:

```go
type ShellHandler interface {
    Prompt(ctx context.Context, out TermOutput, fg termenv.Color) (string, tea.Cmd)
    ReactToInput(ctx context.Context, msg tea.KeyMsg, editing bool, edit *editline.Model) tea.Cmd
    // ...
}
```

This can stay as-is for now. The editline wrapper translates UV keys to tea.KeyMsg before calling `ReactToInput`. Migrating ShellHandler to native UV types is a follow-up.

## Migration order

Each step should leave the code compiling and working.

### Phase 1: Eliminate `fe.mu`

Move all DB/state mutations into `Dispatch()`. This is the foundational change.

1. Change `ExportSpans` to dispatch DB updates to the UI goroutine
2. Change `LogExporter.Export` to dispatch log updates to the UI goroutine
3. Change `MetricExporter.Export` similarly
4. Change `SetCloudURL`, `SetVerbosity`, `SetPrimary`, `SetClient`, `Shell`, `SetSidebarContent` to use Dispatch
5. Remove `fe.mu` and all `Lock()`/`Unlock()` calls
6. Remove `exporterDirty` map (no longer needed — updates happen on UI goroutine)
7. Remove `spanRowKey` (no longer needed — components mark themselves dirty)

### Phase 2: Extract Spinner component

1. Create `Spinner` component with self-ticking OnMount goroutine
2. Modify `SpanRowView` to mount/dismount Spinner based on running state
3. Remove global `frameLoop` (or reduce to just keypress-fade timer)
4. Remove `fe.now` field (each Spinner tracks its own time)
5. Share a single `*Rave` across all Spinners (it holds BPM state)

### Phase 3: Use `Focusable` for focus tracking

1. Add `Focusable` implementation to `SpanRowView`
2. Modify nav methods to call `ctx.SetFocus(spanRow)` instead of setting `fe.FocusedSpan`
3. Remove `fe.FocusedSpan`, `fe.focusedIdx` — use `fe.tui.focusedComponent` or track via `SetFocused` callbacks
4. Remove `focused` from `spanRowKey` (which may already be gone by this point)

### Phase 4: Eliminate viewport windowing

1. Change `frontendPretty.Render()` to iterate ALL `fe.spanRows` (no `renderLines` windowing)
2. Remove `renderLines()`, `renderProgress()`, `renderContent()` — replaced by the component tree render
3. Remove `renderedRowLines()` — each SpanRowView renders itself directly
4. Trust tuist's diff renderer to skip unchanged offscreen lines

### Phase 5: Clean up string-builder rendering

1. Move remaining string-builder renders (keymap, sidebar, zoom header, zoomed logs) into their own components
2. Remove `fe.view`, `fe.viewOut` string builders
3. Remove `renderWithSidebar()` string composition (or keep as lightweight inline logic)

## Files affected

- `dagql/idtui/frontend_pretty.go` — primary target (~3000 lines, will shrink significantly)
- `dagql/idtui/frontend.go` — `ShellHandler` interface unchanged; `Frontend` interface unchanged
- `dagql/idtui/rave.go` — `Rave` type unchanged, used by Spinner component
- `dagql/idtui/output.go` — `ColorProfile()`, `NewOutput()` unchanged
- `dagql/idtui/sys_unix.go` — `openInputTTY()`, `sigquit()` unchanged
- `codeberg.org/vito/tuist` — no changes needed in the framework

## What stays the same

- `Frontend` interface — unchanged
- `ShellHandler` interface — unchanged (still uses `tea.KeyMsg`/`tea.Cmd`)
- `dagui.DB`, `dagui.Rows`, `dagui.TraceRow` — unchanged
- `prettyLogs`, `Vterm` — unchanged
- `renderer` (the render helper for calls/spans/durations) — unchanged
- `Rave` spinner — unchanged (pure `ViewFancy(time.Time)` function)
- `FinalRender()` — still renders directly to io.Writer without component caching (it runs after TUI stops)
- UV key → tea.KeyMsg translation — still needed for editline/huh.Form/ShellHandler
- `Background()` mechanism — still stops/restarts TUI for external commands

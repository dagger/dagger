# Unified TUI focus ownership

Planning document. The interactive TUI can currently render one component's
controls while Tuist routes keyboard input to another component. This most
visibly bricks `dagger agent` around forms, but the same ownership problem can
affect any temporary view that dismisses itself.

The implementation should make Tuist the sole source of truth for keyboard
focus, remove Dagger's parallel focus booleans, and stop changing modes as a
side effect of agent turns starting or finishing.

Do not reproduce the live failure while implementing this plan. The existing
headless Tuist terminal and Dagger frontend harnesses are sufficient and avoid
wedging an interactive session.

## 1. Symptoms

Two form failures have been observed in `dagger agent`:

1. A form appears and its keymap is rendered, but input does not affect the
   form. Keypresses may still highlight the displayed controls.
2. A form accepts input and closes, but the prompt is left in a mixed state:
   its keymap implies input focus, while typing does nothing, no cursor is
   visible, and Escape does not recover.

A similar dropped-input symptom has historically appeared at
`dagger generate`'s `Apply changes?` prompt.

The visible keymap is not evidence of actual focus. In
`dagql/idtui/frontend_pretty.go`, `keys` chooses form bindings from
`formModel != nil`, while Tuist independently routes events through its private
`focusedComponent`. The application can therefore display form controls while
another component receives the keys.

## 2. Current failure paths

### 2.1 Dagger has a second focus authority

`frontendPretty.editlineFocused` is treated as input-mode truth independently
of Tuist's actual focused component. Other booleans and component-presence
checks play similar roles for search, tests, and log views.

This creates invalid combinations such as:

- `editlineFocused == true` while Tuist focuses a form;
- `editlineFocused == false` while Tuist still focuses `textInput`;
- `formModel != nil` while Tuist routes input to `textInput`;
- a removed `formWrap` remaining Tuist's focused target.

`applyTuistFocus` tries to reconstruct focus from those logical flags, but it
returns immediately when `editlineFocused`, `searchActive`, or
`logSearchInput != nil`. That is only safe if those components never lost
focus. A modal form necessarily violates that assumption.

### 2.2 Turn lifecycle can steal form focus

Submitting a prompt currently calls `startShellHandle`, which enters nav mode.
When a turn completes, `handleShellDone` may call `enterInsertMode` after a
debounce. `enterInsertMode` directly focuses `textInput` without checking for
an active form.

In an agent session with overlapping turns, one turn can be waiting on a form
while another finishes. The second turn then focuses the prompt behind the
visible form. Because `keys` still renders `formModel.KeyBinds()`, input can
visually highlight form controls even though it is routed elsewhere.

### 2.3 Form dismissal can retain a stale focus target

`removeForm` removes `formWrap`, clears the form fields, and calls
`applyTuistFocus`. If `editlineFocused` is true, `applyTuistFocus` returns
without focusing `textInput`.

Tuist intentionally keeps `focusedComponent` pointing at a dismounted
component so a temporarily removed component can be re-mounted and notified
again. For a permanently dismissed form, that means input can remain routed to
an exited Bubble Tea wrapper. The prompt remains internally blurred, so it
renders no cursor even though Dagger's input-mode keymap is visible.

### 2.4 Form lifecycle is represented as a singleton

`handlePromptForm` overwrites `formWrap`, `formModel`, and `formSpacer` without
serializing independent callers. A second form can therefore orphan the first
one in the Tuist child tree. `removeForm`'s identity guard prevents a late
callback from deleting the replacement, but cannot make the orphan interactive
again.

`HandleForm` removes an active form when its context is cancelled, but
`handlePromptBool` and `handlePromptString` return on cancellation without
removing the form they installed. Agent interruption can therefore strand a
form whose caller no longer exists.

### 2.5 An older Tuist race is already fixed

Tuist already contains two relevant fixes:

- focus notification and input for a newly added component are deferred until
  that component mounts;
- the Bubble Tea bridge initializes its dispatcher before handling input and
  correctly executes `tea.Batch` and `tea.Sequence` commands.

Those fixes were originally motivated by dropped type-ahead at Dagger's
`Apply changes?` prompt. Preserve their deferred-focus and pending-input
semantics. This plan addresses the remaining ownership and restoration
problems rather than reimplementing those fixes.

## 3. Goals

1. Tuist is the only authority for the currently focused component.
2. Temporary UI can acquire focus and reliably restore the previous target.
3. Focus restoration supports nesting and stale/out-of-order dismissal.
4. Removing or dismissing temporary UI cannot leave input routed to it.
5. Dagger keymaps, cursor behavior, and input handling derive from actual
   Tuist focus rather than parallel booleans.
6. Submitting a prompt does not change focus.
7. An agent turn completing does not change focus.
8. Navigation and input focus changes happen only in response to explicit user
   actions or explicit view presentation/dismissal.
9. Concurrent and cancelled forms cannot orphan components or waiters.

## 4. Non-goals

- Changing agent message routing, interjection semantics, or the distinction
  between focused and busy agents.
- Reworking trace-row selection. `FocusedSpan` remains the selected trace row;
  it simply stops masquerading as keyboard-focus state.
- Replacing Huh or the Bubble Tea adapter.
- Reproducing the issue in a live TUI.
- Changing Tuist's render cache, terminal input decoder, or pending-input
  ordering beyond what focus restoration requires.

## 5. Tuist design

### 5.1 Public focus identity

Expose read-only focus identity on the UI goroutine:

```go
func (t *TUI) Focused() Component
func (t *TUI) IsFocused(comp Component) bool
```

`Focused` returns the component that currently owns input routing. During the
existing add-and-focus-before-render window, it may be a deliberately deferred
unmounted target; this remains valid and continues to use Tuist's pending-input
queue.

Document both methods with the same UI-goroutine restriction as `SetFocus`.
Application render and keymap code can use `IsFocused` instead of maintaining a
second boolean.

A source-relative context helper may also be useful:

```go
func (ctx Context) IsFocused() bool
```

It is optional if the TUI-level methods are enough for Dagger.

### 5.2 Scoped temporary focus

Add a scoped focus API, with final names chosen to match Tuist conventions:

```go
type FocusHandle struct { /* private */ }

func (t *TUI) PushFocus(comp Component) *FocusHandle
func (h *FocusHandle) Restore()
```

Required semantics:

- `PushFocus` records the current target, pushes a focus frame, and focuses
  `comp` using the existing `SetFocus` path.
- `Restore` is idempotent.
- Nested handles restore in LIFO order.
- Restoring a non-top handle marks it closed but does not disturb the newer
  scope.
- When the top handle is restored, Tuist pops it and any already-closed frames
  beneath it, then restores the surviving return target.
- If the return target is no longer mounted or an active overlay, Tuist does
  not focus it. It safely falls back to `nil` rather than creating another
  stale target.
- Focus changes inside an active scope are allowed. Dismissing the scope still
  returns to the target captured by `PushFocus`.
- Existing `SetFocus`, deferred mount notification, input buffering, and
  overlay behavior remain compatible.

One possible stack algorithm is:

```text
Push A over X:       [A(previous=X)]
Push B over A:       [A(previous=X), B(previous=A)]
Restore A first:     [A(closed), B]       focus stays B
Restore B:           pop B, pop closed A, restore X
Restore B first:     pop B, restore A
Restore A afterward: pop A, restore X
```

The handle should retain TUI identity and reject or no-op cross-TUI misuse.
`Restore` must run on the UI goroutine. Dagger already dispatches form
installation and removal there.

### 5.3 Explicit removal invariant

A component explicitly removed from a `Container` must not remain an active
input target forever.

The implementation must preserve the valid case where `SetFocus` targets a
newly added component before its first render. It should distinguish that
intentional deferred mount from permanent removal. Options, in preference
order:

1. scoped users restore their focus handle before removal, and
   `Container.RemoveChild` clears focus when it explicitly removes the focused
   component or an ancestor of it;
2. the TUI tracks focus-frame ownership and automatically restores the frame
   when its scoped target is explicitly removed;
3. at minimum, explicit removal clears focus to `nil` while lazy render-only
   dismount/remount retains current behavior.

Do not simply clear every focus target in `dismountTree`; Tuist currently has a
covered remount behavior that should remain valid unless deliberately replaced
with equivalent semantics.

### 5.4 Tuist tests

Add headless tests covering:

- `Focused` and `IsFocused` before and after `SetFocus`;
- basic `PushFocus`/`Restore`;
- nested scopes;
- out-of-order restoration;
- duplicate restoration;
- restoration after the previous target was explicitly removed;
- removal of the currently focused scoped component;
- an overlay as either temporary or previous focus;
- focus pushed before the target's first render;
- type-ahead queued until mount, preserving the existing regression test;
- no input delivery to a permanently dismissed component.

Likely files in `github.com/vito/tuist`:

- `tui.go`;
- `component.go` if explicit removal participates;
- `focus_defer_test.go` or a focused companion test file.

## 6. Dagger design

### 6.1 Remove duplicate input-focus state

Delete `frontendPretty.editlineFocused` as a field and replace every read with
actual Tuist focus:

```go
func (fe *frontendPretty) inputFocused() bool {
    return fe.textInput != nil && fe.tui.IsFocused(fe.textInput)
}
```

This helper is a projection, not stored state. It may be used by keymap and
layout code, but it must never be independently mutated.

Audit all current `editlineFocused` uses, including:

- keymap selection;
- `focus` and navigation behavior;
- `handleInputComplete`;
- prompt synchronization and rendering;
- cursor visibility;
- search entry/exit;
- `enterNavMode` and `enterInsertMode`;
- shell completion handling.

`TextInput.OnSubmit` is only reached through focused input routing, so
`handleInputComplete` should not need a separate logical-focus guard.

### 6.2 Stop turn-driven mode switching

Change prompt submission and turn completion as agreed:

- remove the `enterNavMode(true)` call from `startShellHandle`;
- remove the auto-insert block from `handleShellDone`;
- remove `autoModeSwitch`;
- remove `navKeyAt`;
- remove `autoModeSwitchDebounce` and its comments/tests;
- remove the `auto` parameters from `enterNavMode` and `enterInsertMode` if no
  explicit caller still needs them.

The resulting behavior is:

- submitting from the prompt leaves the prompt focused;
- the user may continue typing or interject while turns run;
- Escape explicitly enters navigation mode;
- `i` or another explicit input action returns to the prompt;
- a turn finishing never moves focus or changes the meaning of the next key.

### 6.3 Forms own scoped focus

Replace the form singleton's implicit focus manipulation with a per-form
record, for example:

```go
type activePromptForm struct {
    model  *huh.Form
    wrap   *teav1.Wrap
    spacer *blankLine
    focus  *tuist.FocusHandle
    done   func(*huh.Form)
}
```

When presenting a form:

1. add its wrapper and spacer;
2. acquire `focus := fe.tui.PushFocus(wrap)`;
3. update rendering/keymap state;
4. on quit or cancellation, call `focus.Restore()` before removing the
   wrapper;
5. remove exactly that form's wrapper and spacer;
6. invoke its result callback only after teardown, preserving the current
   chained-form fix.

The keymap should show form bindings only when the active form actually owns
focus. It must not infer focus solely from `formModel != nil`.

### 6.4 Serialize independent forms

Only one form can receive keyboard input. Independent `HandleForm` callers
should therefore queue rather than overwrite the active form fields.

Maintain an active form plus a FIFO of pending form requests. A queued request
contains its model, caller context, completion channel, and result callback.
When the active form exits or is cancelled, teardown and restore its focus,
then activate the next live request.

Cancellation rules:

- cancelling the active request dismisses it and activates the next request;
- cancelling a queued request removes it without ever mounting it;
- cancellation and form completion are idempotent and race-safe on the UI
  goroutine;
- a late callback can only affect its own request record.

Route `handlePromptBool` and `handlePromptString` through the same lifecycle as
`HandleForm` so all prompt forms get identical cancellation cleanup.

Internal chained forms, such as branch-with-custom-summary, may enqueue the
next form from the first form's callback. Because teardown happens before the
callback, the next form can activate immediately without leaking the first.

### 6.5 Eliminate `applyTuistFocus`

Delete `applyTuistFocus`; do not replace it with another global reconstruction
function.

Use explicit focus operations at transitions:

- shell startup explicitly focuses `textInput`;
- Escape explicitly focuses the current navigation target;
- entering search pushes search-input focus and restores it on exit;
- opening a form pushes form focus and restores it on dismissal;
- opening the log pager or tests view pushes focus if it is temporary and
  restores the returned handle on close;
- opening log-pager search pushes its input and restores the pager on exit;
- selecting a trace row changes Tuist focus only when the currently focused
  component is part of the navigation surface.

A small projection such as `navigationFocused()` is acceptable if it checks
Tuist's actual focused component. A helper that guesses the globally desired
focus from mode booleans is not.

`FocusedSpan` continues to track row selection. When row synchronization
creates a `SpanTreeView` after selection, it may explicitly transfer focus from
the stable navigation fallback (`fe`) to that new view, but only if Tuist says
focus is currently in the navigation surface. It must not steal focus from an
input, form, search field, pager, or test view.

### 6.6 Cursor and keymap

The prompt cursor should follow `TextInput.SetFocused`, which Tuist already
calls through `Focusable`. Audit `SetShowHardwareCursor` calls so the terminal
cursor cannot become another logical focus flag.

Preferred behavior is for hardware-cursor visibility to be derived from the
focused render result. If that is outside this change, keep the flag as a
rendering side effect of explicit focus transitions, never as an authority used
to decide which component is focused.

Keymap precedence must follow actual Tuist focus:

1. focused form;
2. focused search input;
3. focused log search/pager;
4. focused tests view;
5. focused prompt input;
6. navigation surface.

Component presence alone must not select a keymap.

## 7. Dagger tests

Use `tuist.NewHeadlessTerminal` and the existing frontend test driver. Do not
start a live agent TUI.

Add regression coverage for:

1. Shell startup focuses the prompt and shows its cursor.
2. Submitting a prompt leaves the prompt focused.
3. A turn completing leaves whichever component was focused untouched.
4. Escape explicitly moves prompt input to navigation.
5. A later turn completion does not move navigation back to input.
6. A form presented over prompt input receives keys and restores prompt input
   after accept.
7. After restoration, typing edits the prompt, the cursor is visible, and
   Escape enters navigation.
8. A form presented over navigation restores the exact prior navigation
   target when it still exists.
9. A prior target removed while the form is open restores safely without a
   stale focus pointer.
10. A turn finishing while a form is open cannot steal focus from it.
11. Form cancellation removes the form and restores focus.
12. `HandlePrompt` bool/string cancellation uses the same cleanup.
13. Chained forms remove the first before focusing the second.
14. Two concurrent form requests are serialized and each waiter receives its
    own result.
15. Keymap contents always correspond to the component receiving injected
    keys.
16. Search, log-pager search, tests view, and log pager restore their prior
    focus through handles.

Likely Dagger files:

- `dagql/idtui/frontend_pretty.go`;
- `dagql/idtui/frontend_log_pager.go`;
- `dagql/idtui/frontend_tests.go`;
- existing `dagql/idtui/*_test.go` files, preferably the headless focus tests;
- `go.mod` and `go.sum` for the Tuist revision.

## 8. Implementation sequence

### Phase 1: Tuist focus primitives

1. Implement focus identity accessors.
2. Implement scoped focus handles and stack semantics.
3. Define explicit-removal behavior without regressing pre-mount focus.
4. Add and run Tuist headless tests.
5. Open and merge, or otherwise pin, the Tuist change before adapting Dagger.

### Phase 2: Minimal Dagger form integration

1. Bump Tuist.
2. Give forms scoped focus handles.
3. Restore before removal on accept, abort, and context cancellation.
4. Add the two direct form regressions: form receives input; prompt works after
   dismissal.

This phase should already prevent both observed brick states.

### Phase 3: Remove turn-driven focus changes

1. Leave focus unchanged in `startShellHandle`.
2. Leave focus unchanged in `handleShellDone`.
3. Delete auto-mode state and debounce logic.
4. Update focus tests for the new explicit-only behavior.

### Phase 4: Remove duplicate focus state

1. Replace `editlineFocused` reads with Tuist projections.
2. Migrate keymap and cursor decisions.
3. Migrate search, pager, and tests focus restoration to scoped handles.
4. Replace navigation focus reconstruction with explicit navigation-only
   transitions.
5. Delete `applyTuistFocus`.

### Phase 5: Form concurrency and cancellation

1. Introduce per-form request records.
2. Queue independent forms FIFO.
3. Unify `HandleForm`, bool prompts, and string prompts.
4. Cover queued cancellation and chained forms.

### Phase 6: Validation and cleanup

1. Run Tuist tests, including race tests.
2. Run focused `dagql/idtui` tests.
3. Run the relevant CLI/integration tests for `dagger agent` and
   `dagger generate` without opening a live TUI.
4. Grep for `editlineFocused`, `applyTuistFocus`, `autoModeSwitch`, and
   `navKeyAt`; all should be gone.
5. Review every remaining `SetFocus` call. Each should correspond to an
   explicit user action, component presentation, scoped restoration, or
   navigation target becoming available.

## 9. Acceptance criteria

The work is complete when:

- Tuist exposes one observable focus target and scoped restoration.
- Dagger stores no independent input-focused boolean.
- `applyTuistFocus` and automatic turn-completion focus changes are removed.
- A visible form necessarily receives keyboard input.
- Dismissing or cancelling a form restores the previous live component.
- A removed form can never remain the keyboard target.
- The displayed keymap and cursor agree with the component receiving keys.
- Prompt submission and turn completion leave focus untouched.
- Concurrent form callers cannot overwrite or orphan each other.
- All behavior is covered headlessly, with no live reproduction required.

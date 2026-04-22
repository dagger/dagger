package idtui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/adrg/xdg"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/vito/bubbline/history"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/term"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"

	"github.com/vito/tuist"
	"github.com/vito/tuist/teav1"
)

var historyFile = filepath.Join(xdg.DataHome, "dagger", "histfile")

var (
	ErrShellExited = errors.New("shell exited")
	ErrInterrupted = errors.New("interrupted")
)

// windowSize replaces tea.WindowSizeMsg for terminal dimensions.
type windowSize struct {
	Width, Height int
}

// backgroundRequest communicates Background calls to the main run loop.
type backgroundRequest struct {
	cmd  ExecCommand
	raw  bool
	done chan error
}

type frontendPretty struct {
	tuist.Compo

	dagui.FrontendOpts

	// telemetryError records errors from the OTel telemetry pipeline.
	telemetryError atomic.Pointer[error]

	dag *dagger.Client

	// don't show live progress; just print a full report at the end
	reportOnly bool
	reportMu   sync.Mutex // protects state in reportOnly mode (no TUI event loop)

	// updated by Run
	tui         *tuist.TUI
	run         func(context.Context) (cleanups.CleanupF, error)
	runCtx      context.Context
	interrupt   context.CancelCauseFunc
	interrupted bool
	quitting    bool
	done        bool
	err         error
	cleanup     func()

	// lifecycle channels
	quit          chan struct{}
	backgroundReq chan backgroundRequest

	// updated by Shell
	shell           ShellHandler
	shellCtx        context.Context
	shellInterrupt  context.CancelCauseFunc
	promptFg        termenv.Color
	promptErr       error
	promptErrLabel  *ErrorLabel
	textInput       *tuist.TextInput
	completionMenu  *tuist.CompletionMenu
	keymapBar       *KeymapBar
	editlineFocused bool
	inputHistory    []string // raw encoded history entries (with mode prefix)
	historyIndex    int      // -1 = not browsing history
	historySaved    string   // saved input when browsing history
	autoModeSwitch  bool
	shellRunning    bool
	shellLock       sync.Mutex

	// updated as events are written
	db           *dagui.DB
	logs         *prettyLogs
	eof          bool
	backgrounded bool
	autoFocus    bool
	rowsView     *dagui.RowsView
	rows         *dagui.Rows
	pressedKey   string
	pressedKeyAt time.Time

	// set when authenticated to Cloud
	cloudURL string

	// TUI state/config
	spinnerEpoch time.Time // shared epoch so all spinners animate in sync
	profile      termenv.Profile
	window       windowSize // terminal dimensions
	contentWidth int
	browserBuf   *strings.Builder      // logs if browser fails
	finalRender  bool                  // whether we're doing the final render
	shownErrs    map[dagui.SpanID]bool // which errors we've rendered
	stdin        io.Reader             // used by backgroundMsg for running terminal
	writer       io.Writer

	// notification bubbles (single overlay with a Container of bubbles)
	notifications         map[string]*NotificationBubble // keyed by section title
	notificationContainer *tuist.Container
	notificationOverlay   *tuist.OverlayHandle

	// messages to print before the final render
	msgPreFinalRender strings.Builder

	// Add prompt field
	formWrap  *teav1.Wrap // bubbletea v1 adapter for huh.Form
	formModel *huh.Form   // direct reference for KeyBinds()

	// track whether we've already spawned the run function
	spawned bool

	// per-span tree components for incremental rendering
	spanTrees           map[dagui.SpanID]*SpanTreeView
	topTrees            []*SpanTreeView // top-level tree views, ordered
	renderVersion       uint64          // bumped on global render config changes (verbosity, zoom)
	lastRenderedVersion uint64          // renderVersion at last Render, for detecting changes

	// viewDirty is set when DB data changes (ExportSpans, LogExport) and
	// cleared by recalculateViewLocked in Render. This coalesces multiple
	// data updates into a single recalculate per render frame.
	viewDirty bool

	// search state (Vim-style "/" search)
	searchActive         bool                  // search input bar is shown
	searchQuery          string                // confirmed search string
	searchInput          *tuist.TextInput      // the "/" prompt input (non-nil while searchActive)
	searchMatches        []searchMatch         // ordered list of all matches
	searchMatchSpans     map[dagui.SpanID]bool // fast lookup: does this span have any match?
	prevSearchMatchSpans map[dagui.SpanID]bool // previous frame's matchSpans for diff-based dirtying
	searchIdx            int                   // current match index (-1 = none)
}

// Verify interface compliance at compile time.
var (
	_ tuist.Component   = (*frontendPretty)(nil)
	_ tuist.Interactive = (*frontendPretty)(nil)
	_ tuist.Mounter     = (*frontendPretty)(nil)
)

// treePrefix holds pre-computed prefix strings for a SpanTreeView.
// These are set by the parent SpanTreeView when rendering its children.
// By computing prefixes top-down through the tree, we avoid the stale-prefix
// problem that occurred when each row walked up the TraceRow parent chain
// independently.
type treePrefix struct {
	// step is the prefix for the step title line (ancestor bars + connector).
	// e.g., "│ ├╴" for a non-last child at depth 2.
	step string
	// cont is the prefix for continuation lines (ancestor bars + bar/space).
	// e.g., "│ │ " for a non-last child at depth 2.
	cont string
	// forChildren is the accumulated ancestor bars to pass to this node's
	// children. Equal to cont (the parent's column continues for children).
	forChildren string
	// contWidth is the visual width of cont (for available width calculation).
	contWidth int
}

// SpanTreeView is a tuist component that renders a TraceTree node and its
// children recursively. This is the tree-based replacement for SpanRowView.
//
// The parent SpanTreeView computes and sets the prefix strings for each
// child before calling RenderChild. When a parent's status changes, it
// re-renders, recomputing child prefixes — so prefixes are always fresh.
//
// Children that haven't changed return cached results from RenderChild.
// The parent just concatenates cached child lines, which is O(pointers).
type SpanTreeView struct {
	tuist.Compo
	fe     *frontendPretty
	spanID dagui.SpanID

	// parent points to the parent SpanTreeView (nil for top-level nodes).
	parent *SpanTreeView
	// indexInParent is this node's position in parent.children (or in
	// fe.topTrees for top-level nodes).
	indexInParent int

	// prefix holds the pre-computed indentation from ancestors.
	// Set by the parent before RenderChild is called.
	prefix treePrefix

	// children are the expanded child SpanTreeViews, ordered.
	children []*SpanTreeView
	// childMap indexes children by span ID for reuse across renders.
	childMap map[dagui.SpanID]*SpanTreeView

	// spinner is non-nil when the span is running. Rendered as a child
	// component (via statusIcon → RenderChildInline) so tuist manages
	// its lifecycle (mount starts the tick goroutine, dismount stops it).
	spinner *tuist.Spinner

	// childrenGapPrefix is the prefix for gap lines between this node's
	// children. It shows all ancestor bars + this node's own bar column.
	// Computed by syncTreeNode. Unlike a child's prefix.cont (which omits
	// the parent bar for last children), this always shows the parent bar.
	childrenGapPrefix string

	// focused tracks whether this span is the currently focused span.
	// Synced by tuist's SetFocus → SetFocused callback.
	focused bool

	// debugged tracks whether debug info is shown for this span.
	// Toggled by the "?" key.
	debugged bool

	// Render metadata — set during Render() for focus-line lookup.
	// These are output-derived values, not input state that drives rendering.
	selfLineCount   int   // lines from self content (before children)
	childGapCounts  []int // gap line count before each child
	childLineCounts []int // total line count from each child's RenderChild
}

var _ tuist.Component = (*SpanTreeView)(nil)
var _ tuist.Focusable = (*SpanTreeView)(nil)

// SetFocused is called by tuist when this component gains or loses focus.
// This is O(1) — only the old and new focused components are notified.
func (s *SpanTreeView) SetFocused(_ tuist.Context, focused bool) {
	if s.focused != focused {
		s.focused = focused
		s.Update()
	}
}

// Render produces the lines for this span tree node and its children.
// This method is stateless — all component state (prefix, children,
// focus, spinner) is synced by syncSpanTreeState() before Render runs.
func (s *SpanTreeView) Render(ctx tuist.Context) {
	row := s.fe.rows.BySpan[s.spanID]
	if row == nil {
		return
	}

	r := newRenderer(s.fe.db, s.fe.contentWidth/2, s.fe.FrontendOpts, s.fe.finalRender)

	s.selfLineCount = 0

	// Render the title (renderStep) into a separate buffer so we can
	// apply search highlighting to it without double-highlighting the
	// vterm log output (which handles its own highlighting via
	// SearchQuery/SearchCurrentRow).
	titleBuf := new(strings.Builder)
	titleOut := NewOutput(titleBuf, termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(titleOut)
	s.fe.renderStep(ctx, titleOut, r, row, "")
	titleText := titleBuf.String()
	if titleText != "" {
		titleLines := strings.Split(strings.TrimSuffix(titleText, "\n"), "\n")
		// Highlight search matches in title lines only (not logs).
		if s.fe.searchQuery != "" && s.fe.searchMatchSpans[row.Span.ID] {
			style := matchHighlight
			if s.fe.searchIdx >= 0 && s.fe.searchIdx < len(s.fe.searchMatches) {
				cm := s.fe.searchMatches[s.fe.searchIdx]
				if cm.spanID == row.Span.ID && cm.logRow == -1 {
					style = currentMatchHighlight
				}
			}
			for i, line := range titleLines {
				titleLines[i] = highlightANSI(line, s.fe.searchQuery, style)
			}
		}
		s.selfLineCount += len(titleLines)
		ctx.Lines(titleLines...)
	}

	// Render the rest (logs, errors, debug) into a separate buffer.
	// Log highlighting is handled by the Vterm's own SearchQuery state,
	// so we do NOT apply highlightANSI to these lines.
	restBuf := new(strings.Builder)
	restOut := NewOutput(restBuf, termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(restOut)
	s.fe.renderRowContentRest(ctx, restOut, r, row, "")
	restText := restBuf.String()
	if restText != "" {
		restLines := strings.Split(strings.TrimSuffix(restText, "\n"), "\n")
		s.selfLineCount += len(restLines)
		ctx.Lines(restLines...)
	}

	// Render children (already synced by syncSpanTreeState).
	s.childGapCounts = s.childGapCounts[:0]
	s.childLineCounts = s.childLineCounts[:0]
	for _, child := range s.children {
		// Gap line between children — uses parent's gap prefix (which always
		// shows the parent bar), not the child's prefix.cont (which omits
		// the parent bar for the last child).
		var gapCount int
		childRow := s.fe.rows.BySpan[child.spanID]
		if childRow != nil {
			gaps := s.fe.renderTreeGap(r, childRow, s.childrenGapPrefix)
			gapCount = len(gaps)
			ctx.Lines(gaps...)
		}

		childCtx := ctx
		childCtx.Width = ctx.Width - child.prefix.contWidth
		result := s.RenderChildResult(childCtx, child)
		ctx.Lines(result.Lines...)

		s.childGapCounts = append(s.childGapCounts, gapCount)
		s.childLineCounts = append(s.childLineCounts, len(result.Lines))
	}
}

// indentFunc returns a fancyIndent override that uses the pre-computed prefix.
// It only applies to the SpanTreeView's own span; other rows (e.g., synthetic
// rows from renderErrorCause) return false to fall through to the original
// fancyIndent which walks the row's parent chain.
func (s *SpanTreeView) indentFunc(out TermOutput) func(TermOutput, *dagui.TraceRow, bool, bool) bool {
	return func(o TermOutput, row *dagui.TraceRow, selfBar, selfHoriz bool) bool {
		// Only use tree prefix for our own span. Other rows (synthetic
		// rootCauseRows, etc.) need the original parent-chain walk.
		if row.Span.ID != s.spanID {
			return false
		}
		if selfHoriz {
			fmt.Fprint(o, s.prefix.step)
		} else if selfBar {
			fmt.Fprint(o, s.prefix.cont)
			// Also render self bar (for multi-line call args)
			span := row.Span
			color := restrainedStatusColor(span)
			var symbol string
			if row.ShowingChildren && !row.Span.Reveal {
				symbol = VertBar
			} else {
				symbol = " "
			}
			fmt.Fprint(o, out.String(symbol+" ").Foreground(color).Faint())
		} else {
			fmt.Fprint(o, s.prefix.cont)
		}
		return true
	}
}

// computeChildPrefix computes the prefix for a child at the given position.
func (s *SpanTreeView) computeChildPrefix(out TermOutput, hasNext bool) treePrefix {
	row := s.fe.rows.BySpan[s.spanID]
	if row == nil {
		return treePrefix{}
	}
	span := row.Span
	color := restrainedStatusColor(span)

	var connector, bar string
	if len(span.RevealedSpans.Order) > 0 || span.Reveal {
		// Revealed spans are visually indented beneath their parent,
		// not connected with tree lines.
		connector = "  "
		bar = "  "
	} else if hasNext {
		connector = out.String(VertRightBar + HorizHalfLeftBar).Foreground(color).Faint().String()
		bar = out.String(VertBar + " ").Foreground(color).Faint().String()
	} else {
		connector = out.String(CornerBottomLeft + HorizHalfLeftBar).Foreground(color).Faint().String()
		bar = "  "
	}

	return treePrefix{
		step:        s.prefix.forChildren + connector,
		cont:        s.prefix.forChildren + bar,
		forChildren: s.prefix.forChildren + bar,
		contWidth:   s.prefix.contWidth + 2,
	}
}

// getOrCreateSpanTree returns the SpanTreeView for the given span ID,
// creating one if it doesn't exist.
func (fe *frontendPretty) getOrCreateSpanTree(spanID dagui.SpanID) *SpanTreeView {
	if fe.spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}
	st, ok := fe.spanTrees[spanID]
	if !ok {
		st = &SpanTreeView{
			fe:     fe,
			spanID: spanID,
		}
		fe.spanTrees[spanID] = st
	}
	fe.syncSpinnerTree(st)
	return st
}

// syncSpinnerTree sets or clears the spinner on a SpanTreeView based on
// whether the span is currently running. The spinner lifecycle is
// managed by RenderChildInline in SpanTreeView.Render via statusIcon —
// tuist auto-mounts it and auto-dismounts when the SpanTreeView stops
// rendering it.
func (fe *frontendPretty) syncSpinnerTree(st *SpanTreeView) {
	tree := fe.rowsView.BySpan[st.spanID]
	running := tree != nil && tree.Span.IsRunningOrEffectsRunning()
	if running && st.spinner == nil {
		sp := tuist.NewSpinner()
		sp.Epoch = fe.spinnerEpoch
		st.spinner = sp
	} else if !running && st.spinner != nil {
		st.spinner = nil
	}
}

func (fe *frontendPretty) SetClient(client *dagger.Client) {
	fe.dispatch(func() {
		fe.dag = client
	})
}

func NewPretty(w io.Writer) Frontend {
	return NewWithDB(w, dagui.NewDB())
}

func NewReporter(w io.Writer) Frontend {
	fe := NewWithDB(w, dagui.NewDB())
	fe.reportOnly = true
	return fe
}

// dispatch runs fn on the TUI event loop goroutine (when the TUI is running)
// or directly under a mutex (in reportOnly mode where there is no event loop).
func (fe *frontendPretty) dispatch(fn func()) {
	if fe.reportOnly {
		fe.reportMu.Lock()
		defer fe.reportMu.Unlock()
		fn()
	} else {
		fe.tui.Dispatch(fn)
	}
}

func NewWithDB(w io.Writer, db *dagui.DB) *frontendPretty {
	profile := ColorProfile()
	tui := tuist.New(tuist.NewStdTerminal())
	fe := &frontendPretty{
		db:        db,
		logs:      newPrettyLogs(profile, db),
		autoFocus: true,

		// set empty initial row state to avoid nil checks
		rowsView: &dagui.RowsView{},
		rows:     &dagui.Rows{BySpan: map[dagui.SpanID]*dagui.TraceRow{}},

		// initial TUI state
		tui:           tui,
		spinnerEpoch:  time.Now(),
		window:        windowSize{Width: -1, Height: -1}, // be clear that it's not set
		profile:       profile,
		browserBuf:    new(strings.Builder),
		notifications: make(map[string]*NotificationBubble),
		writer:        w,
		shownErrs:     map[dagui.SpanID]bool{},
	}
	tui.AddChild(fe)
	return fe
}

func (fe *frontendPretty) SetSidebarContent(section SidebarSection) {
	fe.dispatch(func() {
		title := section.Title

		if bubble, ok := fe.notifications[title]; ok {
			// Update existing bubble
			bubble.section = section
			bubble.Update()
		} else {
			// Create new bubble
			bubble := newNotificationBubble(fe, section)
			fe.notifications[title] = bubble

			// Lazily create the container and overlay on first notification
			if fe.notificationContainer == nil {
				fe.notificationContainer = &tuist.Container{}
				fe.notificationOverlay = fe.tui.ShowOverlay(fe.notificationContainer, &tuist.OverlayOptions{
					Width:  tuist.SizeAbs(notificationWidth(fe.window.Width)),
					Anchor: tuist.AnchorTopRight,
					Margin: tuist.OverlayMargin{Right: 1},
				})
			}

			// Untitled goes first, titled appends
			if title == "" {
				fe.notificationContainer.Children = append(
					[]tuist.Component{bubble},
					fe.notificationContainer.Children...,
				)
				fe.notificationContainer.Update()
			} else {
				fe.notificationContainer.AddChild(bubble)
			}
		}

		fe.Update()
	})
}

func (fe *frontendPretty) Shell(ctx context.Context, handler ShellHandler) {
	fe.dispatch(func() {
		fe.startShell(ctx, handler)
		fe.Update()
	})
	<-ctx.Done()
	fe.dispatch(func() {
		fe.stopShell()
		fe.Update()
	})
}

func (fe *frontendPretty) startShell(ctx context.Context, handler ShellHandler) {
	fe.shell = handler
	fe.shellCtx = ctx
	fe.promptFg = termenv.ANSIGreen

	fe.initTextInput()

	// restore history — store raw encoded entries to preserve mode prefixes
	if hist, err := history.LoadHistory(historyFile); err == nil {
		fe.inputHistory = hist
	}
	fe.historyIndex = -1

	// wire up auto completion
	fe.completionMenu = tuist.NewCompletionMenu(fe.textInput, func(input string, cursorPos int) tuist.CompletionResult {
		return handler.AutoComplete(input, cursorPos)
	})

	// Intercept special keys before TextInput processes them.
	fe.textInput.KeyInterceptor = fe.interceptEditlineKey

	// Insert errorLabel + textInput before keymapBar: output → error → prompt → keymap
	fe.promptErrLabel = NewErrorLabel()
	fe.tui.RemoveChild(fe.keymapBar)
	fe.tui.AddChild(fe.promptErrLabel)
	fe.tui.AddChild(fe.textInput)
	fe.tui.AddChild(fe.keymapBar)
	fe.tui.SetShowHardwareCursor(true)

	// put the bowtie on
	fe.syncPrompt()
	fe.tui.SetFocus(fe.textInput)
	fe.editlineFocused = true
	fe.keymapBar.Update()
}

func (fe *frontendPretty) stopShell() {
	// save history before clearing shell state
	fe.saveHistory()

	if fe.promptErrLabel != nil {
		fe.tui.RemoveChild(fe.promptErrLabel)
		fe.promptErrLabel = nil
	}
	if fe.textInput != nil {
		fe.tui.RemoveChild(fe.textInput)
		fe.textInput = nil
	}
	if fe.notificationOverlay != nil {
		fe.notificationOverlay.Remove()
		fe.notificationOverlay = nil
		fe.notificationContainer = nil
		fe.notifications = make(map[string]*NotificationBubble)
	}
	fe.shell = nil
	fe.shellCtx = nil
	fe.completionMenu = nil
	fe.editlineFocused = false
	fe.tui.SetShowHardwareCursor(false)
}

func (fe *frontendPretty) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	if fe.OpenWeb {
		if err := browser.OpenURL(url); err != nil {
			slog.Warn("failed to open URL", "url", url, "err", err)
		}
	}
	fe.dispatch(func() {
		fe.cloudURL = url
		if msg != "" {
			slog.Warn(msg)
		}

		if cmdContext, ok := FromCmdContext(ctx); ok && cmdContext.printTraceLink {
			if logged {
				fe.msgPreFinalRender.WriteString(traceMessage(fe.profile, url, msg))
			} else if !skipLoggedOutTraceMsg() {
				fe.msgPreFinalRender.WriteString(fmt.Sprintf(loggedOutTraceMsg, url))
			}
		}
		fe.Update()
	})
}

func traceMessage(profile termenv.Profile, url string, msg string) string {
	buffer := &bytes.Buffer{}
	out := NewOutput(buffer, termenv.WithProfile(profile))

	fmt.Fprint(buffer, out.String("Full trace at ").Bold().String())
	fmt.Fprint(buffer, url)
	if msg != "" {
		fmt.Fprintf(buffer, " (%s)", msg)
	}

	return buffer.String()
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *frontendPretty) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) (cleanups.CleanupF, error)) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	if opts.GCThreshold == 0 {
		opts.GCThreshold = 1 * time.Second
	}
	fe.FrontendOpts = opts

	if fe.reportOnly {
		cleanup, err := run(ctx)
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		fe.err = err
	} else {
		// run the function wrapped in the TUI
		fe.err = fe.runWithTUI(ctx, run)
	}

	// print the final output display to stderr
	if renderErr := fe.FinalRender(os.Stderr); renderErr != nil {
		return renderErr
	}

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)

	// return original err
	return normalizeFrontendExit(fe.err, fe.db)
}

func (fe *frontendPretty) HandlePrompt(ctx context.Context, title, prompt string, dest any) error {
	switch x := dest.(type) {
	case *bool:
		return fe.handlePromptBool(ctx, title, prompt, x)
	case *string:
		return fe.handlePromptString(ctx, title, prompt, x)
	default:
		return fmt.Errorf("unsupported prompt destination type: %T", dest)
	}
}

func (fe *frontendPretty) HandleForm(ctx context.Context, form *huh.Form) error {
	done := make(chan struct{}, 1)

	fe.dispatch(func() {
		fe.handlePromptForm(form, func(f *huh.Form) {
			close(done)
		})
		fe.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// blankLine is a trivial component that renders a single empty line.
type blankLine struct{ tuist.Compo }

func (*blankLine) Render(ctx tuist.Context) {
	ctx.Line("")
}

func (fe *frontendPretty) handlePromptForm(form *huh.Form, result func(*huh.Form)) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit
	fe.formModel = form.WithTheme(huh.ThemeBase16()).WithShowHelp(false)
	fe.formWrap = teav1.New(fe.formModel)
	formSpacer := &blankLine{}
	fe.formWrap.OnQuit(func() {
		result(fe.formModel)
		fe.tui.RemoveChild(fe.formWrap)
		fe.tui.RemoveChild(formSpacer)
		fe.formWrap = nil
		fe.formModel = nil
		fe.applyTuistFocus() // restore focus to the correct SpanTreeView
		fe.Update()
	})
	// Insert before keymapBar
	fe.tui.RemoveChild(fe.keymapBar)
	fe.tui.AddChild(fe.formWrap)
	fe.tui.AddChild(formSpacer)
	fe.tui.AddChild(fe.keymapBar)
	fe.tui.SetFocus(fe.formWrap)
}

func (fe *frontendPretty) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
}

func (fe *frontendPretty) SetVerbosity(n int) {
	fe.dispatch(func() {
		fe.Opts().Verbosity = n
		fe.Update()
	})
}

func (fe *frontendPretty) SetTelemetryError(err error) {
	fe.telemetryError.Store(&err)
}

func (fe *frontendPretty) SetPrimary(spanID dagui.SpanID) {
	fe.dispatch(func() {
		fe.db.SetPrimarySpan(spanID)
		fe.ZoomedSpan = spanID
		fe.FocusedSpan = spanID
		fe.recalculateViewLocked()
		fe.Update()
	})
}

func (fe *frontendPretty) RevealAllSpans() {
	fe.dispatch(func() {
		fe.ZoomedSpan = dagui.SpanID{}
		fe.Update()
	})
}

func (fe *frontendPretty) runWithTUI(ctx context.Context, run func(context.Context) (cleanups.CleanupF, error)) (rerr error) {
	// wire up the run so we can call it asynchronously with the TUI running
	fe.run = run
	// set up ctx cancellation so the TUI can interrupt via keypresses
	fe.runCtx, fe.interrupt = context.WithCancelCause(ctx)

	fe.quit = make(chan struct{})
	fe.backgroundReq = make(chan backgroundRequest)

	in, _ := findTTYs()
	if in == nil {
		tty, err := openInputTTY()
		if err != nil {
			return err
		}
		if tty != nil {
			in = tty
			defer tty.Close()
		}
	}
	// store in fe to use in Background processing
	fe.stdin = in

	// prevent browser.OpenURL from breaking the TUI if it fails
	browser.Stdout = fe.browserBuf
	browser.Stderr = fe.browserBuf

	// Create and start the TUI
	fe.startTUI()

	// Main loop: wait for quit or background requests
	for {
		select {
		case <-fe.quit:
			fe.tui.Stop()

			// if the ctx was canceled, we don't need to return whatever random garbage
			// error string we got back; just return the ctx err.
			if fe.runCtx.Err() != nil {
				return context.Cause(fe.runCtx)
			}
			return fe.err

		case req := <-fe.backgroundReq:
			req.done <- fe.tui.Exec(func(in io.Reader, out io.Writer, errOut io.Writer) error {
				req.cmd.SetStdin(in)
				req.cmd.SetStdout(out)
				req.cmd.SetStderr(errOut)

				if req.raw {
					if stdin, ok := fe.stdin.(*os.File); ok {
						oldState, rawErr := term.MakeRaw(int(stdin.Fd()))
						if rawErr != nil {
							return rawErr
						}
						defer func() {
							if oldState != nil {
								term.Restore(int(stdin.Fd()), oldState)
							}
						}()
					}
				}
				return req.cmd.Run()
			})
		}
	}
}

func (fe *frontendPretty) startTUI() {
	if p := os.Getenv("TUIST_LOG"); p != "" {
		if f, err := os.Create(p); err == nil {
			fe.tui.SetDebugWriter(f)
		}
	}
	fe.keymapBar = &KeymapBar{
		Profile:          fe.profile,
		UsingCloudEngine: fe.UsingCloudEngine,
		Keys:             fe.keys,
	}
	fe.tui.AddChild(fe.keymapBar)
	fe.tui.SetFocus(fe)
	fe.tui.Start()
}

// OnMount is called by tuist when the component is mounted into the TUI tree.
// It starts the frame ticker and, on the first mount, spawns the run function.
func (fe *frontendPretty) OnMount(ctx tuist.Context) {
	if !fe.spawned && fe.run != nil {
		fe.spawned = true
		// Spawn the run function
		go fe.spawnRun()
	}
}

// recordKeyPress updates the pressed-key state on both the frontend and the
// keymapBar component, then schedules a clear after the highlight fades.
func (fe *frontendPretty) recordKeyPress(keyStr string) {
	fe.pressedKey = keyStr
	fe.pressedKeyAt = time.Now()
	if fe.keymapBar != nil {
		fe.keymapBar.PressedKey = keyStr
		fe.keymapBar.PressedKeyAt = fe.pressedKeyAt
		fe.keymapBar.Update()
	}
	fe.scheduleKeypressClear()
}

// scheduleKeypressClear starts a one-shot timer that re-renders the keymap
// after the keypress highlight fades. Replaces the old polling frameLoop.
func (fe *frontendPretty) scheduleKeypressClear() {
	go func() {
		time.Sleep(keypressDuration + 50*time.Millisecond)
		fe.dispatch(func() {
			if fe.keymapBar != nil {
				fe.keymapBar.Update()
			}
		})
	}()
}

func (fe *frontendPretty) spawnRun() {
	cleanup, err := fe.run(fe.runCtx)
	fe.dispatch(func() {
		if !fe.NoExit || fe.interrupted {
			if cleanup != nil {
				go func() {
					if cleanErr := cleanup(); cleanErr != nil {
						slog.Error("cleanup failed", "err", cleanErr)
					}
					fe.dispatch(func() {
						fe.handleDone(err)
					})
				}()
			} else {
				fe.handleDone(err)
			}
		} else {
			fe.cleanup = func() {
				if cleanup != nil {
					if cleanErr := cleanup(); cleanErr != nil {
						slog.Error("cleanup failed", "err", cleanErr)
					}
				}
			}
			fe.handleDone(err)
		}
	})
}

func (fe *frontendPretty) handleDone(err error) {
	slog.Debug("run finished", "err", err)
	fe.done = true
	fe.err = err
	if fe.eof && (!fe.NoExit || fe.interrupted) {
		fe.quitting = true
		fe.doQuit()
	}
	fe.Update()
}

func (fe *frontendPretty) handleEOF() {
	slog.Debug("got EOF")
	fe.eof = true
	if fe.done && (!fe.NoExit || fe.interrupted) {
		fe.quitting = true
		fe.doQuit()
	}
	fe.Update()
}

func (fe *frontendPretty) doQuit() {
	// Remove the keymap bar so it doesn't appear in the final frame.
	if fe.keymapBar != nil {
		fe.tui.RemoveChild(fe.keymapBar)
	}
	select {
	case <-fe.quit:
		// already closed
	default:
		close(fe.quit)
	}
}

// FinalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) FinalRender(w io.Writer) error {
	if exitCode, ok := renderQuietError(w, fe.err); ok {
		return ExitError{OriginalCode: exitCode, Original: fe.err}
	}

	// Hint for future rendering that this is the final, non-interactive render
	// (so don't show key hints etc.)
	fe.finalRender = true

	// Unfocus for the final render.
	fe.focus(nil)

	// Render the full trace.
	fe.ZoomedSpan = fe.db.PrimarySpan
	fe.viewDirty = false
	fe.recalculateViewLocked()

	out := NewOutput(w, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity >= dagui.ShowCompletedVerbosity || fe.err != nil {
		for _, line := range fe.tui.RenderLines() {
			fmt.Fprintln(w, line)
		}

		if fe.msgPreFinalRender.Len() > 0 {
			defer func() {
				fmt.Fprintln(w)
				var telemetryErr error
				if p := fe.telemetryError.Load(); p != nil {
					telemetryErr = *p
				}
				handleTelemetryErrorOutput(w, out, telemetryErr)
				fmt.Fprintln(os.Stderr, fe.msgPreFinalRender.String())
			}()
		}
	}

	if fe.err != nil && fe.shell == nil {
		if fe.hasShownRootError() {
			// If we've already shown the root cause error for the command, we can
			// skip displaying the primary output and error, since it's just a poorer
			// representation of the same error (`Error: input: ...`)
			var exitErr ExitError
			if errors.As(fe.err, &exitErr) {
				return exitErr
			}
			return ExitError{OriginalCode: 1, Original: fe.err}
		}
	}

	// Replay the primary output log to stdout/stderr.
	return renderPrimaryOutput(w, fe.db)
}

func (fe *frontendPretty) SpanExporter() sdktrace.SpanExporter {
	return prettySpanExporter{fe}
}

type prettySpanExporter struct {
	*frontendPretty
}

func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	// Copy the slice — the OTel SDK reuses it after ExportSpans returns,
	// and Dispatch runs asynchronously on the UI goroutine.
	spansCopy := make([]sdktrace.ReadOnlySpan, len(spans))
	copy(spansCopy, spans)
	spanIDs := make([]dagui.SpanID, len(spans))
	for i, s := range spans {
		spanIDs[i] = dagui.SpanID{SpanID: s.SpanContext().SpanID()}
	}
	fe.dispatch(func() {
		fe.db.ExportSpans(context.Background(), spansCopy)
		for _, id := range spanIDs {
			if fe.logs.flushResolvedLogsForSpan(id) {
				fe.updateSpanTreesForLogs(id)
			}
			if sr, ok := fe.spanTrees[id]; ok {
				sr.Update()
			}
		}
		// Don't recalculate here — set dirty flag so Render coalesces
		// multiple ExportSpans batches into one recalculate per frame.
		fe.viewDirty = true
		fe.Update()
	})
	return nil
}

func (fe *frontendPretty) updateSpanTreesForLogs(spanID dagui.SpanID) {
	if !spanID.IsValid() {
		return
	}
	if sr, ok := fe.spanTrees[spanID]; ok {
		sr.Update()
	}
	if _, rolledUp := fe.logs.findRollUpSpan(spanID); rolledUp {
		for id := spanID; ; {
			span := fe.db.Spans.Map[id]
			if span == nil || span.Boundary || span.Encapsulate || span.Internal {
				break
			}
			if span.RollUpLogs {
				if sr, ok := fe.spanTrees[id]; ok {
					sr.Update()
				}
				break
			}
			if !span.ParentID.IsValid() {
				break
			}
			id = span.ParentID
		}
	}
}

func (fe *frontendPretty) Shutdown(ctx context.Context) error {
	if err := fe.db.Shutdown(ctx); err != nil {
		return err
	}
	return fe.Close()
}

func (fe *frontendPretty) LogExporter() sdklog.Exporter {
	return prettyLogExporter{fe}
}

type prettyLogExporter struct {
	*frontendPretty
}

func (fe prettyLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	// Copy the slice — the OTel SDK reuses it after Export returns.
	logsCopy := make([]sdklog.Record, len(logs))
	copy(logsCopy, logs)
	fe.dispatch(func() {
		for _, log := range logsCopy {
			spanID := fe.db.LogTargetSpanID(log)
			fe.updateSpanTreesForLogs(spanID)
		}
		fe.db.LogExporter().Export(context.Background(), logsCopy)
		fe.logs.Export(context.Background(), logsCopy)
		fe.Update()
	})
	return nil
}

func (fe *frontendPretty) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPretty) Close() error {
	if fe.tui != nil {
		fe.dispatch(func() {
			fe.handleEOF()
		})
	}
	return nil
}

func (fe *frontendPretty) MetricExporter() sdkmetric.Exporter {
	return FrontendMetricExporter{fe}
}

type FrontendMetricExporter struct {
	*frontendPretty
}

func (fe FrontendMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	// Copy the data — the OTel SDK reuses the ResourceMetrics after Export
	// returns (via a sync.Pool in PeriodicReader), and dispatch runs
	// asynchronously on the UI goroutine.
	metricsCopy := cloneResourceMetrics(resourceMetrics)
	fe.dispatch(func() {
		fe.db.MetricExporter().Export(ctx, metricsCopy)
		fe.Update()
	})
	return nil
}

// cloneResourceMetrics returns a shallow-enough copy of rm so that the
// caller can safely read it after the original is recycled by the SDK.
func cloneResourceMetrics(rm *metricdata.ResourceMetrics) *metricdata.ResourceMetrics {
	out := &metricdata.ResourceMetrics{
		Resource: rm.Resource,
	}
	if len(rm.ScopeMetrics) > 0 {
		out.ScopeMetrics = make([]metricdata.ScopeMetrics, len(rm.ScopeMetrics))
		for i, sm := range rm.ScopeMetrics {
			out.ScopeMetrics[i].Scope = sm.Scope
			if len(sm.Metrics) > 0 {
				out.ScopeMetrics[i].Metrics = make([]metricdata.Metrics, len(sm.Metrics))
				copy(out.ScopeMetrics[i].Metrics, sm.Metrics)
			}
		}
	}
	return out
}

func (fe FrontendMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return fe.db.Temporality(ik)
}

func (fe FrontendMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return fe.db.Aggregation(ik)
}

func (fe FrontendMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPretty) Background(cmd ExecCommand, raw bool) error {
	errs := make(chan error, 1)
	fe.backgroundReq <- backgroundRequest{
		cmd:  cmd,
		raw:  raw,
		done: errs,
	}
	return <-errs
}

func (fe *frontendPretty) keys(out *termenv.Output) []key.Binding {
	if fe.formModel != nil {
		return fe.formModel.KeyBinds()
	}

	if fe.editlineFocused {
		bnds := []key.Binding{
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		}
		if fe.shell != nil {
			bnds = append(bnds, fe.shell.KeyBindings(out)...)
		}
		return bnds
	}

	var quitMsg string
	if fe.interrupted {
		quitMsg = "quit!"
	} else if fe.shell != nil {
		quitMsg = "interrupt"
	} else {
		quitMsg = "quit"
	}

	noExitHelp := "no exit"
	if fe.NoExit {
		color := termenv.ANSIYellow
		if fe.done || fe.interrupted {
			color = termenv.ANSIRed
		}
		noExitHelp = out.String(noExitHelp).Foreground(color).String()
	}
	var focused *dagui.Span
	if fe.FocusedSpan.IsValid() {
		focused = fe.db.Spans.Map[fe.FocusedSpan]
	}
	return []key.Binding{
		key.NewBinding(key.WithKeys("i", "tab"),
			key.WithHelp("i", "input mode"),
			KeyEnabled(fe.shell != nil)),
		key.NewBinding(key.WithKeys("w"),
			key.WithHelp("w", out.Hyperlink(fe.cloudURL, "web")),
			KeyEnabled(fe.cloudURL != "")),
		key.NewBinding(key.WithKeys("←↑↓→", "up", "down", "left", "right", "h", "j", "k", "l"),
			key.WithHelp("←↑↓→", "move")),
		key.NewBinding(key.WithKeys("home"),
			key.WithHelp("home", "first")),
		key.NewBinding(key.WithKeys("end", "space"),
			key.WithHelp("end", "last")),
		key.NewBinding(key.WithKeys("+/-", "+", "-"),
			key.WithHelp("+/-", fmt.Sprintf("verbosity=%d", fe.Verbosity))),
		key.NewBinding(key.WithKeys("E"),
			key.WithHelp("E", noExitHelp)),
		key.NewBinding(key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", quitMsg)),
		key.NewBinding(key.WithKeys("esc"),
			key.WithHelp("esc", fe.escHelp()),
			KeyEnabled(fe.searchQuery != "" || (fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan))),
		key.NewBinding(key.WithKeys("r"),
			key.WithHelp("r", "go to error"),
			KeyEnabled(focused != nil && len(focused.ErrorOrigins.Order) > 0)),
		key.NewBinding(key.WithKeys("t"),
			key.WithHelp("t", "start terminal"),
			KeyEnabled(focused != nil && fe.terminalCallback(focused) != nil),
		),
		key.NewBinding(key.WithKeys("/"),
			key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("n"),
			key.WithHelp("n", fe.searchCountHint("next")),
			KeyEnabled(fe.searchQuery != "")),
		key.NewBinding(key.WithKeys("N"),
			key.WithHelp("N", "prev"),
			KeyEnabled(fe.searchQuery != "")),
	}
}

func (fe *frontendPretty) escHelp() string {
	if fe.searchQuery != "" {
		return "clear search"
	}
	return "unzoom"
}

func (fe *frontendPretty) searchCountHint(base string) string {
	if len(fe.searchMatches) == 0 {
		return base + " (0)"
	}
	return fmt.Sprintf("%s (%d/%d)", base, fe.searchIdx+1, len(fe.searchMatches))
}

func KeyEnabled(enabled bool) key.BindingOpt {
	return func(b *key.Binding) {
		b.SetEnabled(enabled)
	}
}

// ---------- tuist.Component -------------------------------------------------

// Render implements tuist.Component. It produces the full TUI output as lines.
func (fe *frontendPretty) Render(ctx tuist.Context) {
	if !fe.finalRender && (fe.backgrounded || fe.quitting) {
		return
	}

	// Coalesce deferred view updates. Multiple ExportSpans batches may
	// have set viewDirty since the last frame — recalculate once now.
	if fe.viewDirty {
		fe.viewDirty = false
		fe.recalculateViewLocked()
	}

	// Refresh search on every frame — picks up new log output via
	// midterm's incremental search (only re-scans changed rows).
	if fe.searchQuery != "" {
		fe.refreshSearchMatches()
	}

	if !fe.finalRender {
		// Update window dimensions from tuist.
		fe.window = windowSize{Width: ctx.Width, Height: ctx.ScreenHeight()}
		fe.setWindowSizeLocked(fe.window)
	} else if fe.contentWidth <= 0 {
		// Final render without a live TUI (report mode). Set to 0
		// so the renderer doesn't truncate (maxLiteralLen = 0).
		fe.contentWidth = 0
	}

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts, fe.finalRender)

	if fe.finalRender {
		// Final render: just emit progress rows, no chrome or truncation.
		progressLines := fe.renderProgressLines(r, ctx, 0)
		ctx.Lines(progressLines...)
		return
	}

	// Zoom header
	var progPrefix string
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		zoomBuf := new(strings.Builder)
		zoomOut := NewOutput(zoomBuf, termenv.WithProfile(fe.profile))
		fe.renderStep(ctx, zoomOut, r, &dagui.TraceRow{
			Span:     fe.rowsView.Zoomed,
			Expanded: true,
		}, "")
		linesFromView(ctx, zoomBuf.String())
		progPrefix = "  "
	}

	// Pre-render chrome below progress to measure its height for truncation.
	logsLines := fe.renderLogsLines(progPrefix)

	chromeHeight := 1 + 1 // keymap (1 line, sibling) + gap after progress
	if len(logsLines) > 0 {
		chromeHeight += len(logsLines) + 1
	}
	chromeHeight += fe.errorLabelHeight() // promptErrLabel is a sibling, not rendered here
	chromeHeight += fe.editlineHeight()   // textInput is a sibling, not rendered here
	chromeHeight += fe.formHeight()       // formWrap is a sibling, not rendered here
	if fe.searchInput != nil {
		chromeHeight += 1 // searchInput is a sibling, 1 line
	}

	// Render progress rows via tree-based components
	progressLines := fe.renderProgressLines(r, ctx, chromeHeight)
	if len(progressLines) > 0 {
		ctx.Lines(progressLines...)
		ctx.Line("") // gap line after progress
	}

	// Append chrome
	if len(logsLines) > 0 {
		ctx.Lines(logsLines...)
		ctx.Line("") // trailing gap
	}
	// NOTE: textInput and formWrap are rendered as siblings in the TUI
	// container, not here. Their cursors propagate through tuist automatically.
	// NOTE: keymapBar is rendered as a sibling in the TUI container.
}

// linesFromView splits a string view into lines and emits them via ctx.
func linesFromView(ctx tuist.Context, view string) {
	if view == "" {
		return
	}
	ctx.Lines(strings.Split(strings.TrimSuffix(view, "\n"), "\n")...)
}

// renderLogsLines returns the zoomed span's log output as lines.
func (fe *frontendPretty) renderLogsLines(prefix string) []string {
	logs := fe.logs.Logs[fe.ZoomedSpan]
	if logs == nil || logs.UsedHeight() == 0 || fe.hasShownRootError() {
		return nil
	}
	logs.SetHeight(fe.window.Height / 3)
	logs.SetPrefix(prefix)
	view := logs.View()
	if view == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(view, "\n"), "\n")
}

// errorLabelHeight returns the line count of the error label for chrome-height budgeting.
func (fe *frontendPretty) errorLabelHeight() int {
	if fe.promptErrLabel == nil || fe.promptErr == nil {
		return 0
	}
	return 1
}

// editlineHeight returns the estimated line count of the text input
// for chrome-height budgeting. The actual rendering is handled by tuist's
// container (textInput is a sibling, not rendered here).
func (fe *frontendPretty) editlineHeight() int {
	if fe.textInput == nil {
		return 0
	}
	// Count newlines in current value + 1 for the input line itself
	val := fe.textInput.Value()
	return strings.Count(val, "\n") + 1
}

// formHeight returns the estimated line count of the form wrap
// for chrome-height budgeting. The actual rendering is handled by tuist
// (formWrap is a sibling component).
func (fe *frontendPretty) formHeight() int {
	if fe.formModel == nil {
		return 0
	}
	view := fe.formModel.View()
	if view == "" {
		return 0
	}
	return strings.Count(view, "\n") + 2 // +1 for the view line, +1 for the spacer
}

func (fe *frontendPretty) recalculateViewLocked() {
	fe.viewDirty = false // clear in case called directly from event handlers
	fe.rowsView = fe.db.RowsView(fe.FrontendOpts)
	fe.rows = fe.rowsView.Rows(fe.FrontendOpts)

	if len(fe.rows.Order) == 0 {
		fe.focus(nil)
		fe.topTrees = nil
		return
	}

	if fe.formWrap != nil {
		// avoid stealing focus from a form if present
		return
	}

	if fe.focusedIndex() < 0 {
		// durability: focused span disappeared from view
		fe.autoFocus = true
	}
	if fe.autoFocus {
		fe.focus(fe.rows.Order[len(fe.rows.Order)-1])
	} else if row := fe.rows.BySpan[fe.FocusedSpan]; row != nil {
		fe.focus(row)
	} else {
		// lost focus somehow
		fe.autoFocus = true
		fe.recalculateViewLocked()
		return
	}

	// Sync the SpanTreeView component tree with the current rowsView.
	// This is where ALL component state mutations happen — prefix,
	// children, focus, spinners. Render() is then a pure read.
	fe.syncSpanTreeState()

	// Re-apply tuist focus after sync. The focus() call above may have
	// targeted a SpanTreeView that didn't exist yet (new span on first
	// appearance). Now that syncSpanTreeState has created all
	// SpanTreeViews, ensure the correct one has tuist keyboard focus.
	fe.applyTuistFocus()
}

// applyTuistFocus sets tuist keyboard focus to the SpanTreeView for the
// currently focused span (or to fe itself when no span is focused).
// Skipped when editline or search has focus.
func (fe *frontendPretty) applyTuistFocus() {
	if fe.editlineFocused || fe.searchActive {
		return
	}
	if fe.FocusedSpan.IsValid() {
		if sr, ok := fe.spanTrees[fe.FocusedSpan]; ok {
			fe.tui.SetFocus(sr)
			return
		}
	}
	fe.tui.SetFocus(fe)
}

// syncSpanTreeState synchronizes the SpanTreeView component tree with
// the current rowsView and rows. Called from recalculateViewLocked()
// (i.e., from event handlers and Dispatch callbacks, never from Render).
//
// It walks the TraceTree top-down, creating/reusing SpanTreeViews,
// computing prefixes, and calling Update() on components whose
// visible state changed.
func (fe *frontendPretty) syncSpanTreeState() {
	if fe.spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}

	// When global config changes (verbosity, zoom, etc.), mark all
	// existing trees dirty so they re-render with the new settings.
	if fe.renderVersion != fe.lastRenderedVersion {
		fe.lastRenderedVersion = fe.renderVersion
		for _, st := range fe.spanTrees {
			st.Update()
		}
	}

	// Determine the zoom prefix for top-level trees.
	var zoomPrefix string
	if fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		zoomPrefix = "  "
	}

	body := fe.rowsView.Body
	newTops := make([]*SpanTreeView, 0, len(body))
	for i, tree := range body {
		st := fe.getOrCreateSpanTree(tree.Span.ID)
		st.parent = nil
		st.indexInParent = i

		// Top-level prefix (zoom indentation if applicable)
		var newPrefix treePrefix
		if zoomPrefix != "" {
			newPrefix = treePrefix{
				step:        zoomPrefix,
				cont:        zoomPrefix,
				forChildren: zoomPrefix,
				contWidth:   lipgloss.Width(zoomPrefix),
			}
		}
		fe.syncTreeNode(st, newPrefix)
		newTops = append(newTops, st)
	}
	fe.topTrees = newTops
}

// syncTreeNode recursively syncs a SpanTreeView and its children with
// the current trace data. Updates prefix, focus, spinner, and children.
// Calls Update() on any SpanTreeView whose visible state changed.
func (fe *frontendPretty) syncTreeNode(st *SpanTreeView, newPrefix treePrefix) {
	changed := false

	// Sync prefix
	if st.prefix != newPrefix {
		st.prefix = newPrefix
		changed = true
	}

	// Sync spinner: mount when running, dismount when done
	fe.syncSpinnerTree(st)

	if changed {
		st.Update()
	}

	// Sync children for expanded nodes
	tree := fe.rowsView.BySpan[st.spanID]
	if tree == nil || !tree.IsExpanded(fe.FrontendOpts) {
		// Collapsed: clear children so they get dismounted on next render
		if len(st.children) > 0 {
			st.children = nil
			st.Update()
		}
		return
	}

	// Determine visible children
	var childTrees []*dagui.TraceTree
	if tree.ShouldShowRevealedSpans(fe.FrontendOpts) {
		for _, revealedSpan := range tree.Span.RevealedSpans.Order {
			if revealedTree, ok := fe.rowsView.BySpan[revealedSpan.ID]; ok {
				childTrees = append(childTrees, revealedTree)
			}
		}
	} else {
		childTrees = tree.Children
	}

	// Compute the gap prefix for lines between this node's children.
	// This is the ancestor bars + this node's own bar column (always
	// shown, since we're between children that both exist).
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	span := tree.Span
	color := restrainedStatusColor(span)
	if !span.Reveal && len(span.RevealedSpans.Order) == 0 {
		st.childrenGapPrefix = st.prefix.forChildren + out.String(VertBar+" ").Foreground(color).Faint().String()
	} else {
		st.childrenGapPrefix = st.prefix.forChildren + "  "
	}

	// Reconcile child SpanTreeViews
	if st.childMap == nil {
		st.childMap = make(map[dagui.SpanID]*SpanTreeView)
	}
	newChildren := make([]*SpanTreeView, 0, len(childTrees))
	seen := make(map[dagui.SpanID]bool, len(childTrees))
	for i, childTree := range childTrees {
		id := childTree.Span.ID
		seen[id] = true
		child, ok := st.childMap[id]
		if !ok {
			child = &SpanTreeView{
				fe:     fe,
				spanID: id,
			}
			st.childMap[id] = child
			fe.spanTrees[id] = child
		}
		child.parent = st
		child.indexInParent = i

		// Compute child prefix
		hasNext := i < len(childTrees)-1
		childPrefix := st.computeChildPrefix(out, hasNext)

		// Recurse
		fe.syncTreeNode(child, childPrefix)
		newChildren = append(newChildren, child)
	}
	for id := range st.childMap {
		if !seen[id] {
			delete(st.childMap, id)
		}
	}

	// Detect children changes (added, removed, or reordered).
	childrenChanged := len(newChildren) != len(st.children)
	if !childrenChanged {
		for i := range newChildren {
			if newChildren[i] != st.children[i] {
				childrenChanged = true
				break
			}
		}
	}
	st.children = newChildren
	if childrenChanged {
		st.Update()
	}
}

// renderProgressLines renders progress using the tree-based SpanTreeView
// components and returns the output as lines. Truncates below the focused
// item so it stays onscreen.
func (fe *frontendPretty) renderProgressLines(r *renderer, ctx tuist.Context, chromeHeight int) []string {
	if fe.rowsView == nil {
		return nil
	}

	// topTrees was synced by syncSpanTreeState() in recalculateViewLocked().

	// Render all top-level trees via RenderChild, assembling into allLines.
	// We render everything (for caching), then truncate below the focused
	// item so it stays onscreen. Content above scrolls into scrollback.
	var allLines []string
	topGapCounts := make([]int, len(fe.topTrees))
	for i, treeView := range fe.topTrees {
		childCtx := ctx
		childCtx.Width = fe.contentWidth
		result := fe.RenderChildResult(childCtx, treeView)

		// Gap between top-level trees
		if i > 0 && len(result.Lines) > 0 {
			row := fe.rows.BySpan[treeView.spanID]
			if row != nil {
				gaps := fe.renderTreeGap(r, row, treeView.prefix.cont)
				topGapCounts[i] = len(gaps)
				allLines = append(allLines, gaps...)
			}
		}

		allLines = append(allLines, result.Lines...)
	}

	if len(allLines) == 0 {
		return nil
	}

	// Find the focused line by walking up from the focused node.
	focusLine := -1
	if fe.FocusedSpan.IsValid() {
		focusLine = fe.findFocusLine(topGapCounts)
	}

	// Crop the bottom so the focused span stays within the visible
	// screen area. Content above scrolls into terminal scrollback
	// naturally — we never crop the top.
	viewportHeight := ctx.ScreenHeight() - chromeHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	end := len(allLines)
	if focusLine >= 0 && len(allLines) > viewportHeight {
		// Use the root span's own rendered height (selfLineCount), not
		// the entire tree height. Children may extend below the viewport,
		// but the root's own content must stay in view.
		focusHeight := 1
		if focused, ok := fe.spanTrees[fe.FocusedSpan]; ok {
			focusHeight = focused.selfLineCount
		}
		end = cropEnd(len(allLines), viewportHeight, focusLine, focusHeight)
	}

	return allLines[:end]
}

// cropEnd computes the end index for slicing rendered lines so that the
// focused span's own content [focusLine, focusLine+focusHeight) is always
// visible within [end-viewportHeight, end). Remaining viewport space is
// split evenly above and below the focused span's root content.
//
// Content above the visible window scrolls into terminal scrollback naturally.
func cropEnd(totalLines, viewportHeight, focusLine, focusHeight int) int {
	focusEnd := focusLine + focusHeight
	if focusEnd > totalLines {
		focusEnd = totalLines
	}

	// Split remaining viewport space evenly above and below the focus root.
	remaining := viewportHeight - focusHeight
	if remaining < 0 {
		remaining = 0
	}
	below := remaining / 2

	end := focusEnd + below

	// Ensure the focus root stays fully visible: the visible window is
	// [end-viewportHeight, end), so cap end so focusLine >= end-viewportHeight.
	if focusHeight <= viewportHeight && end > focusLine+viewportHeight {
		end = focusLine + viewportHeight
	}

	// When the focus root is taller than the viewport, at least show up
	// to its end.
	if end < focusEnd {
		end = focusEnd
	}

	// Never crop to less than a full viewport when there's enough content.
	if end < viewportHeight && viewportHeight < totalLines {
		end = viewportHeight
	}

	if end > totalLines {
		end = totalLines
	}

	return end
}

// totalLineCount returns the total number of rendered lines for a SpanTreeView,
// including self content, gap lines, and all children.
func (s *SpanTreeView) totalLineCount() int {
	n := s.selfLineCount
	if len(s.childGapCounts) != len(s.children) || len(s.childLineCounts) != len(s.children) {
		return n
	}
	for i := range s.children {
		n += s.childGapCounts[i] + s.childLineCounts[i]
	}
	return n
}

// findFocusLine returns the line offset of the focused span within the
// rendered output, or -1 if not found. Instead of searching top-down
// through the entire tree (O(nodes)), it walks up from the focused
// SpanTreeView to the root, accumulating offsets (O(depth × siblings)).
func (fe *frontendPretty) findFocusLine(topGapCounts []int) int {
	focused, ok := fe.spanTrees[fe.FocusedSpan]
	if !ok {
		return -1
	}

	// Walk up from focused node to root, collecting the path.
	// We need the path so we can compute offsets top-down.
	var path []*SpanTreeView
	for cur := focused; cur != nil; cur = cur.parent {
		path = append(path, cur)
	}

	// The last element is a top-level node. Compute its base offset.
	root := path[len(path)-1]
	offset := 0
	for i, tree := range fe.topTrees {
		if tree == root {
			offset += topGapCounts[i]
			break
		}
		offset += topGapCounts[i] + tree.totalLineCount()
	}

	// Walk down the path (reverse order), adding offsets for preceding
	// siblings at each level.
	for j := len(path) - 1; j >= 0; j-- {
		node := path[j]
		if j < len(path)-1 {
			// Add self lines of the parent (the node above us in the path).
			parent := path[j+1]
			offset += parent.selfLineCount

			// Add lines from siblings before this node.
			idx := node.indexInParent
			if len(parent.childGapCounts) != len(parent.children) ||
				len(parent.childLineCounts) != len(parent.children) {
				return -1
			}
			for s := 0; s < idx; s++ {
				offset += parent.childGapCounts[s] + parent.childLineCounts[s]
			}
			// Add the gap before this node itself.
			offset += parent.childGapCounts[idx]
		}
	}

	return offset
}

// renderTreeGap renders the gap line(s) that precede a row in tree rendering,
// using the tree prefix instead of calling fancyIndent.
func (fe *frontendPretty) renderTreeGap(_ *renderer, row *dagui.TraceRow, gapPrefix string) []string {
	trimmedPrefix := strings.TrimRight(gapPrefix, " ")
	if fe.shell != nil {
		if row.Depth == 0 && row.Previous != nil {
			return []string{""}
		}
		// Gap above each LLM response to visually group RTTT sequences.
		if row.Previous != nil && row.Span.LLMRole == telemetry.LLMRoleAssistant {
			return []string{trimmedPrefix}
		}
		return nil
	}
	if row.PreviousVisual != nil &&
		row.PreviousVisual.Depth >= row.Depth &&
		!row.Chained &&
		(row.PreviousVisual.Depth > row.Depth ||
			row.Span.Call() != nil ||
			row.Span.CheckName != "" ||
			row.Span.GeneratorName != "" ||
			(row.PreviousVisual.Span.Call() != nil && row.Span.Call() == nil) ||
			(row.PreviousVisual.Span.Message != "" && row.Span.Message != "") ||
			(row.PreviousVisual.Span.Message == "" && row.Span.Message != "")) {
		return []string{trimmedPrefix}
	}
	return nil
}

// focusedIndex returns the current index of the focused span in rows.Order,
// or -1 if nothing is focused or the span is not in the current row list.
func (fe *frontendPretty) focusedIndex() int {
	if !fe.FocusedSpan.IsValid() || fe.rows == nil {
		return -1
	}
	if row := fe.rows.BySpan[fe.FocusedSpan]; row != nil {
		return row.Index
	}
	return -1
}

func (fe *frontendPretty) focus(row *dagui.TraceRow) {
	oldSpan := fe.FocusedSpan
	var newSpan dagui.SpanID
	if row == nil {
		fe.FocusedSpan = dagui.SpanID{}
		if !fe.editlineFocused && !fe.searchActive {
			fe.tui.SetFocus(fe)
		}
	} else {
		newSpan = row.Span.ID
		fe.FocusedSpan = newSpan
		if !fe.editlineFocused && !fe.searchActive {
			if sr, ok := fe.spanTrees[newSpan]; ok {
				fe.tui.SetFocus(sr)
			}
		}
	}
	// Invalidate the render caches of old and new SpanTreeViews when
	// focus moves. Their Render methods read fe.FocusedSpan to decide
	// highlighting, so stale caches show the wrong focus indicator.
	if oldSpan != newSpan {
		if st, ok := fe.spanTrees[oldSpan]; ok {
			st.Update()
		}
		if st, ok := fe.spanTrees[newSpan]; ok {
			st.Update()
		}
	}
}

// manualFocus is like focus but also deselects the current search match
// so that n/N seek relative to the new position.
func (fe *frontendPretty) manualFocus(row *dagui.TraceRow) {
	fe.focus(row)
	if fe.searchQuery != "" {
		fe.searchIdx = -1
	}
}

// ---------- tuist.Interactive -----------------------------------------------

// HandleKeyPress implements tuist.Interactive. It dispatches key events to the
// nav handler. When the TextInput or formWrap is focused, keys go directly to
// them via tuist's focus routing.
func (fe *frontendPretty) HandleKeyPress(_ tuist.Context, ev uv.KeyPressEvent) bool {
	fe.handleNavKeyUV(ev)

	// Schedule a re-render after the keypress highlight fades
	fe.scheduleKeypressClear()

	fe.Update()
	return true
}

// interceptEditlineKey is the TextInput's KeyInterceptor. It handles
// special keys before TextInput processes them. Returns true if consumed.
func (fe *frontendPretty) interceptEditlineKey(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	k := uv.Key(ev)
	keyStr := k.String()
	fe.recordKeyPress(keyStr)

	// Let the completion menu handle keys when visible (up/down/esc/tab).
	if fe.completionMenu != nil && fe.completionMenu.HandleKeyPress(ctx, ev) {
		return true
	}

	switch keyStr {
	case "ctrl+d":
		if fe.textInput.Value() == "" {
			fe.quitAction(ErrShellExited)
			return true
		}
		return false // let TextInput handle ctrl+d (delete char) when input non-empty
	case "ctrl+c":
		if fe.shellInterrupt != nil {
			fe.shellInterrupt(errors.New("interrupted"))
		}
		fe.textInput.SetValue("")
		fe.syncPrompt()
		return true
	case "ctrl+l":
		fe.tui.RequestRender(true)
		fe.syncPrompt()
		return true
	case "esc":
		fe.enterNavMode(false)
		fe.syncPrompt()
		return true
	case "alt++", "alt+=":
		fe.Verbosity++
		fe.renderVersion++
		fe.recalculateViewLocked()
		fe.syncPrompt()
		return true
	case "alt+-":
		fe.Verbosity--
		fe.renderVersion++
		fe.recalculateViewLocked()
		fe.syncPrompt()
		return true
	case "up":
		if fe.historyUp() {
			return true
		}
	case "down":
		if fe.historyDown() {
			return true
		}
	default:
		if fe.shell != nil {
			if work := fe.shell.ReactToInput(fe.shellCtx, ev, fe.textInput.Value(), true); work != nil {
				fe.runShellAsync(work)
				return true
			}
		}
	}

	return false // let TextInput handle it
}

// handleNavKeyUV handles key events in navigation mode.
//
//nolint:gocyclo // splitting this up doesn't feel more readable
func (fe *frontendPretty) handleNavKeyUV(ev uv.KeyPressEvent) {
	k := uv.Key(ev)
	keyStr := k.String()
	lastKey := fe.pressedKey
	fe.recordKeyPress(keyStr)

	switch keyStr {
	case "q", "ctrl+c":
		if fe.shell != nil {
			if fe.shellInterrupt != nil {
				fe.shellInterrupt(errors.New("interrupted"))
			}
		} else {
			fe.quitAction(ErrInterrupted)
		}
	case "ctrl+\\": // SIGQUIT
		// Note: can't release terminal mid-render in tuist the way bubbletea can.
		// Just send the signal.
		sigquit()
		return
	case "E":
		fe.NoExit = !fe.NoExit
		return
	case "down", "j":
		fe.goDown()
		return
	case "up", "k":
		fe.goUp()
		return
	case "left", "h":
		fe.closeOrGoOut()
		return
	case "right", "l":
		fe.openOrGoIn()
		return
	case "home":
		fe.goStart()
		return
	case "end", "G", "space":
		fe.goEnd()
		fe.recordKeyPress("end")
		return
	case "r":
		fe.goErrorOrigin()
		return
	case "esc":
		if fe.searchQuery != "" {
			fe.clearSearch()
			fe.renderVersion++
			fe.recalculateViewLocked()
			return
		}
		fe.ZoomedSpan = fe.db.PrimarySpan
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "+", "=":
		fe.FrontendOpts.Verbosity++
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "-":
		fe.FrontendOpts.Verbosity--
		if fe.FrontendOpts.Verbosity < -1 {
			fe.FrontendOpts.Verbosity = -1
		}
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "w":
		if fe.cloudURL == "" {
			return
		}
		url := fe.cloudURL
		if fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan {
			url += "?span=" + fe.ZoomedSpan.String()
		}
		if fe.FocusedSpan.IsValid() && fe.FocusedSpan != fe.db.PrimarySpan {
			url += "#" + fe.FocusedSpan.String()
		}
		go func() {
			if err := browser.OpenURL(url); err != nil {
				slog.Warn("failed to open URL",
					"url", url,
					"err", err,
					"output", fe.browserBuf.String())
			}
		}()
		return
	case "?":
		if st, ok := fe.spanTrees[fe.FocusedSpan]; ok {
			st.debugged = !st.debugged
			st.Update()
		}
		return
	case "enter":
		fe.ZoomedSpan = fe.FocusedSpan
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "tab", "i":
		fe.enterInsertMode(false)
		return
	case "t":
		fe.terminal()
		return
	case "/":
		fe.enterSearchMode()
		return
	case "n":
		if fe.searchQuery != "" {
			fe.searchNext()
			return
		}
	case "N":
		if fe.searchQuery != "" {
			fe.searchPrev()
			return
		}
	default:
		if fe.shell != nil {
			inputVal := ""
			if fe.textInput != nil {
				inputVal = fe.textInput.Value()
			}
			if work := fe.shell.ReactToInput(fe.shellCtx, ev, inputVal, false); work != nil {
				fe.runShellAsync(work)
				return
			}
		}
	}

	switch lastKey { //nolint:gocritic
	case "g":
		switch keyStr { //nolint:gocritic
		case "g":
			fe.goStart()
			fe.recordKeyPress("home")
			return
		}
	}
}

// ---------- editline input completion ---------------------------------------

// handleInputComplete is called when the editline signals that input is
// complete (user pressed Enter on a complete line).
func (fe *frontendPretty) handleInputComplete() {
	if !fe.editlineFocused {
		return
	}

	// reset prompt error state
	fe.promptErr = nil
	if fe.promptErrLabel != nil {
		fe.promptErrLabel.SetError(nil)
	}

	value := fe.textInput.Value()
	// Add to history (encoded with mode prefix for round-trip fidelity)
	if value != "" {
		encoded := value
		if fe.shell != nil {
			encoded = fe.shell.EncodeHistory(value)
		}
		fe.inputHistory = append(fe.inputHistory, encoded)
	}
	fe.historyIndex = -1
	fe.promptFg = termenv.ANSIYellow
	fe.syncPrompt()

	// reset now that we've accepted input
	fe.textInput.SetValue("")
	if fe.shell != nil {
		ctx, cancel := context.WithCancelCause(fe.shellCtx)
		fe.shellInterrupt = cancel
		fe.shellRunning = true

		// switch back to following the bottom and re-enter nav mode
		fe.goEnd()
		fe.enterNavMode(true)

		go func() {
			fe.shellLock.Lock()
			defer fe.shellLock.Unlock()
			err := fe.shell.Handle(ctx, value)
			fe.dispatch(func() {
				fe.handleShellDone(err)
				fe.Update()
			})
		}()
	}
}

func (fe *frontendPretty) handleShellDone(err error) {
	fe.promptErr = err
	if fe.promptErrLabel != nil {
		fe.promptErrLabel.SetError(err)
	}
	if err == nil {
		fe.promptFg = termenv.ANSIGreen
	} else {
		fe.promptFg = termenv.ANSIRed
	}
	if fe.autoModeSwitch {
		fe.enterInsertMode(true)
	}
	fe.syncPrompt()
	fe.shellRunning = false
}

// ---------- mode switching --------------------------------------------------

func (fe *frontendPretty) enterNavMode(auto bool) {
	fe.autoModeSwitch = auto
	fe.editlineFocused = false
	fe.recalculateViewLocked() // also applies tuist focus via applyTuistFocus
	fe.keymapBar.Update()
}

func (fe *frontendPretty) enterSearchMode() {
	if fe.searchActive {
		return
	}
	fe.searchActive = true
	fe.searchInput = tuist.NewTextInput("")
	fe.searchInput.Prompt = "/"
	fe.searchInput.OnSubmit = func(ctx tuist.Context, value string) bool {
		fe.confirmSearch(value)
		return true
	}
	fe.searchInput.KeyInterceptor = fe.interceptSearchKey

	// Insert before keymapBar.
	fe.tui.RemoveChild(fe.keymapBar)
	fe.tui.AddChild(fe.searchInput)
	fe.tui.AddChild(fe.keymapBar)
	fe.tui.SetFocus(fe.searchInput)
	fe.tui.SetShowHardwareCursor(true)
	fe.keymapBar.Update()
}

func (fe *frontendPretty) exitSearchMode() {
	if fe.searchInput != nil {
		fe.tui.RemoveChild(fe.searchInput)
		fe.searchInput = nil
	}
	fe.searchActive = false
	fe.tui.SetShowHardwareCursor(fe.textInput != nil && fe.editlineFocused)
	fe.applyTuistFocus() // restore focus to the correct SpanTreeView
	fe.keymapBar.Update()
}

func (fe *frontendPretty) confirmSearch(query string) {
	fe.exitSearchMode()
	query = strings.TrimSpace(query)
	if query == "" {
		fe.clearSearch()
		return
	}
	fe.searchQuery = query
	fe.searchIdx = -1
	// Push query to all vterms (triggers midterm Search), read results,
	// navigate to first match, then update highlights + dirty trees.
	fe.syncVtermSearchHighlights()
	fe.buildSearchMatches()
	fe.searchFirstForward()
	fe.dirtySearchTrees()
	fe.Update()
}

func (fe *frontendPretty) interceptSearchKey(_ tuist.Context, ev uv.KeyPressEvent) bool {
	k := uv.Key(ev)
	keyStr := k.String()
	if keyStr == "esc" {
		fe.exitSearchMode()
		fe.Update()
		return true
	}
	return false
}

func (fe *frontendPretty) enterInsertMode(auto bool) {
	fe.autoModeSwitch = auto
	if fe.textInput != nil {
		fe.editlineFocused = true
		fe.syncPrompt()
		fe.tui.SetFocus(fe.textInput)
		fe.recalculateViewLocked()
		fe.keymapBar.Update()
	}
}

func (fe *frontendPretty) terminal() {
	if !fe.FocusedSpan.IsValid() {
		return
	}
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}

	callback := fe.terminalCallback(focused)
	if callback != nil {
		go func() {
			err := callback()
			if err != nil {
				slog.Error("failed to open terminal for span", err)
			}
		}()
	}
}

func (fe *frontendPretty) terminalCallback(span *dagui.Span) func() error {
	if fe.dag == nil {
		// we haven't got a dag client, so can't open a terminal
		return nil
	}

	// NOTE: this func is in the hot-path, so just use the call info to
	// determine if we can create a callback - the actual callback can do the
	// expensive id reconstruction
	call := span.Call()
	if call == nil {
		return nil
	}

	switch call.Type.NamedType {
	case "Container":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadContainerFromID(dagger.ContainerID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Directory":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadDirectoryFromID(dagger.DirectoryID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Service":
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadServiceFromID(dagger.ServiceID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	}

	return nil
}

func loadIDFromSpan(span *dagui.Span) (string, error) {
	callID, err := span.CallID()
	if err != nil {
		return "", err
	}
	id, err := callID.Encode()
	if err != nil {
		return "", err
	}
	return id, nil
}

// saveHistory persists the in-memory history to disk.
func (fe *frontendPretty) saveHistory() {
	if len(fe.inputHistory) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
		slog.Error("failed to create history directory", "err", err)
		return
	}
	if err := history.SaveHistory(fe.inputHistory, historyFile); err != nil {
		slog.Error("failed to save history", "err", err)
	}
}

// historyUp navigates to the previous history entry. Returns true if handled.
func (fe *frontendPretty) historyUp() bool {
	if len(fe.inputHistory) == 0 {
		return false
	}
	if fe.historyIndex == -1 {
		// Start browsing: save current input and mode
		fe.historySaved = fe.textInput.Value()
		if fe.shell != nil {
			fe.shell.SaveBeforeHistory()
		}
		fe.historyIndex = len(fe.inputHistory) - 1
	} else if fe.historyIndex > 0 {
		fe.historyIndex--
	} else {
		return true // at oldest entry
	}
	fe.setHistoryEntry(fe.historyIndex)
	return true
}

// historyDown navigates to the next history entry. Returns true if handled.
func (fe *frontendPretty) historyDown() bool {
	if fe.historyIndex == -1 {
		return false // not browsing history
	}
	if fe.historyIndex < len(fe.inputHistory)-1 {
		fe.historyIndex++
		fe.setHistoryEntry(fe.historyIndex)
	} else {
		// Restore saved input and mode
		fe.historyIndex = -1
		fe.textInput.SetValue(fe.historySaved)
		if fe.shell != nil {
			fe.shell.RestoreAfterHistory()
		}
		fe.syncPrompt()
	}
	return true
}

// setHistoryEntry decodes the history entry at idx and sets it as the
// TextInput value. If the shell handler is available, DecodeHistory is
// used to strip mode prefixes.
func (fe *frontendPretty) setHistoryEntry(idx int) {
	entry := fe.inputHistory[idx]
	if fe.shell != nil {
		entry = fe.shell.DecodeHistory(entry)
	}
	fe.textInput.SetValue(entry)
	fe.syncPrompt()
}

func (fe *frontendPretty) initTextInput() {
	fe.textInput = tuist.NewTextInput("")
	fe.textInput.OnSubmit = func(ctx tuist.Context, value string) bool {
		// Check if the shell considers this a complete command.
		// If not, insert a newline for multiline editing.
		if fe.shell != nil && !fe.shell.IsComplete(value) {
			// Insert a newline at cursor for multiline editing.
			fe.textInput.InsertRune('\n')
			return false // don't clear
		}
		fe.handleInputComplete()
		return true // clear input
	}
	fe.editlineFocused = true
}

// syncPrompt refreshes the text input prompt from the shell handler.
// If the handler returns an async init function (e.g. for LLM setup),
// it is run in a background goroutine that refreshes the prompt on
// completion.
func (fe *frontendPretty) syncPrompt() {
	if fe.shell != nil && fe.textInput != nil {
		promptOut := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
		prompt, init := fe.shell.Prompt(fe.runCtx, promptOut, fe.promptFg)
		fe.textInput.Prompt = prompt
		fe.textInput.Update()
		if init != nil {
			fe.runShellAsync(init)
		}
	}
}

// runShellAsync runs a shell handler function in a background goroutine,
// then dispatches a prompt refresh + re-render back to the UI thread.
func (fe *frontendPretty) runShellAsync(work func()) {
	go func() {
		work()
		fe.dispatch(func() {
			fe.syncPrompt()
			fe.Update()
		})
	}()
}

func (fe *frontendPretty) quitAction(interruptErr error) {
	if fe.cleanup != nil {
		cleanup := fe.cleanup
		fe.cleanup = nil // prevent double cleanup
		go func() {
			cleanup()
			fe.dispatch(func() {
				fe.quitting = true
				fe.doQuit()
			})
		}()
	} else if fe.interrupted {
		slog.Warn("exiting immediately")
		fe.quitting = true
		fe.doQuit()
	} else {
		slog.Warn("canceling... (press again to exit immediately)")
		fe.interrupted = true
		fe.interrupt(interruptErr)
	}
}

func (fe *frontendPretty) goStart() {
	fe.autoFocus = false
	if len(fe.rows.Order) > 0 {
		fe.manualFocus(fe.rows.Order[0])
	}
}

func (fe *frontendPretty) goEnd() {
	fe.autoFocus = true
	if len(fe.rows.Order) > 0 {
		fe.manualFocus(fe.rows.Order[len(fe.rows.Order)-1])
	}
}

func (fe *frontendPretty) goUp() {
	fe.autoFocus = false
	newIdx := fe.focusedIndex() - 1
	if newIdx < 0 || newIdx >= len(fe.rows.Order) {
		return
	}
	fe.manualFocus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goDown() {
	fe.autoFocus = false
	newIdx := fe.focusedIndex() + 1
	if newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	fe.manualFocus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goOut() {
	fe.autoFocus = false
	focused := fe.rows.BySpan[fe.FocusedSpan]
	if focused == nil {
		return
	}
	fe.manualFocus(focused.Parent)
}

func (fe *frontendPretty) goIn() {
	fe.autoFocus = false
	curIdx := fe.focusedIndex()
	newIdx := curIdx + 1
	if curIdx < 0 || newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	cur := fe.rows.Order[curIdx]
	next := fe.rows.Order[newIdx]
	if next.Depth <= cur.Depth {
		// has no children
		return
	}
	fe.manualFocus(next)
}

func (fe *frontendPretty) closeOrGoOut() {
	if !fe.FocusedSpan.IsValid() {
		return
	}
	tree := fe.rowsView.BySpan[fe.FocusedSpan]
	if tree == nil || !tree.IsExpanded(fe.FrontendOpts) {
		// already closed; move up
		fe.goOut()
		return
	}
	fe.setExpanded(fe.FocusedSpan, false)
	fe.syncAfterExpandToggle(fe.FocusedSpan)
}

func (fe *frontendPretty) openOrGoIn() {
	if !fe.FocusedSpan.IsValid() {
		return
	}
	tree := fe.rowsView.BySpan[fe.FocusedSpan]
	if tree != nil && tree.IsExpanded(fe.FrontendOpts) {
		// already expanded; go in
		fe.goIn()
		return
	}
	fe.setExpanded(fe.FocusedSpan, true)
	fe.syncAfterExpandToggle(fe.FocusedSpan)
}

func (fe *frontendPretty) goErrorOrigin() {
	fe.autoFocus = false
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}
	if len(focused.ErrorOrigins.Order) == 0 {
		return
	}
	var earliest *dagui.Span
	for _, span := range focused.ErrorOrigins.Order {
		if earliest == nil || span.StartTime.Before(earliest.StartTime) {
			earliest = span
		}
	}
	focusedRow := fe.rows.BySpan[earliest.ID]
	if focusedRow == nil {
		return
	}
	fe.manualFocus(focusedRow)
	for cur := focusedRow.Parent; cur != nil; cur = cur.Parent {
		// expand parents of target span
		fe.setExpanded(cur.Span.ID, true)
	}
	fe.recalculateViewLocked()
}

func (fe *frontendPretty) setWindowSizeLocked(msg windowSize) {
	fe.window = msg
	fe.contentWidth = msg.Width
	fe.logs.SetWidth(fe.contentWidth)
	if fe.textInput != nil {
		fe.textInput.Update()
	}
}

func (fe *frontendPretty) setExpanded(id dagui.SpanID, expanded bool) {
	if fe.SpanExpanded == nil {
		fe.SpanExpanded = make(map[dagui.SpanID]bool)
	}
	fe.SpanExpanded[id] = expanded
}

// syncAfterExpandToggle rebuilds the flat row list from the existing
// rowsView (cheap — no RowsView rebuild) and syncs the affected subtree.
// Use this after setExpanded for local expand/collapse operations.
func (fe *frontendPretty) syncAfterExpandToggle(id dagui.SpanID) {
	// Rebuild flat rows from existing tree. This is O(visible nodes)
	// and skips the expensive RowsView rebuild (WalkSpans + ShouldShow).
	fe.rows = fe.rowsView.Rows(fe.FrontendOpts)
	// Sync just the affected subtree's SpanTreeView children.
	if st, ok := fe.spanTrees[id]; ok {
		fe.syncTreeNode(st, st.prefix)
		// Always mark the toggled span dirty — even if syncTreeNode
		// found no structural changes (e.g. no children), the span's
		// own rendering may change (logs are shown/hidden based on
		// row.Expanded).
		st.Update()
	}
}

// renderRowContentRest renders everything after the step title: logs, errors,
// and debug output. Split out so SpanTreeView.Render can apply search
// highlighting to the title separately from the log content (which handles
// its own highlighting via Vterm.SearchQuery).
func (fe *frontendPretty) renderRowContentRest(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused

	if span.Message == "" && // messages are displayed in renderStep
		(row.Expanded || row.Span.LLMTool != "") {
		fe.renderStepLogs(out, r, row, prefix, isFocused)
	} else if (row.Span.RollUpLogs || fe.shell != nil) && row.Depth == 0 && !row.Expanded {
		// in shell mode, we print top-level command logs unindented, like shells
		// usually does
		if logs := fe.logs.Logs[row.Span.ID]; logs != nil && logs.UsedHeight() > 0 {
			if fe.shell != nil {
				unindent := *row
				unindent.Depth = -1
				fe.renderLogs(out, r, &unindent, logs, logs.UsedHeight(), prefix, false)
			} else if row.Span.RollUpLogs && row.IsRunningOrChildRunning {
				// Only show rolled-up logs while the span is running.
				fe.renderStepLogs(out, r, row, prefix, isFocused)
			}
		}
	}
	if len(row.Span.ErrorOrigins.Order) > 0 && (!row.Expanded || !row.HasChildren) {
		multi := len(row.Span.ErrorOrigins.Order) > 1
		for _, cause := range row.Span.ErrorOrigins.Order {
			if multi {
				var gapBuf strings.Builder
				gapOut := NewOutput(&gapBuf, termenv.WithProfile(fe.profile))
				r.fancyIndent(gapOut, row, false, false)
				fmt.Fprint(&gapBuf, prefix)
				fmt.Fprintln(out, strings.TrimRight(gapBuf.String(), " "))
			}
			fe.renderErrorCause(ctx, out, r, row, prefix, cause)
		}
	} else {
		fe.renderStepError(out, r, row, prefix)
	}
	fe.renderDebug(out, row.Span, prefix+Block25+" ", false)
}

func (fe *frontendPretty) renderDebug(out TermOutput, span *dagui.Span, prefix string, force bool) {
	if !force {
		st, ok := fe.spanTrees[span.ID]
		if !ok || !st.debugged {
			return
		}
	}
	vt := NewVterm(fe.profile)
	vt.WriteMarkdown([]byte("## Span\n"))
	vt.SetPrefix(prefix)
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(span.Snapshot())
	vt.WriteMarkdown([]byte("```json\n" + strings.TrimSpace(buf.String()) + "\n```"))
	var continuations []*dagui.Span
	for continuation := range span.EffectSpans {
		continuations = append(continuations, continuation)
	}
	if len(continuations) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Causal continuations\n\n"))
		for _, effect := range continuations {
			vt.WriteMarkdown([]byte("- " + effect.Name + "\n"))
		}
	}
	if len(span.RevealedSpans.Order) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Revealed spans\n\n"))
		for _, revealed := range span.RevealedSpans.Order {
			vt.WriteMarkdown([]byte("- " + revealed.Name + "\n"))
		}
	}
	if len(span.ErrorOrigins.Order) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Error origins\n\n"))
		for _, span := range span.ErrorOrigins.Order {
			vt.WriteMarkdown([]byte("- " + span.Name + "\n"))
		}
	}
	fmt.Fprint(out, prefix+vt.View())
}

// sync this with core.llmLogsLastLines to ensure user and LLM sees the same
// thing
const llmLogsLastLines = 8

func (fe *frontendPretty) renderStepLogs(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, focused bool) bool {
	limit := fe.window.Height / 3
	if row.Span.LLMTool != "" && !row.Expanded {
		limit = llmLogsLastLines
	}
	if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
		return fe.renderLogs(out, r, row, logs, limit, prefix, focused)
	}
	return false
}

func (fe *frontendPretty) renderErrorCause(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, rootCause *dagui.Span) {
	rootCauseTree := fe.rowsView.BySpan[rootCause.ID]
	if rootCauseTree == nil {
		// error origin has no tree, likely due to internal/hidden spans
		// create a synthetic tree by walking span parents
		var syntheticParents []*dagui.Span
		for current := rootCause; current != nil && current.ParentID.IsValid(); {
			parent := fe.db.Spans.Map[current.ParentID]
			if parent == nil {
				break
			}
			syntheticParents = append(syntheticParents, parent)
			current = parent
			// Stop if we reach the current row's span or a boundary
			if parent.ID == row.Span.ID {
				break
			}
		}

		// Create synthetic tree structure
		rootCauseTree = &dagui.TraceTree{
			Span: rootCause,
		}

		// Build parent chain
		current := rootCauseTree
		for i := len(syntheticParents) - 1; i >= 0; i-- {
			parent := &dagui.TraceTree{
				Span: syntheticParents[i],
			}
			current.Parent = parent
			current = parent
		}
	}

	rootCauseRow := &dagui.TraceRow{
		Span:     rootCause,
		Chained:  false,
		Expanded: true,
		Depth:    row.Depth,
	}

	var parents []*dagui.TraceRow
	for p := rootCauseTree.Parent; p != nil; p = p.Parent {
		if p.Span.ID == row.Span.ID {
			break
		}
		parentRow := &dagui.TraceRow{
			Span:     p.Span,
			Chained:  p.Chained,
			Depth:    row.Depth,
			Expanded: true,
		}
		parents = append(parents, parentRow)
	}

	indent := strings.Repeat("  ", row.Depth)
	if !fe.finalRender {
		indent += "  "
	}

	indentBuf := new(strings.Builder)
	fmt.Fprint(indentBuf, prefix)
	indentOut := NewOutput(indentBuf, termenv.WithProfile(fe.profile))
	r.fancyIndent(indentOut, row, false, false)
	if !fe.finalRender {
		fmt.Fprint(indentOut, "  ")
	}

	if len(parents) > 0 {
		r.fancyIndent(out, row, false, false)
		if !fe.finalRender {
			fmt.Fprint(out, "  ")
		}
		slices.Reverse(parents)
		context := new(strings.Builder)
		noColorOut := termenv.NewOutput(context, termenv.WithProfile(termenv.Ascii))
		fmt.Fprint(noColorOut, VertBoldDash3+" ")
		for _, p := range parents {
			fe.renderStepTitle(ctx, noColorOut, r, p, prefix+indent, true)
			fmt.Fprintf(noColorOut, " › ")
		}
		fmt.Fprint(out, out.String(context.String()).Foreground(termenv.ANSIBrightBlack).Faint())
		fmt.Fprintln(out)
	}
	r.fancyIndent(out, row, false, false)
	if !fe.finalRender {
		fmt.Fprint(out, "  ")
	}
	fe.renderStepTitle(ctx, out, r, rootCauseRow, prefix+indent, false)
	fmt.Fprintln(out)
	if logs := fe.logs.Logs[rootCauseRow.Span.ID]; logs != nil {
		if row.Depth == 0 && fe.finalRender {
			logs.SetPrefix("")
		} else {
			pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(rootCauseRow.Span)).String()
			logs.SetPrefix(indentBuf.String() + pipe + " ")
		}
		if fe.finalRender {
			logs.SetHeight(logs.UsedHeight())
		} else {
			logs.SetHeight(fe.window.Height / 3)
		}
		fmt.Fprint(out, logs.View())
	}
	fe.renderStepError(out, r, rootCauseRow, indentBuf.String())

	fe.shownErrs[rootCause.ID] = true
}

func (fe *frontendPretty) hasShownRootError() bool {
	if fe.err == nil {
		return false
	}
	origins := telemetry.ParseErrorOrigins(fe.err.Error())
	if len(origins) == 0 {
		return false
	}
	for _, origin := range origins {
		if !origin.IsValid() {
			return false
		}
		if !fe.shownErrs[dagui.SpanID{SpanID: origin.SpanID()}] {
			return false
		}
	}
	return true
}

func (fe *frontendPretty) renderStepError(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	if len(row.Span.ErrorOrigins.Order) > 0 {
		// span's error originated elsewhere; don't repeat the message, the ERROR status
		// links to its origin instead
		return
	}
	fe.shownErrs[row.Span.ID] = true
	errorCounts := map[string]int{}
	for _, span := range row.Span.Errors().Order {
		errText := span.Status.Description
		if errText == "" {
			continue
		}
		errorCounts[errText]++
	}
	type errWithCount struct {
		text  string
		count int
	}
	var counts []errWithCount
	for errText, count := range errorCounts {
		counts = append(counts, errWithCount{errText, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			return counts[i].text < counts[j].text
		}
		return counts[i].count > counts[j].count
	})
	for _, c := range counts {
		errText, count := c.text, c.count
		// Calculate available width for text
		prefixWidth := lipgloss.Width(prefix)
		indentWidth := row.Depth * 2 // Assuming indent is 2 spaces per depth level
		markerWidth := 2             // "! " prefix
		availableWidth := fe.contentWidth - prefixWidth - indentWidth - markerWidth
		if availableWidth > 0 {
			errText = cellbuf.Wrap(errText, availableWidth, "")
		}

		if count > 1 {
			errText = fmt.Sprintf("%dx ", count) + errText
		}

		// Print each wrapped line with proper indentation
		first := true
		for line := range strings.SplitSeq(strings.TrimSpace(errText), "\n") {
			fmt.Fprint(out, prefix)
			r.fancyIndent(out, row, false, false)
			var symbol string
			if first {
				symbol = "!"
			} else {
				symbol = " "
			}
			fmt.Fprintf(out,
				out.String("%s %s").Foreground(termenv.ANSIRed).String(),
				symbol,
				line,
			)
			fmt.Fprintln(out)
			first = false
		}
	}
}

func (fe *frontendPretty) renderStepTitle(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, abridged bool) error {
	span := row.Span
	chained := row.Chained
	depth := row.Depth
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.formWrap == nil

	if !abridged && row.Span.LLMRole == "" {
		fe.renderStatusIcon(ctx, out, row)
		fmt.Fprint(out, " ")
	}

	if r.Debug {
		fmt.Fprintf(out, out.String("%s ").Foreground(termenv.ANSIBrightBlack).String(), span.ID)
	}

	var empty bool
	if span.Message != "" {
		// when a span represents a message, we don't need to print its name
		//
		// NOTE: arguably this should be opt-in, but it's not clear how the
		// span name relates to the message in all cases; is it the
		// subject? or author? better to be explicit with attributes.
		if fe.renderStepLogs(out, r, row, prefix, isFocused) {
			if span.LLMRole == telemetry.LLMRoleUser {
				// Bail early if we printed a user message span; these don't have any
				// further information to show. Duration is always 0, metrics are empty,
				// status is always OK.
				return nil
			}
			r.fancyIndent(out, row, false, false)
			bar := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
			if isFocused {
				bar = hl(bar)
			}
			fmt.Fprint(out, bar)
		} else {
			empty = true
		}
	} else if call := span.Call(); call != nil {
		if err := r.renderCall(out, span, call, prefix, chained, depth, span.Internal, row, abridged); err != nil {
			return err
		}
	} else if span != nil {
		if span.Name == "" {
			empty = true
		}
		if err := r.renderSpan(out, span, span.Name); err != nil {
			return err
		}
	}

	if span != nil && !abridged {
		// TODO: when a span has child spans that have progress, do 2-d progress
		// fe.renderVertexTasks(out, span, depth)
		r.renderDuration(out, span, !empty)

		// Render RollUp dots after status/duration for collapsed RollUp spans
		if span.RollUpSpans {
			dots := fe.renderRollUpDots(out, span, row, prefix, fe.FrontendOpts)
			if dots != "" {
				fmt.Fprint(out, " ")
				fmt.Fprint(out, dots)
			}
		}

		fe.renderStatus(out, span)
		r.renderMetrics(out, span)

		summary := map[string]int{}
		for effect := range span.EffectSpans {
			if effect.Passthrough {
				// Don't show spans which are aggressively hidden.
				continue
			}
			icon, isInteresting := fe.statusIcon(ctx, effect)
			if !isInteresting {
				// summarize boring statuses, rather than showing them in full
				summary[icon]++
				continue
			}
			fmt.Fprintf(out, " %s ", out.String(icon).Foreground(statusColor(effect)))
			r.renderSpan(out, effect, effect.Name)
		}

		for _, icon := range statusOrder {
			count := summary[icon]
			if count > 0 {
				color := statusColors[icon]
				fmt.Fprintf(out, " %s %s",
					out.String(icon).Foreground(color).Faint(),
					out.String(strconv.Itoa(count)).Faint())
			}
		}
	}

	return nil
}

func (fe *frontendPretty) renderStep(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) error {
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.formWrap == nil

	fmt.Fprint(out, prefix)
	r.fancyIndent(out, row, false, true)

	if row.Span.LLMRole != "" {
		switch row.Span.LLMRole {
		case telemetry.LLMRoleUser:
			fmt.Fprint(out, out.String(Block).Foreground(termenv.ANSIMagenta))
		case telemetry.LLMRoleAssistant:
			fmt.Fprint(out, out.String(VertBoldBar).Foreground(termenv.ANSIMagenta))
		}
		fmt.Fprint(out, " ")
	} else if !fe.finalRender {
		fe.renderToggler(out, row, isFocused)
		fmt.Fprint(out, " ")
	}

	if err := fe.renderStepTitle(ctx, out, r, row, prefix, false); err != nil {
		return err
	}

	// User prompts already have a trailing newline from renderLogs,
	// so skip the extra newline to avoid a blank gap.
	if row.Span.LLMRole != telemetry.LLMRoleUser {
		fmt.Fprintln(out)
	}

	return nil
}

var statusOrder = []string{
	DotFilled,
	IconSuccess,
	IconCached,
	IconSkipped,
	DotEmpty,
}

var statusColors = map[string]termenv.Color{
	DotHalf:     termenv.ANSIYellow,
	IconCached:  termenv.ANSIBlue,
	IconSkipped: termenv.ANSIBrightBlack,
	IconFailure: termenv.ANSIRed,
	DotEmpty:    termenv.ANSIBrightBlack,
	DotFilled:   termenv.ANSIGreen,
	IconSuccess: termenv.ANSIGreen,
}

// brailleDots maps a count (0-8) to a Braille unicode character showing that many dots
// Braille patterns "pile up" from bottom to top, left to right
var brailleDots = []rune{
	' ',      // 0 dots: empty space
	'\u2840', // 1 dot:  ⡀ (bottom-left)
	'\u2844', // 2 dots: ⡄ (bottom-left, top-left)
	'\u2846', // 3 dots: ⡆ (bottom-left, top-left, middle-left)
	'\u2847', // 4 dots: ⡇ (left column full)
	'\u28C7', // 5 dots: ⣇ (left column + bottom-right)
	'\u28E7', // 6 dots: ⣧ (left column + bottom-right, top-right)
	'\u28F7', // 7 dots: ⣷ (left column + bottom-right, top-right, middle-right)
	'\u28FF', // 8 dots: ⣿ (all dots filled)
}

// renderRollUpDots renders a visual summary of child span states using pre-computed state
func (fe *frontendPretty) renderRollUpDots(out TermOutput, span *dagui.Span, row *dagui.TraceRow, prefix string, _ dagui.FrontendOpts) string {
	if !span.RollUpSpans {
		return ""
	}

	// Use pre-computed state instead of computing on every frame
	state := span.RollUpState()
	if state == nil {
		return ""
	}

	// Calculate available width for dots
	// Account for: prefix + indent (2 spaces per depth) + toggler + space + span name (rough estimate)
	prefixWidth := lipgloss.Width(prefix)
	indentWidth := row.Depth * 2
	togglerWidth := 2 // toggler icon + space
	nameWidth := lipgloss.Width(span.Name)

	// Estimate width used by duration, metrics, status, effect summary
	// This is a rough estimate - duration ~10 chars, status ~10 chars
	extraWidth := 25

	usedWidth := prefixWidth + indentWidth + togglerWidth + nameWidth + extraWidth
	// Need at least some space for dots (minimum 5 characters for " " + 1 braille char)
	availableWidth := max(fe.contentWidth-usedWidth, 5)

	// Calculate total spans across all statuses
	totalSpans := state.SuccessCount + state.CachedCount + state.FailedCount +
		state.CanceledCount + state.RunningCount + state.PendingCount

	if totalSpans == 0 {
		return ""
	}

	// Each Braille char packs 8 dots. Calculate how many chars we can fit.
	// Reserve 1 char for spacing between groups.
	maxChars := availableWidth
	maxDots := maxChars * 8

	// Calculate scale factor: how many spans per dot
	// Start at 1:1, then scale up as needed (1:1, 2:1, 3:1, 4:1, 5:1, 10:1, etc.)
	scale := 1
	for totalSpans/scale > maxDots {
		if scale < 5 {
			scale++
		} else {
			scale = (scale/5 + 1) * 5 // Jump by 5s after reaching 5
		}
	}

	var result strings.Builder

	// Helper to render a group of dots with a given count and color
	renderGroup := func(count int, color termenv.Color) {
		if count == 0 {
			return
		}
		// Scale down the count
		dotCount := (count + scale - 1) / scale // Round up
		for i := 0; i < dotCount; i += 8 {
			dotsInChar := min(dotCount-i, 8)
			braille := string(brailleDots[dotsInChar])
			styled := out.String(braille).Foreground(color)
			result.WriteString(styled.String())
		}
	}

	// Show scale indicator if we're not at 1:1
	if scale > 1 {
		scaleIndicator := fmt.Sprintf("%d×", scale)
		styled := out.String(scaleIndicator).Foreground(termenv.ANSIBrightBlack).Faint()
		result.WriteString(styled.String())
	}

	// Render in order: success, cached, failed, canceled, running, pending
	// This creates a "settling" effect from right to left as tasks start and complete
	renderGroup(state.SuccessCount, termenv.ANSIGreen)
	renderGroup(state.CachedCount, termenv.ANSIBlue)
	renderGroup(state.FailedCount, termenv.ANSIRed)
	renderGroup(state.CanceledCount, termenv.ANSIBrightBlack)
	renderGroup(state.RunningCount, termenv.ANSIYellow)
	renderGroup(state.PendingCount, termenv.ANSIBrightBlack)

	return result.String()
}

// statusIcon returns an icon indicating the span's status, and a bool
// indicating whether it's interesting enough to reveal at a summary level.
func (fe *frontendPretty) statusIcon(ctx tuist.Context, span *dagui.Span) (string, bool) {
	if span.IsRunningOrEffectsRunning() {
		if sr, ok := fe.spanTrees[span.ID]; ok && sr.spinner != nil {
			return sr.RenderChildInline(ctx, sr.spinner), true
		}
		return "?", true
	} else if span.IsCached() {
		return IconCached, false
	} else if span.IsCanceled() {
		return IconSkipped, false
	} else if span.IsFailedOrCausedFailure() {
		return IconFailure, true
	} else if span.IsPending() {
		return DotEmpty, false
	} else {
		return IconSuccess, false
	}
}

func (fe *frontendPretty) renderToggler(out TermOutput, row *dagui.TraceRow, isFocused bool) {
	var icon termenv.Style
	if row.HasChildren || row.Span.HasLogs {
		if row.Expanded {
			icon = out.String(CaretDownFilled).Foreground(termenv.ANSIBrightBlack)
		} else {
			icon = out.String(CaretRightFilled).Foreground(termenv.ANSIBrightBlack)
		}
	} else {
		// Use a placeholder symbol for items without children
		icon = out.String(DotFilled).Foreground(termenv.ANSIBrightBlack)
	}

	// Apply focus highlighting to chevron only
	if isFocused {
		icon = hl(icon.Foreground(statusColor(row.Span)))
	}
	fmt.Fprint(out, icon.String())
}

func (fe *frontendPretty) renderStatusIcon(ctx tuist.Context, out TermOutput, row *dagui.TraceRow) {
	// Then render the status icon (without focus highlighting)
	icon, _ := fe.statusIcon(ctx, row.Span)
	statusIcon := out.String(icon).Foreground(statusColor(row.Span))
	fmt.Fprint(out, statusIcon.String())
}

func (fe *frontendPretty) renderStatus(out TermOutput, span *dagui.Span) {
	if span.CheckPassed {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("OK").Foreground(termenv.ANSIGreen))
	} else if span.IsFailedOrCausedFailure() && !span.IsCanceled() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("ERROR").Foreground(termenv.ANSIRed))
		if len(span.ErrorOrigins.Order) > 0 && !fe.reportOnly && !fe.finalRender {
			color := termenv.ANSIBrightBlack
			_, focusedAnyOrigin := span.ErrorOrigins.Map[fe.FocusedSpan]
			if time.Since(fe.pressedKeyAt) < keypressDuration && focusedAnyOrigin {
				color = termenv.ANSIWhite
			}
			fmt.Fprintf(out, " %s %s",
				out.String("r").Foreground(color).Bold(),
				out.String("jump ↴").Foreground(color),
			)
		}
	} else if !span.IsRunningOrEffectsRunning() && span.IsCached() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("CACHED").Foreground(termenv.ANSIBlue))
	}
}

func (fe *frontendPretty) renderLogs(out TermOutput, r *renderer, row *dagui.TraceRow, logs *Vterm, height int, prefix string, focused bool) bool {
	span := row.Span
	depth := row.Depth

	pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
	dashed := out.String(VertBoldDash3).Foreground(restrainedStatusColor(span))
	if focused {
		pipe = hl(pipe)
		dashed = hl(dashed)
	}

	if depth == -1 {
		// clear prefix when zoomed
		logs.SetPrefix(prefix)
	} else {
		pipeBuf := new(strings.Builder)
		fmt.Fprint(pipeBuf, prefix)
		indentOut := NewOutput(pipeBuf, termenv.WithProfile(fe.profile))
		r.fancyIndent(indentOut, row, false, false)
		fmt.Fprint(indentOut, pipe)
		fmt.Fprint(indentOut, out.String(" "))
		logs.SetPrefix(pipeBuf.String())
	}
	if height <= 0 {
		height = logs.UsedHeight()
	}
	trimmed := logs.UsedHeight() - height
	if trimmed > 0 {
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row, false, false)
		fmt.Fprint(out, dashed)
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("...").Foreground(termenv.ANSIBrightBlack))
		fmt.Fprintf(out, out.String("%d").Foreground(termenv.ANSIBrightBlack).Bold().String(), trimmed)
		fmt.Fprintln(out, out.String(" lines hidden...").Foreground(termenv.ANSIBrightBlack))
	}
	logs.SetHeight(height)
	view := logs.View()
	if view == "" {
		return false
	}
	fmt.Fprint(out, view)
	return true
}

// ---------- pretty logs (unchanged) -----------------------------------------

type prettyLogs struct {
	DB            *dagui.DB
	Logs          map[dagui.SpanID]*Vterm
	PrefixWriters map[dagui.SpanID]*multiprefixw.Writer
	LogWidth      int
	SawEOF        map[dagui.SpanID]bool
	Profile       termenv.Profile
	Output        TermOutput
}

func newPrettyLogs(profile termenv.Profile, db *dagui.DB) *prettyLogs {
	return &prettyLogs{
		DB:            db,
		Logs:          make(map[dagui.SpanID]*Vterm),
		PrefixWriters: make(map[dagui.SpanID]*multiprefixw.Writer),
		LogWidth:      -1,
		Profile:       profile,
		SawEOF:        make(map[dagui.SpanID]bool),
		Output:        termenv.NewOutput(io.Discard, termenv.WithProfile(profile)),
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		// Check for Markdown content type
		contentType := ""
		eof := false
		verbose := false
		global := false
		for attr := range log.WalkAttributes {
			switch attr.Key {
			case telemetry.ContentTypeAttr:
				contentType = attr.Value.AsString()
			case telemetry.StdioEOFAttr:
				eof = attr.Value.AsBool()
			case telemetry.LogsGlobalAttr:
				global = attr.Value.AsBool()
			case telemetry.LogsVerboseAttr:
				verbose = attr.Value.AsBool()
			}
		}

		spanID := l.DB.LogTargetSpanID(log)
		if !spanID.IsValid() {
			continue
		}
		if eof && spanID.IsValid() {
			l.SawEOF[spanID] = true
			continue
		}

		pw, rolledUp := l.findRollUpSpan(spanID)
		if rolledUp && !verbose && !global {
			var context string
			span, ok := l.DB.Spans.Map[spanID]
			if ok {
				context = l.extractSpanContext(span)
			} else {
				context = spanID.String()
			}
			pw.Prefix = l.Output.String("["+context+"]").Foreground(termenv.ANSICyan).String() + " "
			fmt.Fprint(pw, log.Body().AsString())
		}

		vterm := l.spanLogs(spanID)
		if contentType == "text/markdown" {
			_, _ = vterm.WriteMarkdown([]byte(log.Body().AsString()))
		} else {
			_, _ = fmt.Fprint(vterm, log.Body().AsString())
		}
	}
	return nil
}

func (l *prettyLogs) flushResolvedLogsForSpan(spanID dagui.SpanID) bool {
	logs := l.DB.DrainResolvedLogs(spanID)
	if len(logs) == 0 {
		return false
	}
	_ = l.Export(context.Background(), logs)
	return true
}

// extractSpanContext extracts a meaningful context label from a span
func (l *prettyLogs) extractSpanContext(span *dagui.Span) string {
	call := span.Call()
	if call == nil {
		return span.Name
	}

	// Handle withExec: extract first argument (the command)
	if call.Field == "withExec" {
		if len(call.Args) > 0 && call.Args[0].Name == "args" {
			// The args value is a list literal
			if argList := call.Args[0].Value.GetList(); argList != nil {
				if len(argList.Values) > 0 {
					// Extract just the command name (first element of the list)
					cmd := argList.Values[0].GetString_()
					if cmd != "" {
						return cmd
					}
				}
			}
		}
		return "exec"
	}

	// For function calls, use the function name
	if call.Field != "" {
		return call.Field
	}

	// Fallback to span name
	return span.Name
}

func (l *prettyLogs) findRollUpSpan(origID dagui.SpanID) (*multiprefixw.Writer, bool) {
	id := origID
	for {
		span := l.DB.Spans.Map[id]
		if span == nil {
			break
		}
		if span.Boundary || span.Encapsulate || span.Internal {
			break
		}
		if span.RollUpLogs {
			// Found a roll-up span; find-or-create a prefixed writer for it.
			pw, found := l.PrefixWriters[id]
			if !found {
				vterm := l.spanLogs(id)
				pw = multiprefixw.New(vterm)
				l.PrefixWriters[id] = pw
			}
			return pw, true
		}
		if span.ParentID.IsValid() {
			// Keep walking upward
			id = span.ParentID
		} else {
			break
		}
	}
	return nil, false
}

func (l *prettyLogs) spanLogs(spanID dagui.SpanID) *Vterm {
	term, found := l.Logs[spanID]
	if !found {
		term = NewVterm(l.Profile)
		if l.LogWidth > -1 {
			term.SetWidth(l.LogWidth)
		}
		l.Logs[spanID] = term
	}
	return term
}

func (l *prettyLogs) SetWidth(width int) {
	l.LogWidth = width
	for _, vt := range l.Logs {
		vt.SetWidth(width)
	}
}

func (l *prettyLogs) Shutdown(ctx context.Context) error {
	return nil
}

func findTTYs() (in io.Reader, out io.Writer) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		in = os.Stdin
	}
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		if term.IsTerminal(int(f.Fd())) {
			out = f
			break
		}
	}
	return
}

// TermOutput is an interface that captures the methods we need from termenv.Output
type TermOutput interface {
	io.Writer
	String(...string) termenv.Style
	ColorProfile() termenv.Profile
}

func (fe *frontendPretty) handlePromptBool(ctx context.Context, title, message string, dest *bool) error {
	done := make(chan struct{})

	fe.dispatch(func() {
		fe.handlePromptForm(
			NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(title).
						Description(strings.TrimSpace((&Markdown{
							Content: message,
							Width:   fe.window.Width,
						}).View())).
						Value(dest),
				),
			),
			func(f *huh.Form) { close(done) },
		)
		fe.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (fe *frontendPretty) handlePromptString(ctx context.Context, title, message string, dest *string) error {
	done := make(chan struct{})

	fe.dispatch(func() {
		fe.handlePromptForm(
			NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title(title).
						Description(strings.TrimSpace((&Markdown{
							Content: message,
							Width:   fe.window.Width,
						}).View())).
						Value(dest),
				),
			),
			func(f *huh.Form) { close(done) },
		)
		fe.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func handleTelemetryErrorOutput(w io.Writer, to TermOutput, err error) {
	if err != nil {
		fmt.Fprintf(w, "%s - %s\n(%s)\n", to.String("WARN").Foreground(termenv.ANSIYellow), "failures detected while emitting telemetry. trace information incomplete", err.Error())
		fmt.Fprintln(w)
	}
}

var (
	ANSIBlack         = lipgloss.Black
	ANSIRed           = lipgloss.Red
	ANSIGreen         = lipgloss.Green
	ANSIYellow        = lipgloss.Yellow
	ANSIBlue          = lipgloss.Blue
	ANSIMagenta       = lipgloss.Magenta
	ANSICyan          = lipgloss.Cyan
	ANSIWhite         = lipgloss.White
	ANSIBrightBlack   = lipgloss.BrightBlack
	ANSIBrightRed     = lipgloss.BrightRed
	ANSIBrightGreen   = lipgloss.BrightGreen
	ANSIBrightYellow  = lipgloss.BrightYellow
	ANSIBrightBlue    = lipgloss.BrightBlue
	ANSIBrightMagenta = lipgloss.BrightMagenta
	ANSIBrightCyan    = lipgloss.BrightCyan
	ANSIBrightWhite   = lipgloss.BrightWhite
)

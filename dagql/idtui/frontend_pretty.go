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

	// logProvider lazily fetches a span's logs on demand (e.g. on expand, or
	// when a failure is surfaced). The bool is whether to roll up descendant
	// logs (span.RollUpLogs). Set by 'dagger trace' to pull recorded logs per
	// span; nil for live runs. requestedLogs dedups so each span is fetched once.
	logProvider   func(dagui.SpanID, bool)
	requestedLogs map[dagui.SpanID]bool

	// spanProvider lazily fetches a span's children on demand when the user
	// expands it (or it's surfaced/zoomed). Set by 'dagger trace' to fetch
	// deeper spans incrementally instead of loading the whole trace up front;
	// nil for live runs. requestedSpans dedups so each span is fetched once.
	spanProvider   func(dagui.SpanID)
	requestedSpans map[dagui.SpanID]bool

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

	// traceID is the trace being rendered, set by 'dagger trace' so surfaced
	// failure logs can point at 'dagger cloud logs <trace> <span>' for the full,
	// untruncated output. Empty for live runs (no follow-up command applies).
	traceID string

	// pinnedZoom is an explicitly requested zoom (e.g. 'dagger trace
	// --span/--check/--test') that persists into the final, non-interactive
	// render, where an ordinary zoom is otherwise reset to the primary span.
	pinnedZoom dagui.SpanID

	// TUI state/config
	spinnerEpoch time.Time // shared epoch so all spinners animate in sync
	profile      termenv.Profile
	window       windowSize // terminal dimensions
	contentWidth int
	browserBuf   *strings.Builder // logs if browser fails
	finalRender  bool             // whether we're doing the final render
	claims       *renderClaims
	stdin        io.Reader // used by backgroundMsg for running terminal
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
	spanTrees      map[dagui.SpanID]*SpanTreeView
	topTrees       []*SpanTreeView // top-level tree views, ordered
	statusSpinners map[dagui.SpanID]*tuist.Spinner
	renderVersion  uint64 // bumped on global render config changes (verbosity, zoom)

	// progressExpanded tracks rows whose completed-transfer roll-up has
	// been expanded into individual rows (the "p" keybind, distinct from
	// regular tree expansion).
	progressExpanded map[dagui.SpanID]bool

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

	// test view state
	testsMode        bool
	testsReturnSpan  dagui.SpanID
	fullscreenTests  *TestView
	testViews        map[dagui.SpanID]*TestView
	orphanTests      *TestView
	testSpanChildren map[dagui.SpanID]*TestSpanChildrenView

	// fullscreen log pager state
	logPager       *LogPagerView
	logPagerReturn func()
	logSearchInput *tuist.TextInput
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
type spanTreeScope struct {
	rowsView  *dagui.RowsView
	rows      *dagui.Rows
	opts      dagui.FrontendOpts
	spanTrees map[dagui.SpanID]*SpanTreeView
}

type SpanTreeView struct {
	tuist.Compo
	fe     *frontendPretty
	spanID dagui.SpanID
	scope  *spanTreeScope

	// finalRender and renderVersion are synced from frontendPretty before
	// rendering. Render reads these instead of relying on hidden frontend state
	// so Tuist knows when this component's cached output is invalid.
	finalRender   bool
	renderVersion uint64

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

	// statusSpinners are inline spinner components owned by this rendered
	// occurrence of a span tree. They are keyed by the status span ID because a
	// row can also summarize running effect spans in its title.
	statusSpinners map[dagui.SpanID]*tuist.Spinner

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
var _ tuist.Dismounter = (*SpanTreeView)(nil)

// SetFocused is called by tuist when this component gains or loses focus.
// This is O(1) — only the old and new focused components are notified.
func (s *SpanTreeView) SetFocused(_ tuist.Context, focused bool) {
	if s.focused != focused {
		s.focused = focused
		s.Update()
	}
}

func (s *SpanTreeView) OnDismount() {
	s.focused = false
}

// Render produces the lines for this span tree node and its children.
// Prefix, child, and focus state is synced by the owning tree renderer before
// RenderChild reaches this component.
func (s *SpanTreeView) Render(ctx tuist.Context) {
	rows := s.rows()
	if rows == nil {
		return
	}
	row := rows.BySpan[s.spanID]
	if row == nil {
		return
	}

	maxLiteralWidth := s.fe.contentWidth / 2
	if s.scope != nil && ctx.Width > 0 {
		maxLiteralWidth = ctx.Width / 2
	}
	r := newRenderer(s.fe.db, maxLiteralWidth, s.frontendOpts(), s.finalRender)
	visualFocused := s.focused && !s.finalRender

	s.selfLineCount = 0

	// Render the title (renderStep) into a separate buffer so we can
	// apply search highlighting to it without double-highlighting the
	// vterm log output (which handles its own highlighting via
	// SearchQuery/SearchCurrentRow).
	titleBuf := new(strings.Builder)
	titleOut := NewOutput(titleBuf, termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(titleOut)
	s.fe.renderStep(ctx, titleOut, r, row, "", s, visualFocused)
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

	if inlineTests := s.renderInlineTests(ctx, r, row); len(inlineTests) > 0 {
		s.selfLineCount += len(inlineTests)
		ctx.Lines(inlineTests...)
	}

	// Render the rest (logs, errors, debug) into a separate buffer.
	// Log highlighting is handled by the Vterm's own SearchQuery state,
	// so we do NOT apply highlightANSI to these lines.
	restBuf := new(strings.Builder)
	restOut := NewOutput(restBuf, termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(restOut)
	s.fe.renderRowContentRest(ctx, restOut, r, row, "", s, visualFocused)
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
		childRow := rows.BySpan[child.spanID]
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

func (s *SpanTreeView) rows() *dagui.Rows {
	if s.scope != nil {
		return s.scope.rows
	}
	return s.fe.rows
}

func (s *SpanTreeView) frontendOpts() dagui.FrontendOpts {
	if s.scope != nil {
		return s.scope.opts
	}
	return s.fe.FrontendOpts
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
	rows := s.rows()
	if rows == nil {
		return treePrefix{}
	}
	row := rows.BySpan[s.spanID]
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

// getOrCreateSpanTree returns the main SpanTreeView for the given span ID,
// creating one if it doesn't exist.
func (fe *frontendPretty) getOrCreateSpanTree(spanID dagui.SpanID) *SpanTreeView {
	return fe.getOrCreateSpanTreeInScope(spanID, nil)
}

// getOrCreateSpanTreeInScope returns the SpanTreeView for the given span ID in
// the given scope. Each scope owns its own component instances; a Tuist
// component must never be rendered in multiple places at once.
func (fe *frontendPretty) getOrCreateSpanTreeInScope(spanID dagui.SpanID, scope *spanTreeScope) *SpanTreeView {
	spanTrees := fe.spanTrees
	if scope != nil {
		if scope.spanTrees == nil {
			scope.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
		}
		spanTrees = scope.spanTrees
	} else if spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
		spanTrees = fe.spanTrees
	}

	st, ok := spanTrees[spanID]
	if !ok {
		st = &SpanTreeView{
			fe:     fe,
			spanID: spanID,
			scope:  scope,
		}
		spanTrees[spanID] = st
	}
	return st
}

type statusIconHost interface {
	RenderChildInline(tuist.Context, tuist.Component) string
	spinnerForStatus(dagui.SpanID) *tuist.Spinner
}

func (fe *frontendPretty) newStatusSpinner() *tuist.Spinner {
	sp := tuist.NewSpinner()
	sp.Epoch = fe.spinnerEpoch
	return sp
}

func (fe *frontendPretty) spinnerForStatus(spanID dagui.SpanID) *tuist.Spinner {
	if fe.statusSpinners == nil {
		fe.statusSpinners = make(map[dagui.SpanID]*tuist.Spinner)
	}
	sp, ok := fe.statusSpinners[spanID]
	if !ok {
		sp = fe.newStatusSpinner()
		fe.statusSpinners[spanID] = sp
	}
	return sp
}

func (s *SpanTreeView) spinnerForStatus(spanID dagui.SpanID) *tuist.Spinner {
	if s.statusSpinners == nil {
		s.statusSpinners = make(map[dagui.SpanID]*tuist.Spinner)
	}
	sp, ok := s.statusSpinners[spanID]
	if !ok {
		sp = s.fe.newStatusSpinner()
		s.statusSpinners[spanID] = sp
	}
	return sp
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
		claims:        newRenderClaims(),
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
				fmt.Fprintf(&fe.msgPreFinalRender, loggedOutTraceMsg, url)
			}
		}
		fe.Update()
	})
}

// SetTraceID records the trace being rendered so surfaced failure logs can point
// at 'dagger cloud logs <trace> <span>' for the full output. Called by 'dagger
// trace'; no-op for live runs.
func (fe *frontendPretty) SetTraceID(traceID string) {
	fe.dispatch(func() {
		fe.traceID = traceID
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
		stopHeartbeat := fe.startReportHeartbeat()
		cleanup, err := run(ctx)
		stopHeartbeat()
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

// reportHeartbeatInterval is how often report mode prints a one-line
// progress summary while work runs. Report mode is otherwise silent until
// the final report, which leaves non-interactive consumers (e.g. coding
// agents) with no liveness signal during long runs. Override with
// DAGGER_REPORT_HEARTBEAT (a Go duration; 0 disables).
const reportHeartbeatInterval = 30 * time.Second

func (fe *frontendPretty) startReportHeartbeat() func() {
	interval := reportHeartbeatInterval
	if v := os.Getenv("DAGGER_REPORT_HEARTBEAT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	if interval <= 0 || fe.Silent {
		return func() {}
	}

	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	start := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintln(fe.writer, fe.reportHeartbeatLine(time.Since(start)))
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

// reportHeartbeatLine summarizes in-flight work in a single line. Checks get
// first-class treatment since `dagger check` over a large repo is the
// longest-running everyday command.
func (fe *frontendPretty) reportHeartbeatLine(elapsed time.Duration) string {
	fe.reportMu.Lock()
	defer fe.reportMu.Unlock()

	now := time.Now()
	var checksDone, checksFailed int
	var runningChecks []string
	var runningSteps int
	for _, span := range fe.db.Spans.Order {
		running := span.IsRunningOrEffectsRunning()
		if running {
			// only count leaves to approximate "things actually executing"
			leaf := true
			for _, child := range span.ChildSpans.Order {
				if child.IsRunningOrEffectsRunning() {
					leaf = false
					break
				}
			}
			if leaf {
				runningSteps++
			}
		}
		if span.CheckName == "" {
			continue
		}
		switch {
		case running:
			runningChecks = append(runningChecks,
				fmt.Sprintf("%s (%s)", span.CheckName, dagui.FormatDuration(span.Activity.Duration(now))))
		case span.IsFailed():
			checksDone++
			checksFailed++
		default:
			checksDone++
		}
	}

	line := fmt.Sprintf("[dagger] %s elapsed", dagui.FormatDuration(elapsed))
	if total := checksDone + len(runningChecks); total > 0 {
		line += fmt.Sprintf(" · checks: %d/%d done", checksDone, total)
		if checksFailed > 0 {
			line += fmt.Sprintf(" (%d failed)", checksFailed)
		}
		if len(runningChecks) > 0 {
			const maxListed = 4
			listed := runningChecks
			if len(listed) > maxListed {
				listed = listed[:maxListed]
			}
			line += " · running: " + strings.Join(listed, ", ")
			if extra := len(runningChecks) - maxListed; extra > 0 {
				line += fmt.Sprintf(" (+%d more)", extra)
			}
		}
	} else if runningSteps > 0 {
		line += fmt.Sprintf(" · %d steps running", runningSteps)
	}
	return line
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

// SetLogProvider registers a callback that lazily supplies a span's logs. The
// frontend calls it when a span's logs become relevant: the user expands the
// span, or a failed span is surfaced in the view. The bool argument is whether
// to roll up descendant logs (the span's RollUpLogs). The provider should fetch
// asynchronously and feed results back through LogExporter. Used by 'dagger
// trace' to fetch recorded logs per span on demand instead of streaming the
// whole trace.
func (fe *frontendPretty) SetLogProvider(provider func(dagui.SpanID, bool)) {
	fe.dispatch(func() {
		fe.logProvider = provider
	})
}

// RequestSurfacedLogs asks the log provider for the logs of every failed span
// currently visible in the view. It's used by non-interactive ('report') mode,
// which renders only once: the caller invokes this after the spans are loaded,
// waits for the fetches it triggers, then the final render includes the
// surfaced failures' detail. Interactive mode surfaces these during its normal
// recalc loop, but calling this is harmless (requestLogs dedups).
func (fe *frontendPretty) RequestSurfacedLogs() {
	fe.dispatch(func() {
		fe.recalculateViewLocked()
	})
}

// requestLogs asks the log provider for a span's logs, once. It rolls up
// descendant logs when the span is marked RollUpLogs (e.g. a check or test
// whose real output lives in a sub-operation), mirroring the web UI.
func (fe *frontendPretty) requestLogs(id dagui.SpanID) {
	span, ok := fe.db.Spans.Map[id]
	if !ok {
		// Span not loaded yet; it'll be requested once it's surfaced.
		return
	}
	// Roll up descendants for spans marked RollUpLogs and for failed leaf test
	// cases, whose real output lives in a sub-operation even though the test span
	// isn't flagged. Deciding it here keeps every entry point -- expand, zoom,
	// surfaced failures -- consistent, so an early descendants=false fetch can't
	// dedupe the roll-up.
	descendants := span.RollUpLogs || fe.isFailingLeafTestSpan(id)
	// ...except a check whose failures are test cases: the report renders them
	// per-test (each test rolls up its own logs), never the check's own
	// rolled-up dump. Rolling up here would fetch the check's entire subtree --
	// every test's output, tens of MB -- that nothing renders.
	if span.CheckName != "" && fe.checkDefersToTests(span) {
		descendants = false
	}
	fe.requestLogsWith(id, descendants)
}

// isFailingLeafTestSpan reports whether id is the span of a failing leaf test
// case, whose descendant logs should roll up onto it.
func (fe *frontendPretty) isFailingLeafTestSpan(id dagui.SpanID) bool {
	tv := fe.db.TestView()
	if tv == nil {
		return false
	}
	return isFailingLeafTestCase(tv.BySpan[id])
}

// requestLogsWith asks the log provider for a span's logs once, forcing whether
// to roll up descendant logs. Used to roll up failed leaf test cases, whose real
// output lives in a sub-operation even though the test span isn't marked
// RollUpLogs.
func (fe *frontendPretty) requestLogsWith(id dagui.SpanID, descendants bool) {
	if fe.logProvider == nil || !id.IsValid() {
		return
	}
	if fe.requestedLogs[id] {
		return
	}
	if fe.requestedLogs == nil {
		fe.requestedLogs = make(map[dagui.SpanID]bool)
	}
	fe.requestedLogs[id] = true
	fe.logProvider(id, descendants)
}

// SetSpanProvider registers a callback that lazily supplies a span's children.
// The frontend calls it when a span's subtree becomes relevant: the user
// expands the span, or it's surfaced/zoomed. The provider should fetch
// asynchronously and feed results back through ImportSnapshots. Used by 'dagger
// trace' to fetch deeper spans on demand instead of streaming the whole trace.
func (fe *frontendPretty) SetSpanProvider(provider func(dagui.SpanID)) {
	fe.dispatch(func() {
		fe.spanProvider = provider
	})
}

// requestSpans asks the span provider for a span's children, once. The provider
// only needs to be asked when the server reported children we haven't loaded
// yet (ChildCount exceeds the children we actually have); otherwise the subtree
// is already present and expanding it is purely local.
func (fe *frontendPretty) requestSpans(id dagui.SpanID) {
	if fe.spanProvider == nil || !id.IsValid() {
		return
	}
	if fe.requestedSpans[id] {
		return
	}
	span, ok := fe.db.Spans.Map[id]
	if !ok {
		// Span not loaded yet; it'll be requested once it's surfaced.
		return
	}
	if span.ChildCount == 0 {
		// Leaf span: nothing deeper to fetch.
		return
	}
	if fe.requestedSpans == nil {
		fe.requestedSpans = make(map[dagui.SpanID]bool)
	}
	fe.requestedSpans[id] = true
	fe.spanProvider(id)
}

// ImportSnapshots folds a batch of span snapshots into the DB and refreshes the
// view. It's the snapshot-based counterpart to the OTLP ExportSpans path, used
// by 'dagger trace' which receives spans as snapshots from Cloud (carrying
// ChildCount and Partial, which the OTLP form drops). Mirrors the post-import
// bookkeeping ExportSpans does so logs and test views stay in sync.
func (fe *frontendPretty) ImportSnapshots(snapshots []dagui.SpanSnapshot) {
	if len(snapshots) == 0 {
		return
	}
	ids := make([]dagui.SpanID, len(snapshots))
	for i, s := range snapshots {
		ids[i] = s.ID
	}
	fe.dispatch(func() {
		fe.db.ImportSnapshots(snapshots)
		for _, id := range ids {
			if fe.logs.flushResolvedLogsForSpan(id) {
				fe.updateSpanTreesForLogs(id)
				fe.updateLogPagerForLogs(id)
			}
			if sr, ok := fe.spanTrees[id]; ok {
				sr.Update()
			}
		}
		fe.updateTestViews()
		// Don't recalculate here — set dirty flag so Render coalesces
		// multiple batches into one recalculate per frame.
		fe.viewDirty = true
		fe.Update()
	})
}

func (fe *frontendPretty) RevealAllSpans() {
	fe.dispatch(func() {
		fe.ZoomedSpan = dagui.SpanID{}
		fe.Update()
	})
}

// ZoomToSpan scopes the view to a span and treats it as expanded, mirroring the
// web UI's ?span= deep link. It pulls the span's logs and children on demand
// (via the registered providers) so 'dagger trace --span' can focus a subtree
// without loading the whole trace.
func (fe *frontendPretty) ZoomToSpan(id dagui.SpanID) {
	fe.dispatch(func() {
		if !id.IsValid() {
			return
		}
		fe.ZoomedSpan = id
		fe.pinnedZoom = id
		fe.FocusedSpan = id
		fe.autoFocus = false
		fe.setExpanded(id, true)
		fe.recalculateViewLocked()
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
	// Mark the frontend dirty so the final live frame observes fe.quitting and
	// renders blank instead of reusing cached progress rows. Without this, the
	// TUI can leave stale live output above the final render when NoExit exits
	// via q after the run has already completed.
	fe.Update()

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
	// (so don't show key hints etc.). syncSpanTreeState copies this into each
	// SpanTreeView and marks any changed tree dirty.
	if !fe.finalRender {
		fe.finalRender = true
		fe.Update()
	}

	// Unfocus for the final render.
	fe.focus(nil)

	// Render the full trace, or the pinned subtree when one was explicitly
	// requested (e.g. 'dagger trace --span/--check/--test').
	fe.claims = newRenderClaims()
	if fe.pinnedZoom.IsValid() {
		fe.ZoomedSpan = fe.pinnedZoom
	} else {
		fe.ZoomedSpan = fe.db.PrimarySpan
	}
	fe.viewDirty = false
	fe.recalculateViewLocked()

	out := NewOutput(w, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity >= dagui.ShowCompletedVerbosity || fe.err != nil || fe.db.HasTests() || fe.db.HasChecks() {
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
	if fe.reportOnly {
		// In report mode a failed plain-call's root cause is already rendered
		// above (renderRootCauseSection); the primary span's own logs are that
		// same output wrapped by the engine as `Error: ... Stdout: ... Stderr:
		// ...`. Replaying them here would duplicate the root cause (and reprint
		// the raw, un-vterm'd stream). Skip the replay for a failed primary
		// span; a passing call still replays its result (e.g. test output).
		if primary := fe.db.Spans.Map[fe.db.PrimarySpan]; primary != nil && primary.IsFailedOrCausedFailure() {
			return nil
		}
	}
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
				fe.updateLogPagerForLogs(id)
			}
			if sr, ok := fe.spanTrees[id]; ok {
				sr.Update()
			}
		}
		fe.updateTestViews()
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
		logSpanIDs := make(map[dagui.SpanID]struct{})
		for _, log := range logsCopy {
			spanID := fe.db.LogTargetSpanID(log)
			logSpanIDs[spanID] = struct{}{}
			fe.updateSpanTreesForLogs(spanID)
		}
		fe.db.LogExporter().Export(context.Background(), logsCopy)
		fe.logs.Export(context.Background(), logsCopy)
		for spanID := range logSpanIDs {
			fe.updateLogPagerForLogs(spanID)
		}
		fe.updateTestViews()
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
			key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "nav mode")),
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
	if fe.logSearchInput != nil {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"),
				key.WithHelp("enter", "search")),
			key.NewBinding(key.WithKeys("esc", "alt+esc"),
				key.WithHelp("esc", "cancel")),
		}
	}
	if fe.logPager != nil {
		return []key.Binding{
			key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"),
				key.WithHelp("↑↓", "scroll")),
			key.NewBinding(key.WithKeys("pgup", "pgdown", "space"),
				key.WithHelp("pgup", "page")),
			key.NewBinding(key.WithKeys("home"),
				key.WithHelp("home", "top")),
			key.NewBinding(key.WithKeys("end"),
				key.WithHelp("end", "bottom")),
			key.NewBinding(key.WithKeys("/"),
				key.WithHelp("/", "search")),
			key.NewBinding(key.WithKeys("n"),
				key.WithHelp("n", "next"),
				KeyEnabled(fe.logPager.SearchQuery != "")),
			key.NewBinding(key.WithKeys("N"),
				key.WithHelp("N", "prev"),
				KeyEnabled(fe.logPager.SearchQuery != "")),
			key.NewBinding(key.WithKeys("esc", "alt+esc"),
				key.WithHelp("esc", "back")),
			key.NewBinding(key.WithKeys("q"),
				key.WithHelp("q", "back")),
			key.NewBinding(key.WithKeys("ctrl+c"),
				key.WithHelp("ctrl+c", quitMsg)),
		}
	}
	var focused *dagui.Span
	if fe.testsMode {
		enterHelp := "detail"
		enterEnabled := false
		if fe.fullscreenTests != nil {
			focused = fe.currentLogSpan()
			enterEnabled = fe.fullscreenTests.FocusedNodeCanFocusDetail()
			if expanded, isGroup := fe.fullscreenTests.FocusedPassedGroupExpanded(); isGroup {
				enterEnabled = true
				enterHelp = "expand"
				if expanded {
					enterHelp = "collapse"
				}
			}
		}
		logSpan := fe.currentLogSpan()
		return []key.Binding{
			key.NewBinding(key.WithKeys("T"),
				key.WithHelp("T", "trace")),
			key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"),
				key.WithHelp("↑↓", "select")),
			key.NewBinding(key.WithKeys("home"),
				key.WithHelp("home", "first")),
			key.NewBinding(key.WithKeys("end", "space"),
				key.WithHelp("end", "last")),
			key.NewBinding(key.WithKeys("enter", "right", "l"),
				key.WithHelp("enter", enterHelp),
				KeyEnabled(enterEnabled)),
			key.NewBinding(key.WithKeys("t"),
				key.WithHelp("t", "start terminal"),
				KeyEnabled(focused != nil && fe.terminalCallback(focused) != nil)),
			key.NewBinding(key.WithKeys("L"),
				key.WithHelp("L", "logs"),
				KeyEnabled(fe.spanHasLogs(logSpan))),
			key.NewBinding(key.WithKeys("esc", "alt+esc"),
				key.WithHelp("esc", "trace")),
			key.NewBinding(key.WithKeys("q"),
				key.WithHelp("q", "trace")),
			key.NewBinding(key.WithKeys("ctrl+c"),
				key.WithHelp("ctrl+c", quitMsg)),
		}
	}
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
		key.NewBinding(key.WithKeys("T"),
			key.WithHelp("T", "tests"),
			KeyEnabled(fe.db != nil && fe.db.HasTests())),
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
		key.NewBinding(key.WithKeys("esc", "alt+esc"),
			key.WithHelp("esc", fe.escHelp()),
			KeyEnabled(fe.searchQuery != "" || (fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan))),
		key.NewBinding(key.WithKeys("r"),
			key.WithHelp("r", "go to error"),
			KeyEnabled(focused != nil && len(focused.ErrorOrigins.Order) > 0)),
		key.NewBinding(key.WithKeys("p"),
			key.WithHelp("p", progressToggleHelp(fe.progressExpanded[fe.FocusedSpan])),
			KeyEnabled(focused != nil && fe.spanHasProgressRollup(fe.FocusedSpan))),
		key.NewBinding(key.WithKeys("t"),
			key.WithHelp("t", "start terminal"),
			KeyEnabled(focused != nil && fe.terminalCallback(focused) != nil),
		),
		key.NewBinding(key.WithKeys("L"),
			key.WithHelp("L", "logs"),
			KeyEnabled(fe.spanHasLogs(focused)),
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

func isEscapeKey(keyStr string) bool {
	return keyStr == "esc" || keyStr == "alt+esc"
}

// ---------- tuist.Component -------------------------------------------------

// Render implements tuist.Component. It produces the full TUI output as lines.
func (fe *frontendPretty) Render(ctx tuist.Context) {
	if !fe.finalRender && (fe.backgrounded || fe.quitting) {
		return
	}
	fe.claims = newRenderClaims()

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
		fe.setWindowSizeLocked(windowSize{Width: ctx.Width, Height: ctx.ScreenHeight()})
	} else if fe.contentWidth <= 0 {
		// Final render without a live TUI (report mode). Set to 0
		// so the renderer doesn't truncate (maxLiteralLen = 0).
		fe.contentWidth = 0
	}

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts, fe.finalRender)

	if fe.finalRender {
		// Final render: emit progress rows and any unscoped tests, no chrome or truncation.
		pol := fe.renderPolicy()
		zoomed := fe.rowsView != nil && fe.rowsView.Zoomed != nil &&
			fe.rowsView.Zoomed.ID != fe.db.PrimarySpan

		// Lead the whole-trace report with the overall verdict -- did it pass or
		// fail, what command ran, and the top-level error -- the one-glance summary
		// the server-computed summary used to provide. A zoom titles itself below.
		if !zoomed {
			if hdr := fe.renderTraceHeader(r); len(hdr) > 0 {
				ctx.Lines(hdr...)
				ctx.Line("")
			}
		}

		// When scoped to a span (e.g. --test/--span/--check), title the subtree
		// with the zoomed span so it isn't a headless, mysteriously indented tree.
		if zoomed {
			zoomBuf := new(strings.Builder)
			zoomOut := NewOutput(zoomBuf, termenv.WithProfile(fe.profile))
			fe.renderStep(ctx, zoomOut, r, &dagui.TraceRow{
				Span:     fe.rowsView.Zoomed,
				Expanded: true,
			}, "", fe, false)
			linesFromView(ctx, zoomBuf.String())
			ctx.Line("") // separate the header from its content
		}

		rootCauseRendered := false
		if pol.showRootCause {
			// XXX: we always render the root cause for now, even when the same
			// failing span also shows up under a test below (the cause often
			// lives in a test, which already prints it -- so this can repeat the
			// test's logs). This is where a dedupe conditional would go, e.g.
			// skip an origin already covered by a rendered test. Compare both
			// cases on the litmus trace (a0d14706d2b326f778989c181585e9df):
			//   with root cause (current):
			//     dagger trace a0d14706d2b326f778989c181585e9df --full --check "test-split:test-container"
			//   without it (tests carry the cause):
			//     DAGGER_TRACE_RENDER=root dagger trace a0d14706d2b326f778989c181585e9df --full --check "test-split:test-container"
			if rcLines := fe.renderRootCauseSection(ctx, r); len(rcLines) > 0 {
				ctx.Lines(rcLines...)
				ctx.Line("")
				rootCauseRendered = true
			}
		}

		// At the root, render the checks reveal-independently: a CHECKS heading
		// with the pass/fail breakdown, then every surfaced check nested under
		// its parent (renderChecksSection). This replaces the reveal-based
		// progress rows, which miss checks nested under another check and drop
		// passing ones. Fall back to the progress tree when there are no surfaced
		// checks (e.g. a plain trace, or one whose only checks are test fixtures).
		var renderedRows bool
		if checkLines := fe.checksReport(ctx, r, zoomed); len(checkLines) > 0 {
			ctx.Lines(checkLines...)
			renderedRows = true
		} else if !rootCauseRendered {
			// Only fall back to the raw progress tree when there's nothing better.
			// A plain `dagger call` failure renders its root cause above; dumping
			// the bootstrap spans (connect / load workspace / parsing args) under
			// it would just be noise.
			progressLines := fe.renderProgressLines(r, ctx, 0)
			ctx.Lines(progressLines...)
			renderedRows = len(progressLines) > 0
		}

		if zoomed && pol.showOwnDescendantLogs {
			// Surface the scoped span's own rolled-up failure logs, the same
			// error-anchored window and 'dagger cloud logs' hint the summary uses.
			logOut := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
			if logLines := fe.renderZoomedFinalLogs(logOut, ""); len(logLines) > 0 {
				ctx.Line("")
				ctx.Lines(logLines...)
			}
		} else if zoomed && pol.showSubtests {
			// Zoomed to a check: show the tests beneath it (with their logs)
			// instead of the check's own rolled-up descendant log dump.
			if testLines := fe.renderZoomedCheckTests(ctx, fe.rowsView.Zoomed); len(testLines) > 0 {
				ctx.Line("")
				ctx.Lines(testLines...)
			}
		} else if !zoomed && pol.showSubtests {
			if testLines := fe.renderFinalGlobalTests(ctx); len(testLines) > 0 {
				if renderedRows {
					ctx.Line("")
				}
				ctx.Lines(testLines...)
			}
		}

		if pol.showSuggestions {
			var zoomSpan *dagui.Span
			if zoomed {
				zoomSpan = fe.rowsView.Zoomed
			}
			if suggLines := fe.renderSuggestionsSection(zoomSpan); len(suggLines) > 0 {
				ctx.Line("")
				ctx.Lines(suggLines...)
			}
		}
		return
	}

	if fe.logPager != nil {
		fe.logPager.RefreshSearch()
		fe.renderLogPager(ctx)
		return
	}

	if fe.testsMode {
		fe.renderTestsView(ctx)
		return
	}

	// Zoom header: split the zoomed span off as a header above its (unindented)
	// content, separated by a blank line.
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		zoomBuf := new(strings.Builder)
		zoomOut := NewOutput(zoomBuf, termenv.WithProfile(fe.profile))
		fe.renderStep(ctx, zoomOut, r, &dagui.TraceRow{
			Span:     fe.rowsView.Zoomed,
			Expanded: true,
		}, "", fe, false)
		linesFromView(ctx, zoomBuf.String())
		ctx.Line("")
	}

	// Pre-render chrome below progress to measure its height for truncation.
	// Global tests are rendered before progress so their claims can suppress
	// duplicate test logs in the trace rows above them.
	globalTestLines := fe.renderLiveGlobalTests(ctx)
	logsLines := fe.renderLogsLines("")

	chromeHeight := 1 + 1 // keymap (1 line, sibling) + gap after progress
	if len(logsLines) > 0 {
		chromeHeight += len(logsLines) + 1
	}
	if len(globalTestLines) > 0 {
		chromeHeight += len(globalTestLines) + 1
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
	if len(globalTestLines) > 0 {
		ctx.Lines(globalTestLines...)
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

// renderTraceHeader renders the trace's overall verdict at the top of the
// whole-trace report: the invoked command, whether it passed or failed, and the
// top-level error. The sections below explain the failure in detail; this is the
// one-glance outcome the server-computed summary used to lead with.
func (fe *frontendPretty) renderTraceHeader(r *renderer) []string {
	root := fe.db.RootSpan
	if root == nil {
		return nil
	}
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))

	icon, word, color := IconSuccess, "PASSED", termenv.ANSIGreen
	switch {
	case root.IsFailed():
		icon, word, color = IconFailure, "FAILED", termenv.ANSIRed
	case root.IsRunning():
		icon, word, color = Diamond, "RUNNING", termenv.ANSIYellow
	}
	status := out.String(fmt.Sprintf("%s %s", icon, word)).Foreground(color).String()
	lines := []string{reportHeadingLine(out, "TRACE") + "  " + status}

	name := root.Name
	if name == "" {
		name = "-"
	}
	dur := out.String(dagui.FormatDuration(root.Activity.Duration(r.now))).Faint().String()
	lines = append(lines, fmt.Sprintf("%s  %s", name, dur))

	// Top-level error, traceparent markers stripped (they're cross-SDK plumbing,
	// not part of the message). The detailed cause is rendered below.
	if root.IsFailed() {
		if msg := stripTraceparent(root.Status.Description); strings.TrimSpace(msg) != "" {
			for _, ln := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				lines = append(lines, out.String("! "+ln).Foreground(termenv.ANSIRed).String())
			}
		}
	}
	return lines
}

// stripTraceparent removes the cross-SDK "[traceparent:<trace>-<span>]" error
// markers (and any single space before them) from a message.
func stripTraceparent(s string) string {
	for {
		i := strings.Index(s, "[traceparent:")
		if i < 0 {
			return s
		}
		end := strings.IndexByte(s[i:], ']')
		if end < 0 {
			return s
		}
		start := i
		if start > 0 && s[start-1] == ' ' {
			start--
		}
		s = s[:start] + s[i+end+1:]
	}
}

// renderZoomedFinalLogs renders the zoomed span's rolled-up logs for the final
// report -- the same error-anchored window and 'dagger cloud logs' hint the test
// summary uses -- so 'dagger trace --full --test X' surfaces X's failure output
// (its descendants having been fetched and re-keyed onto it).
func (fe *frontendPretty) renderZoomedFinalLogs(out TermOutput, indent string) []string {
	span, ok := fe.db.Spans.Map[fe.ZoomedSpan]
	if !ok {
		return nil
	}
	logs := fe.logs.Logs[fe.ZoomedSpan]
	if logs == nil || logs.UsedHeight() == 0 {
		return nil
	}
	var buf strings.Builder
	if err := logs.PrintRaw(&buf); err != nil {
		return nil
	}
	rawLines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	return errorWindowLines(out, rawLines, indent, fe.traceID, cloudLogsTarget(span))
}

// renderZoomedCheckTests renders the tests beneath a zoomed check as inline
// summaries -- the same way they appear under the check in the unscoped report.
// When zoomed to a check the check is rendered as the (headerized) zoom root, so
// the normal renderInlineTests path doesn't fire; this surfaces them explicitly.
func (fe *frontendPretty) renderZoomedCheckTests(ctx tuist.Context, span *dagui.Span) []string {
	if span == nil || span.CheckName == "" {
		return nil
	}
	view := fe.db.TestViewForSpan(span)
	if !view.HasTests() {
		return nil
	}
	tv := &TestView{
		Profile:         fe.profile,
		Logs:            fe.logs.Logs,
		SummaryIndent:   2,
		SummaryLogLines: -1,
		TraceID:         fe.traceID,
	}
	width := ctx.Width
	if width <= 0 {
		width = finalRenderTestsWidth
	}
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	lines := tv.renderTestSummaryLines(out, view, max(width, finalRenderTestsWidth), finalTestViewHeight(tv))
	if len(lines) == 0 {
		return nil
	}
	fe.claims.claimTestReport(span, view)
	return lines
}

// reportHeadingLine renders a section title in the failure summary's style
// (daggercmd.section, which idtui can't import without a cycle): a flat,
// greppable "== TITLE ==" marker under an AI agent, or a bold heading for
// humans.
func reportHeadingLine(out TermOutput, title string) string {
	if RunningInAgent() {
		return fmt.Sprintf("== %s ==", title)
	}
	return out.String(title).Bold().String()
}

// reportSectionLines renders a titled block: the heading from reportHeadingLine
// with the body left at the margin under an agent or indented two spaces for
// humans. body lines are pre-rendered and may already carry styling.
func reportSectionLines(out TermOutput, title string, body []string) []string {
	if len(body) == 0 {
		return nil
	}
	lines := make([]string, 0, len(body)+1)
	lines = append(lines, reportHeadingLine(out, title))
	for _, b := range body {
		switch {
		case RunningInAgent(), b == "":
			lines = append(lines, b)
		default:
			lines = append(lines, "  "+b)
		}
	}
	return lines
}

// renderSuggestionsSection prints copy-paste 'dagger trace --full' commands that
// scope the report to a single failure, so the reader learns how to drill in
// with --check/--test. At the root it points at failed checks (and any failed
// tests not under a check); zoomed to a check it points at that check's failed
// tests. Returns nil when there's nothing to drill into or no trace ID to build
// a command from. Gated by traceRenderPolicy.showSuggestions at the call site.
func (fe *frontendPretty) renderSuggestionsSection(zoomed *dagui.Span) []string {
	if fe.db == nil || fe.traceID == "" {
		return nil
	}

	var targets []string
	seen := map[string]bool{}
	add := func(span *dagui.Span) {
		if span == nil {
			return
		}
		sel := cloudLogsTarget(span)
		if sel == "" || seen[sel] {
			return
		}
		seen[sel] = true
		targets = append(targets, sel)
	}

	if zoomed != nil && zoomed.CheckName != "" {
		for _, node := range failingLeafTestCases(fe.db.TestViewForSpan(zoomed)) {
			add(node.Span)
		}
	} else {
		// Root: surface the failed checks (broad) and the failing tests beneath
		// them (specific), so the reader can jump straight to either level. Use
		// the boundary-respecting check set so checks a test intentionally runs as
		// fixtures aren't suggested -- matching the CHECKS section and count.
		var walkChecks func(ns []*dagui.CheckNode)
		walkChecks = func(ns []*dagui.CheckNode) {
			for _, n := range ns {
				if n.Failed {
					add(n.Span)
				}
				walkChecks(n.Children)
			}
		}
		walkChecks(fe.db.SurfacedChecks())
		for _, node := range failingLeafTestCases(fe.db.TestView()) {
			add(node.Span)
		}
	}

	if len(targets) == 0 {
		return nil
	}

	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	body := make([]string, 0, len(targets))
	for _, sel := range targets {
		body = append(body, fmt.Sprintf("dagger trace %s --full %s", fe.traceID, sel))
	}
	return reportSectionLines(out, "MORE DETAILS", body)
}

// renderChecksHeader renders a "CHECKS" heading with the pass/fail tallies
// joined onto the same line (mirroring the TESTS inspector header), to sit above
// the root-level check rows (which carry their own tree indentation, so they're
// left unwrapped).
func (fe *frontendPretty) renderChecksHeader() []string {
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	line := reportHeadingLine(out, "CHECKS")
	for _, part := range fe.checkBreakdownParts(out) {
		line += "  " + part
	}
	return []string{line}
}

// checkBreakdownParts renders the failed/passed check tallies as "✘ N failed" /
// "✔ N passed" parts, in the same icon+color style as the test summary.
func (fe *frontendPretty) checkBreakdownParts(out TermOutput) []string {
	failed, passed := fe.checkStatusCounts()
	var parts []string
	add := func(count int, icon string, color termenv.Color, label string) {
		if count == 0 {
			return
		}
		parts = append(parts, out.String(fmt.Sprintf("%s %d %s", icon, count, label)).Foreground(color).String())
	}
	add(failed, IconFailure, termenv.ANSIRed, "failed")
	add(passed, IconSuccess, termenv.ANSIGreen, "passed")
	return parts
}

// checkStatusCounts tallies the surfaced checks into failed vs passed, so the
// CHECKS heading agrees with the rendered list -- both honor boundaries, so
// checks a test intentionally runs aren't counted. A check counts as failed if
// any of its spans failed (directly or via a failed link). NB: with incremental
// --full loading the passed tally only covers checks already fetched.
func (fe *frontendPretty) checkStatusCounts() (failed, passed int) {
	var walk func(ns []*dagui.CheckNode)
	walk = func(ns []*dagui.CheckNode) {
		for _, n := range ns {
			if n.Failed {
				failed++
			} else {
				passed++
			}
			walk(n.Children)
		}
	}
	walk(fe.db.SurfacedChecks())
	return failed, passed
}

// renderLogsLines returns the zoomed span's log output as lines.
func (fe *frontendPretty) renderLogsLines(prefix string) []string {
	logs := fe.logs.Logs[fe.ZoomedSpan]
	if logs == nil || logs.UsedHeight() == 0 || fe.claims.hasLog(fe.ZoomedSpan) || fe.hasShownRootError() {
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
	fe.promoteChecksLocked()
	fe.rowsView = fe.db.RowsView(fe.FrontendOpts)
	fe.rows = fe.rowsView.Rows(fe.FrontendOpts)

	// Eager: fetch logs for the failures we've surfaced, so their detail is ready
	// without the user expanding anything (and so the non-interactive report
	// includes it). Two surfaces: failed rows in the trace tree, and failed test
	// cases in the tests view (which aren't tree rows). Both are bounded to what
	// we actually render -- a handful -- not every failed span in the trace.
	// No-op for live runs (no provider).
	if fe.logProvider != nil {
		for _, row := range fe.rows.Order {
			if row.Span != nil && row.Span.IsFailed() {
				fe.requestLogs(row.Span.ID)
			}
		}
		if tv := fe.db.TestView(); tv != nil {
			for _, node := range tv.BySpan {
				if node != nil && node.Kind == dagui.TestNodeCase &&
					node.SelfCategory == dagui.TestCategoryFailing && node.Span != nil {
					// requestLogs rolls up a failed leaf test's descendants (its real
					// output lives in a sub-operation it ran, not the test span itself).
					fe.requestLogs(node.Span.ID)
				}
			}
		}
		// Eagerly fetch logs for the zoom target's root-cause origins so the
		// ROOT CAUSE section has its detail in the single final render.
		if fe.renderPolicy().showRootCause {
			if zoomSpan := fe.db.Spans.Map[fe.ZoomedSpan]; zoomSpan != nil {
				for _, origin := range fe.checkRootCauses(zoomSpan) {
					fe.requestLogs(origin.ID)
				}
			}
		}
		// For a plain call (no checks, no tests) fetch the primary span's own logs:
		// these are the command's result output, which the engine prints onto the
		// root span. renderPrimaryOutput replays db.PrimaryLogs at the end of the
		// run, so without this the report shows the call tree but never the output.
		// descendants=false keeps it to the primary output itself, not the whole
		// rolled-up build log.
		if fe.zoomKind() == zoomRoot && len(fe.db.SurfacedChecks()) == 0 {
			if tv := fe.db.TestView(); tv == nil || !tv.HasTests() {
				if prim := fe.db.PrimarySpan; prim.IsValid() {
					fe.requestLogsWith(prim, false)
				}
			}
		}
		// Eagerly fetch logs for surfaced failed checks' root causes so the
		// reveal-independent CHECKS section's inline failure detail is ready.
		// These aren't all tree rows -- a check nested under another check is
		// collapsed -- so the failed-row loop above misses them.
		eachFailedLeafCheck(fe.db.SurfacedChecks(), func(n *dagui.CheckNode) {
			for _, origin := range fe.checkRootCauses(n.Span) {
				fe.requestLogs(origin.ID)
			}
		})
	}

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

// promoteChecksLocked mirrors the web UI (cloud/components/trace.go): when a
// trace has checks, mark the root span passthrough so RowsView surfaces the
// revealed check spans -- all of them -- at the top level instead of the
// root's setup children (the session and per-module loads). Checks bubble up
// to the root via the reveal mechanism, so this reuses the existing tree/row
// rendering and navigation without constructing a synthetic tree. The
// passthrough branch in RowsView only fires when the root is the zoomed span,
// so default the zoom to the primary (root) span when nothing else has zoomed.
func (fe *frontendPretty) promoteChecksLocked() {
	if fe.db == nil || fe.db.RootSpan == nil || !fe.db.HasChecks() {
		return
	}
	if fe.db.RootSpan.CheckName != "" {
		// The root span is itself a check: there's no setup noise above it to
		// hide, and passing it through would reparent its children (the tests) to
		// the top level, breaking the inline tests-under-check view. Nothing to
		// promote.
		return
	}
	fe.db.RootSpan.Passthrough = true
	if !fe.ZoomedSpan.IsValid() {
		fe.ZoomedSpan = fe.db.PrimarySpan
	}
}

// applyTuistFocus sets tuist keyboard focus to the active view: the fullscreen
// test view in tests mode, the SpanTreeView for the selected span in trace mode,
// or fe itself when no span is selected. Skipped when editline or search has
// focus.
func (fe *frontendPretty) applyTuistFocus() {
	if fe.editlineFocused || fe.searchActive || fe.logSearchInput != nil {
		return
	}
	if fe.logPager != nil {
		fe.tui.SetFocus(fe.logPager)
		return
	}
	if fe.testsMode && fe.fullscreenTests != nil {
		fe.tui.SetFocus(fe.fullscreenTests)
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

// syncSpanTreeState synchronizes the main trace SpanTreeView component tree
// with the current rowsView and rows. Called from recalculateViewLocked()
// (i.e., from event handlers and Dispatch callbacks, never from Render).
// Scoped span tree renderers use syncTreeNodeInScope with their own rows.
//
// It walks the TraceTree top-down, creating/reusing SpanTreeViews,
// computing prefixes, and calling Update() on components whose
// visible state changed.
func (fe *frontendPretty) syncSpanTreeState() {
	if fe.spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}

	// A zoomed subtree renders at the margin: its root is split off as a header
	// (see Render), so the content below isn't indented under it.
	body := fe.rowsView.Body
	newTops := make([]*SpanTreeView, 0, len(body))
	for i, tree := range body {
		st := fe.getOrCreateSpanTree(tree.Span.ID)
		st.parent = nil
		st.indexInParent = i
		fe.syncTreeNode(st, treePrefix{})
		newTops = append(newTops, st)
	}
	fe.topTrees = newTops
}

// syncTreeNode recursively syncs a SpanTreeView and its children with
// the current trace data. Updates prefix, render mode, and children. Calls
// Update() on any SpanTreeView whose visible state changed.
func (fe *frontendPretty) syncTreeNode(st *SpanTreeView, newPrefix treePrefix) {
	fe.syncTreeNodeInScope(st, newPrefix, nil)
}

func (fe *frontendPretty) syncTreeNodeInScope(st *SpanTreeView, newPrefix treePrefix, scope *spanTreeScope) {
	changed := false

	// Sync scope
	if st.scope != scope {
		st.scope = scope
		changed = true
	}

	// Sync prefix
	if st.prefix != newPrefix {
		st.prefix = newPrefix
		changed = true
	}

	// Sync render mode and global render config version.
	if st.finalRender != fe.finalRender {
		st.finalRender = fe.finalRender
		changed = true
	}
	if st.renderVersion != fe.renderVersion {
		st.renderVersion = fe.renderVersion
		changed = true
	}

	if changed {
		st.Update()
	}

	rowsView := fe.rowsView
	opts := fe.FrontendOpts
	spanTrees := fe.spanTrees
	if scope != nil {
		rowsView = scope.rowsView
		opts = scope.opts
		if scope.spanTrees == nil {
			scope.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
		}
		spanTrees = scope.spanTrees
	}

	// Sync children for expanded nodes
	tree := rowsView.BySpan[st.spanID]
	if tree == nil || !tree.IsExpanded(opts) {
		// Collapsed: clear children so they get dismounted on next render
		if len(st.children) > 0 {
			st.children = nil
			st.Update()
		}
		return
	}

	// Determine visible children
	var childTrees []*dagui.TraceTree
	if tree.ShouldShowRevealedSpans(opts) {
		for _, revealedSpan := range tree.Span.RevealedSpans.Order {
			if revealedTree, ok := rowsView.BySpan[revealedSpan.ID]; ok {
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
				scope:  scope,
			}
			st.childMap[id] = child
			spanTrees[id] = child
		}
		child.parent = st
		child.indexInParent = i

		// Compute child prefix
		hasNext := i < len(childTrees)-1
		childPrefix := st.computeChildPrefix(out, hasNext)

		// Recurse
		fe.syncTreeNodeInScope(child, childPrefix, scope)
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

	if fe.finalRender {
		return allLines
	}

	// Crop the bottom so the focused span stays within the visible
	// screen area. Content above scrolls into terminal scrollback
	// naturally — we never crop the top.
	viewportHeight := max(ctx.ScreenHeight()-chromeHeight, 1)

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
	focusEnd := min(focusLine+focusHeight, totalLines)

	// Split remaining viewport space evenly above and below the focus root.
	remaining := max(viewportHeight-focusHeight, 0)
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
			for s := range idx {
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
		if !fe.editlineFocused && !fe.searchActive && !fe.testsMode {
			fe.tui.SetFocus(fe)
		}
	} else {
		newSpan = row.Span.ID
		fe.FocusedSpan = newSpan
		if !fe.editlineFocused && !fe.searchActive && !fe.testsMode {
			if sr, ok := fe.spanTrees[newSpan]; ok {
				fe.tui.SetFocus(sr)
			}
		}
	}
	// Invalidate the render caches of old and new SpanTreeViews when the
	// selected span changes. Tuist SetFocus handles visual focus invalidation;
	// this covers any remaining selected-span-dependent rendering.
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
	case "esc", "alt+esc":
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

	if fe.logPager != nil {
		switch keyStr {
		case "q", "esc", "alt+esc":
			fe.closeLogPager()
		case "ctrl+c":
			if fe.shell != nil {
				if fe.shellInterrupt != nil {
					fe.shellInterrupt(errors.New("interrupted"))
				}
			} else {
				fe.quitAction(ErrInterrupted)
			}
		case "down", "j":
			fe.logPager.ScrollBy(1)
		case "up", "k":
			fe.logPager.ScrollBy(-1)
		case "pgdown", "ctrl+f", "space":
			fe.logPager.ScrollPage(1)
		case "pgup", "ctrl+b":
			fe.logPager.ScrollPage(-1)
		case "home", "g":
			fe.logPager.ScrollToTop()
		case "end", "G":
			fe.logPager.ScrollToBottom()
		case "/":
			fe.enterLogPagerSearchMode()
		case "n":
			fe.logPager.SearchNext()
		case "N":
			fe.logPager.SearchPrev()
		}
		return
	}

	if fe.testsMode {
		switch keyStr {
		case "q", "T", "esc", "alt+esc":
			fe.closeTestsMode()
		case "ctrl+c":
			if fe.shell != nil {
				if fe.shellInterrupt != nil {
					fe.shellInterrupt(errors.New("interrupted"))
				}
			} else {
				fe.quitAction(ErrInterrupted)
			}
		case "left", "h":
			fe.testFocusLeft()
		case "down", "j":
			fe.goTestDown()
		case "up", "k":
			fe.goTestUp()
		case "home":
			fe.goTestStart()
		case "end", "G", "space":
			fe.goTestEnd()
		case "enter", "right", "l":
			fe.focusFocusedTestDetail()
		case "L":
			fe.openFocusedLogs()
		case "t":
			if span := fe.currentLogSpan(); span != nil {
				fe.FocusedSpan = span.ID
				fe.terminal()
			}
		}
		return
	}

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
	case "esc", "alt+esc":
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
	case "T":
		fe.toggleTestsMode()
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
	case "p":
		// toggle the focused row's completed-transfer roll-up between the
		// merged summary line and individual rows (distinct from regular
		// tree expansion)
		if fe.FocusedSpan.IsValid() && fe.spanHasProgressRollup(fe.FocusedSpan) {
			if fe.progressExpanded == nil {
				fe.progressExpanded = make(map[dagui.SpanID]bool)
			}
			fe.progressExpanded[fe.FocusedSpan] = !fe.progressExpanded[fe.FocusedSpan]
			if st, ok := fe.spanTrees[fe.FocusedSpan]; ok {
				st.Update()
			}
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
	case "L":
		fe.openFocusedLogs()
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
	if isEscapeKey(keyStr) {
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
			_, err = dagger.Ref[*dagger.Container](fe.dag, dagger.ID(id)).Terminal().Sync(fe.runCtx)
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
			_, err = dagger.Ref[*dagger.Directory](fe.dag, dagger.ID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Service":
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = dagger.Ref[*dagger.Service](fe.dag, dagger.ID(id)).Terminal().Sync(fe.runCtx)
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
	fe.recalculateViewLocked()
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
	old := fe.window
	fe.window = msg
	fe.contentWidth = msg.Width
	fe.logs.SetWidth(fe.contentWidth)
	if old != msg {
		fe.updateTestViews()
	}
	if fe.textInput != nil {
		fe.textInput.Update()
	}
}

func (fe *frontendPretty) setExpanded(id dagui.SpanID, expanded bool) {
	if fe.SpanExpanded == nil {
		fe.SpanExpanded = make(map[dagui.SpanID]bool)
	}
	fe.SpanExpanded[id] = expanded
	if expanded {
		// Lazily pull this span's logs and children the first time it's opened.
		fe.requestLogs(id)
		fe.requestSpans(id)
	}
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
func (fe *frontendPretty) renderRowContentRest(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, statusHost statusIconHost, focused bool) {
	span := row.Span

	if span.Message == "" && // messages are displayed in renderStep
		(row.Expanded || row.Span.LLMTool != "") {
		fe.renderStepLogs(out, r, row, prefix, focused)
	} else if (row.Span.RollUpLogs || fe.shell != nil) && row.Depth == 0 && !row.Expanded && !fe.shouldRenderInlineTests(row) {
		// in shell mode, we print top-level command logs unindented, like shells
		// usually does
		if logs := fe.logs.Logs[row.Span.ID]; logs != nil && logs.UsedHeight() > 0 {
			if fe.shell != nil {
				unindent := *row
				unindent.Depth = -1
				fe.renderLogs(out, r, &unindent, logs, logs.UsedHeight(), prefix, false)
			} else if row.Span.RollUpLogs && row.IsRunningOrChildRunning {
				// Only show rolled-up logs while the span is running.
				fe.renderStepLogs(out, r, row, prefix, focused)
			}
		}
	}
	if len(span.ProgressSpans.Order) > 0 && (!row.Expanded || !row.HasChildren) {
		fe.renderProgressRollup(ctx, out, r, row, prefix, statusHost)
	}
	if len(row.Span.ErrorOrigins.Order) > 0 && (!row.Expanded || !row.HasChildren) {
		// Filter self-references and causes already rendered elsewhere in this
		// trace: a span propagated as its own error origin should never be
		// rendered as the cause of itself, and a cause already shown as a
		// primary row doesn't need a redundant "↳ ..." block here.
		origins := make([]*dagui.Span, 0, len(row.Span.ErrorOrigins.Order))
		for _, cause := range row.Span.ErrorOrigins.Order {
			if cause.ID == row.Span.ID {
				continue
			}
			if fe.claims.hasError(cause.ID) {
				continue
			}
			origins = append(origins, cause)
		}
		sortErrorOrigins(origins)
		multi := len(origins) > 1
		for _, cause := range origins {
			if multi {
				var gapBuf strings.Builder
				gapOut := NewOutput(&gapBuf, termenv.WithProfile(fe.profile))
				r.fancyIndent(gapOut, row, false, false)
				fmt.Fprint(&gapBuf, prefix)
				fmt.Fprintln(out, strings.TrimRight(gapBuf.String(), " "))
			}
			fe.renderErrorCause(ctx, out, r, row, prefix, cause, statusHost)
		}
		if len(origins) == 0 {
			fe.renderStepError(out, r, row, prefix)
		}
	} else {
		fe.renderStepError(out, r, row, prefix)
	}
	fe.renderDebug(out, row.Span, prefix+Block25+" ", false)
}

func sortErrorOrigins(origins []*dagui.Span) {
	// Error origins can be linked before their referenced spans have arrived.
	// In that case their StartTime is still zero when they are inserted into the
	// SpanSet, and mutating StartTime later won't re-sort the set. Sort a copy at
	// render time using the current span data so final output is deterministic.
	sort.SliceStable(origins, func(i, j int) bool {
		return compareErrorOrigins(origins[i], origins[j]) < 0
	})
}

func compareErrorOrigins(a, b *dagui.Span) int {
	if a == b {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if !a.StartTime.IsZero() && !b.StartTime.IsZero() && !a.StartTime.Equal(b.StartTime) {
		if a.StartTime.Before(b.StartTime) {
			return -1
		}
		return 1
	}
	if a.StartTime.IsZero() != b.StartTime.IsZero() {
		if a.StartTime.IsZero() {
			return 1
		}
		return -1
	}
	if c := strings.Compare(spanPath(a), spanPath(b)); c != 0 {
		return c
	}
	return strings.Compare(a.ID.String(), b.ID.String())
}

func spanPath(span *dagui.Span) string {
	var parts []string
	for cur := span; cur != nil; cur = cur.ParentSpan {
		parts = append(parts, cur.Name)
	}
	slices.Reverse(parts)
	return strings.Join(parts, "\x00")
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
	if fe.claims.hasLog(row.Span.ID) {
		return false
	}
	limit := fe.window.Height / 3
	if row.Span.LLMTool != "" && !row.Expanded {
		limit = llmLogsLastLines
	}
	if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
		return fe.renderLogs(out, r, row, logs, limit, prefix, focused)
	}
	return false
}

// transferKinds maps the leading verb of a transfer span's name — the
// engine's progress emitters all follow "<verb> <subject>", e.g.
// "pulling nginx:latest", "fetching <url>" — to singular/plural nouns for
// the merged summary line.
var transferKinds = map[string][2]string{
	"pulling":     {"pull", "pulls"},
	"pushing":     {"push", "pushes"},
	"unpacking":   {"unpack", "unpacks"},
	"fetching":    {"fetch", "fetches"},
	"uploading":   {"upload", "uploads"},
	"downloading": {"download", "downloads"},
}

// transferSummary counts the given transfer spans by kind in order of
// first appearance, e.g. "3 pulls, 38 fetches, 1 upload".
func transferSummary(srcs []*dagui.Span) string {
	counts := map[[2]string]int{}
	var order [][2]string
	for _, src := range srcs {
		verb, _, _ := strings.Cut(src.Name, " ")
		kind, ok := transferKinds[verb]
		if !ok {
			kind = [2]string{"transfer", "transfers"}
		}
		if counts[kind] == 0 {
			order = append(order, kind)
		}
		counts[kind]++
	}
	parts := make([]string, len(order))
	for i, kind := range order {
		n := counts[kind]
		noun := kind[0]
		if n != 1 {
			noun = kind[1]
		}
		parts[i] = fmt.Sprintf("%d %s", n, noun)
	}
	return strings.Join(parts, ", ")
}

// renderMergedProgressRow folds completed transfers into one summary line
// beneath the given row: a count by kind and the merged wall-clock
// duration of their activity. The interval union means parallel transfers
// don't double-count, and byte totals are deliberately omitted — fetch and
// unpack read the same bytes, so summing would double the apparent size.
// The "p" keybind expands the fold into individual rows.
func (fe *frontendPretty) renderMergedProgressRow(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, srcs []*dagui.Span) {
	fmt.Fprint(out, prefix)
	r.fancyIndent(out, row, false, false)
	// indent past the parent's icon column so the line reads as its detail
	fmt.Fprint(out, "  ")
	fmt.Fprint(out, out.String(IconSuccess).Foreground(termenv.ANSIGreen))
	fmt.Fprint(out, out.String(" "+transferSummary(srcs)).Faint())
	var activity dagui.Activity
	for _, src := range srcs {
		activity.Add(src)
	}
	fmt.Fprint(out, out.String(" "+dagui.FormatDuration(activity.Duration(r.now))).Faint())
	if fe.FocusedSpan == row.Span.ID && !fe.reportOnly && !fe.finalRender {
		// discoverability hint, like the error origins' "r jump ↴"
		color := termenv.ANSIBrightBlack
		if time.Since(fe.pressedKeyAt) < keypressDuration {
			color = termenv.ANSIWhite
		}
		fmt.Fprintf(out, " %s %s",
			out.String("p").Foreground(color).Bold(),
			out.String("expand").Foreground(color),
		)
	}
	fmt.Fprintln(out)
}

// foldableProgressSource reports whether a transfer belongs in the merged
// completed-transfer summary: finished, and neither failed nor canceled —
// those must stay visible as their own rows rather than disappear into a
// green checkmark.
func foldableProgressSource(src *dagui.Span) bool {
	return !src.IsRunningOrEffectsRunning() &&
		!src.IsFailedOrCausedFailure() &&
		!src.IsCanceled()
}

// renderProgressRollup surfaces a collapsed row's descendant transfers,
// like error origins: when the row is expanded they render in their
// natural tree position instead (carrying progress reveals an encapsulated
// span). In-flight, failed, and canceled transfers each get their own row;
// successfully completed ones always fold into a single merged summary
// line — a module fetching dozens of packages would otherwise drown the
// view. The "p" keybind (progressExpanded), debug, and high verbosity
// expand the fold into individual rows.
func (fe *frontendPretty) renderProgressRollup(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, statusHost statusIconHost) {
	span := row.Span
	showAll := fe.progressExpanded[span.ID] || r.Debug ||
		r.Verbosity >= dagui.ShowSpammyVerbosity
	var done []*dagui.Span
	for _, src := range span.ProgressSpans.Order {
		if src == span || !src.HasProgress() {
			continue
		}
		if !showAll && foldableProgressSource(src) {
			done = append(done, src)
			continue
		}
		fe.renderProgressSpanRow(ctx, out, r, row, prefix, src, statusHost)
	}
	if len(done) == 1 {
		// a single completed transfer is already its own summary
		fe.renderProgressSpanRow(ctx, out, r, row, prefix, done[0], statusHost)
	} else if len(done) > 1 {
		fe.renderMergedProgressRow(out, r, row, prefix, done)
	}
}

func progressToggleHelp(expanded bool) string {
	if expanded {
		return "collapse transfers"
	}
	return "expand transfers"
}

// spanHasProgressRollup reports whether the span currently folds completed
// descendant transfers into a merged line (or has it expanded), i.e.
// whether the "p" toggle applies to it.
func (fe *frontendPretty) spanHasProgressRollup(id dagui.SpanID) bool {
	span := fe.db.Spans.Map[id]
	if span == nil {
		return false
	}
	if fe.rows != nil {
		if row := fe.rows.BySpan[id]; row != nil && row.Expanded && row.HasChildren {
			// the roll-up only renders beneath collapsed rows
			return false
		}
	}
	var done int
	for _, src := range span.ProgressSpans.Order {
		if src == span || !src.HasProgress() || !foldableProgressSource(src) {
			continue
		}
		done++
		if done > 1 {
			return true
		}
	}
	return false
}

// renderProgressSpanRow renders one hidden/collapsed descendant's streaming
// progress as a labeled bar-first line beneath the given row.
func (fe *frontendPretty) renderProgressSpanRow(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, src *dagui.Span, statusHost statusIconHost) {
	fmt.Fprint(out, prefix)
	r.fancyIndent(out, row, false, false)
	// indent past the parent's icon column so the bar reads as its detail
	fmt.Fprint(out, "  ")
	syntheticRow := &dagui.TraceRow{
		Span:     src,
		Depth:    row.Depth,
		Expanded: true,
	}
	fe.renderStepTitle(ctx, out, r, syntheticRow, prefix, statusHost, false, false)
	fmt.Fprintln(out)
}

// checkRootCauses returns the failing origin span(s) for a zoom target -- the
// span-derived equivalent of the summary's "root cause". It prefers the
// ErrorOrigins already propagated onto the span via causal links, and otherwise
// walks the subtree for failed leaves (a failed span with no failed child).
func (fe *frontendPretty) checkRootCauses(root *dagui.Span) []*dagui.Span {
	var origins []*dagui.Span
	seen := map[dagui.SpanID]bool{}
	add := func(s *dagui.Span) {
		if s == nil || seen[s.ID] {
			return
		}
		seen[s.ID] = true
		origins = append(origins, s)
	}
	for _, o := range root.ErrorOrigins.Order {
		add(o)
	}
	if len(origins) > 0 {
		return origins
	}
	var walk func(s *dagui.Span)
	walk = func(s *dagui.Span) {
		if s.IsFailed() {
			for _, o := range s.ErrorOrigins.Order {
				add(o)
			}
			failedChild := false
			for _, c := range s.ChildSpans.Order {
				if c.IsFailedOrCausedFailure() {
					failedChild = true
				}
			}
			if !failedChild && len(s.ErrorOrigins.Order) == 0 {
				add(s)
			}
		}
		for _, c := range s.ChildSpans.Order {
			walk(c)
		}
	}
	walk(root)
	return origins
}

// renderRootCauseSection renders the zoom target's root-cause origin span(s)
// with the same `› parent context › failed span` breadcrumb, logs, and error
// the live tree uses. It reuses renderErrorCause, whose logs.View() preserves
// the user program's own ANSI colour (UI chrome is handled by the agent/ASCII
// profile elsewhere -- we must not strip the user's output here).
func (fe *frontendPretty) renderRootCauseSection(ctx tuist.Context, r *renderer) []string {
	zoomSpan := fe.db.Spans.Map[fe.ZoomedSpan]
	if zoomSpan == nil {
		return nil
	}
	origins := fe.checkRootCauses(zoomSpan)
	if len(origins) == 0 {
		return nil
	}
	zoomRow := &dagui.TraceRow{Span: zoomSpan, Expanded: true}
	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	rendered := false
	for _, origin := range origins {
		if !origin.Received {
			// Incremental --full may not have loaded the origin span (or its
			// logs) yet; skip rather than render an empty stub.
			continue
		}
		fe.renderErrorCause(ctx, out, r, zoomRow, "", origin, fe)
		rendered = true
	}
	if !rendered {
		return nil
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

func (fe *frontendPretty) renderErrorCause(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, rootCause *dagui.Span, statusHost statusIconHost) {
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
			fe.renderStepTitle(ctx, noColorOut, r, p, prefix+indent, statusHost, false, true)
			fmt.Fprintf(noColorOut, " › ")
		}
		fmt.Fprint(out, out.String(context.String()).Foreground(termenv.ANSIBrightBlack).Faint())
		fmt.Fprintln(out)
	}
	r.fancyIndent(out, row, false, false)
	if !fe.finalRender {
		fmt.Fprint(out, "  ")
	}
	fe.renderStepTitle(ctx, out, r, rootCauseRow, prefix+indent, statusHost, false, false)
	fmt.Fprintln(out)
	if logs := fe.logs.Logs[rootCauseRow.Span.ID]; logs != nil && !fe.claims.hasLog(rootCauseRow.Span.ID) {
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

	fe.claims.claimError(rootCause)
}

func (fe *frontendPretty) hasShownRootError() bool {
	return fe.claims.hasRootError(fe.err)
}

func (fe *frontendPretty) renderStepError(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	if len(row.Span.ErrorOrigins.Order) > 0 {
		// span's error originated elsewhere; don't repeat the message, the ERROR status
		// links to its origin instead
		return
	}
	fe.claims.claimError(row.Span)
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

func (fe *frontendPretty) renderStepTitle(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, statusHost statusIconHost, focused bool, abridged bool) error {
	span := row.Span
	chained := row.Chained
	depth := row.Depth

	// Progress rows (e.g. "pulling nginx:latest") render their name faintly,
	// as a label for the trailing bar rather than a step of its own.
	progressRow := span.HasProgress() && span.Call() == nil && span.Message == ""

	if !abridged && row.Span.LLMRole == "" {
		fe.renderStatusIcon(ctx, out, row, statusHost)
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
		if fe.renderStepLogs(out, r, row, prefix, focused) {
			if span.LLMRole == telemetry.LLMRoleUser {
				// Bail early if we printed a user message span; these don't have any
				// further information to show. Duration is always 0, metrics are empty,
				// status is always OK.
				return nil
			}
			r.fancyIndent(out, row, false, false)
			bar := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
			if focused {
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
		if progressRow {
			// keep the focus on the bar; the name is a label
			fmt.Fprint(out, out.String(span.Name).Faint())
		} else if err := r.renderSpan(out, span, span.Name); err != nil {
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

		// Render streaming progress (e.g. image layer downloads)
		if bars := fe.renderProgressBars(out, span); bars != "" {
			fmt.Fprint(out, " ")
			fmt.Fprint(out, bars)
		}

		fe.renderStatus(out, span)
		r.renderMetrics(out, span)

		summary := map[string]int{}
		for effect := range span.EffectSpans {
			if effect.Passthrough {
				// Don't show spans which are aggressively hidden.
				continue
			}
			icon, isInteresting := fe.statusIcon(ctx, statusHost, effect)
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

func (fe *frontendPretty) renderStep(ctx tuist.Context, out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, statusHost statusIconHost, focused bool) error {
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
		fe.renderToggler(out, row, focused)
		fmt.Fprint(out, " ")
	}

	if err := fe.renderStepTitle(ctx, out, r, row, prefix, statusHost, focused, false); err != nil {
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

	// The braille rollup is a visual density cue; an agent reading the output as
	// text gets nothing from it but noise, so skip it entirely.
	if RunningInAgent() {
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

// maxProgressItems caps how many per-item cells a single row may render
// before summarizing the remainder.
const maxProgressItems = 40

// progressTrackWidth is the fixed cell width of a single-item (1-D)
// progress track.
const progressTrackWidth = 12

// verticalEighths maps a fill level (1-8) to a block element rising from
// the bottom of the cell. Progress uses block elements rather than braille
// so the braille glyphs keep one meaning in the UI: span status (the
// spinner and roll-up dots).
var verticalEighths = []rune{
	' ', // 0: empty (unused; untouched items render level 1)
	'▁', // 1: ▁
	'▂', // 2: ▂
	'▃', // 3: ▃
	'▄', // 4: ▄
	'▅', // 5: ▅
	'▆', // 6: ▆
	'▇', // 7: ▇
	'█', // 8: █
}

// horizontalEighths maps a fill level (1-8) to a block element extending
// from the left of the cell.
var horizontalEighths = []rune{
	' ', // 0: empty
	'▏', // 1: ▏
	'▎', // 2: ▎
	'▍', // 3: ▍
	'▌', // 4: ▌
	'▋', // 5: ▋
	'▊', // 6: ▊
	'▉', // 7: ▉
	'█', // 8: █
}

// renderProgressBars renders the span's own streaming-progress state, plus
// an aggregate byte count. Multiple items render 2-D: one cell per item,
// filling bottom-up like a bar chart. A single item renders 1-D: a fixed
// track filling left-to-right, or just a climbing count when the total is
// unknown (e.g. a filesync's streaming diff). Descendants' progress is
// never merged in: each progress-carrying span renders as its own labeled
// row (revealed in the tree, or rolled up under a collapsed ancestor).
func (fe *frontendPretty) renderProgressBars(out TermOutput, span *dagui.Span) string {
	if !span.HasProgress() {
		return ""
	}
	items := span.Progress.Order

	var sb strings.Builder
	switch {
	case len(items) == 1 && items[0].Total > 0:
		fe.renderProgressTrack(out, &sb, items[0])
	case len(items) == 1:
		// indeterminate: only the climbing count below
	default:
		fe.renderProgressCells(out, &sb, items)
	}

	current, total := span.Progress.Totals()
	if unit := items[0].Unit; unit != "" && current > 0 {
		var summary string
		if unit == "bytes" {
			summary = humanizeBytes(current)
			if current < total {
				summary += "/" + humanizeBytes(total)
			}
		} else {
			summary = strconv.FormatInt(current, 10)
			if current < total {
				summary += "/" + strconv.FormatInt(total, 10)
			}
			summary += " " + unit
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(out.String(summary).Faint().String())
	}
	return sb.String()
}

// renderProgressCells renders one bottom-up filling cell per item.
func (fe *frontendPretty) renderProgressCells(out TermOutput, sb *strings.Builder, items []*dagui.ProgressItem) {
	shown := items
	if len(shown) > maxProgressItems {
		shown = shown[:maxProgressItems]
	}
	for _, item := range shown {
		level := 1
		if item.Total > 0 {
			level = int((item.Current*8 + item.Total - 1) / item.Total) // ceil
			level = max(min(level, 8), 1)
		}
		color := termenv.ANSIYellow
		switch {
		case item.Complete():
			color = termenv.ANSIGreen
		case item.Current == 0:
			color = termenv.ANSIBrightBlack
		}
		sb.WriteString(out.String(string(verticalEighths[level])).Foreground(color).Faint().String())
	}
	if rest := len(items) - len(shown); rest > 0 {
		sb.WriteString(out.String(fmt.Sprintf("+%d", rest)).Faint().String())
	}
}

// renderProgressTrack renders a single item as a fixed-width left-to-right
// track with eighth-cell resolution.
func (fe *frontendPretty) renderProgressTrack(out TermOutput, sb *strings.Builder, item *dagui.ProgressItem) {
	eighths := int(item.Current * progressTrackWidth * 8 / item.Total)
	eighths = max(min(eighths, progressTrackWidth*8), 0)
	full, rem := eighths/8, eighths%8
	color := termenv.ANSIYellow
	if item.Complete() {
		color = termenv.ANSIGreen
	}
	if full > 0 {
		sb.WriteString(out.String(strings.Repeat(string(verticalEighths[8]), full)).Foreground(color).Faint().String())
	}
	if rem > 0 {
		sb.WriteString(out.String(string(horizontalEighths[rem])).Foreground(color).Faint().String())
	}
	if empty := progressTrackWidth - full - min(rem, 1); empty > 0 {
		sb.WriteString(out.String(strings.Repeat("░", empty)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
}

// statusIcon returns an icon indicating the span's status, and a bool
// indicating whether it's interesting enough to reveal at a summary level.
func (fe *frontendPretty) statusIcon(ctx tuist.Context, host statusIconHost, span *dagui.Span) (string, bool) {
	if span.IsRunningOrEffectsRunning() {
		if host == nil {
			return DotHalf, true
		}
		return host.RenderChildInline(ctx, host.spinnerForStatus(span.ID)), true
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
	if row.HasChildren || row.Span.ChildCount > 0 || row.Span.HasLogs {
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

func (fe *frontendPretty) renderStatusIcon(ctx tuist.Context, out TermOutput, row *dagui.TraceRow, host statusIconHost) {
	// Then render the status icon (without focus highlighting)
	icon, _ := fe.statusIcon(ctx, host, row.Span)
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

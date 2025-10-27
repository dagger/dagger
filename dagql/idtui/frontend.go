package idtui

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
	"github.com/iancoleman/strcase"
	"github.com/muesli/termenv"
	"github.com/vito/bubbline/editline"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/term"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/cleanups"
)

type (
	cmdContextKey struct{}
	cmdContext    struct {
		printTraceLink bool
	}
)

// WithPrintTraceLink is used for enabling printing the trace link
// for the selected commands.
func WithPrintTraceLink(ctx context.Context, printTraceLink bool) context.Context {
	return context.WithValue(ctx, cmdContextKey{}, &cmdContext{printTraceLink: printTraceLink})
}

func FromCmdContext(ctx context.Context) (*cmdContext, bool) {
	value, ok := ctx.Value(cmdContextKey{}).(*cmdContext)
	if ok {
		return value, true
	}

	return nil, false
}

var SkipLoggedOutTraceMsgEnvs = []string{
	"DAGGER_NO_NAG",

	// old envs kept for backwards compat
	"NOTHANKS", "SHUTUP", "GOAWAY", "STOPIT",
}

// NOTE: keep this to one line, and 80 characters max
var loggedOutTraceMsg = fmt.Sprintf("Setup tracing at %%s. To hide set %s=1", SkipLoggedOutTraceMsgEnvs[0])

//go:generate go run github.com/matryer/moq -out frontend_mock.go . Frontend

type Frontend interface {
	// Run starts a frontend, and runs the target function.
	Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error

	// Opts returns the opts of the currently running frontend.
	Opts() *dagui.FrontendOpts
	SetVerbosity(n int)

	// SetPrimary tells the frontend which span should be treated like the focal
	// point of the command. Its output will be displayed at the end, and its
	// children will be promoted to the "top-level" of the TUI.
	SetPrimary(spanID dagui.SpanID)
	Background(cmd tea.ExecCommand, raw bool) error
	// RevealAllSpans tells the frontend to show all spans, not just
	// the spans beneath the primary span.
	RevealAllSpans()

	// Can consume otel spans, logs and metrics.
	SpanExporter() sdktrace.SpanExporter
	LogExporter() sdklog.Exporter
	MetricExporter() sdkmetric.Exporter

	// SetCloudURL is called after the CLI checks auth and sets the cloud URL.
	SetCloudURL(ctx context.Context, url string, msg string, logged bool)

	// SetClient is called to notify the frontend of a created dagger client.
	// This can be used to make requests to the engine for more information.
	SetClient(*dagger.Client)

	// Shell is called when the CLI enters interactive mode.
	Shell(ctx context.Context, handler ShellHandler)

	// Populate the sidebar with content.
	SetSidebarContent(SidebarSection)

	prompt.PromptHandler
}

type SidebarSection struct {
	// A heading to show for the content, if any. If empty, the content will be
	// placed in the topmost portion of the sidebar.
	Title string
	// The content to display.
	Content string
	// The content to display, for a given width.
	ContentFunc func(int) string
	// Keymap associated with this section
	KeyMap []key.Binding
}

func (sec SidebarSection) Body(width int) string {
	if sec.Content != "" {
		return sec.Content
	}
	if sec.ContentFunc != nil {
		return sec.ContentFunc(width)
	}
	return ""
}

// ShellHandler defines the interface for handling shell interactions
type ShellHandler interface {
	// Handle processes shell input
	Handle(ctx context.Context, input string) error

	// AutoComplete provides shell auto-completion functionality
	AutoComplete(entireInput [][]rune, line, col int) (string, editline.Completions)

	// IsComplete determines if the current input is a complete command
	IsComplete(entireInput [][]rune, line int, col int) bool

	// Prompt generates the shell prompt string
	Prompt(ctx context.Context, out TermOutput, fg termenv.Color) (string, tea.Cmd)

	// Keys returns the keys that will be displayed when the input is focused
	KeyBindings() []key.Binding

	// ReactToInput allows reacting to live input before it's submitted
	ReactToInput(ctx context.Context, msg tea.KeyMsg) tea.Cmd

	// Shell handlers can man-in-the-middle history items to preserve per-entry modes etc.
	editline.HistoryEncoder
}

type Dump struct {
	Newline string
	Prefix  string
}

func (d *Dump) DumpID(out *termenv.Output, id *call.ID) error {
	if id.Receiver() != nil {
		if err := d.DumpID(out, id.Receiver()); err != nil {
			return err
		}
	}
	dag, err := id.ToProto()
	if err != nil {
		return err
	}

	db := dagui.NewDB()
	maps.Copy(db.Calls, dag.CallsByDigest)
	r := newRenderer(db, -1, dagui.FrontendOpts{})
	if d.Newline != "" {
		r.newline = d.Newline
	}
	err = r.renderCall(out, nil, id.Call(), d.Prefix, true, 0, false, nil)
	fmt.Fprint(out, r.newline)
	return err
}

type renderer struct {
	dagui.FrontendOpts

	now           time.Time
	newline       string
	db            *dagui.DB
	maxLiteralLen int
	rendering     map[string]bool
}

func newRenderer(db *dagui.DB, maxLiteralLen int, fe dagui.FrontendOpts) *renderer {
	return &renderer{
		FrontendOpts:  fe,
		now:           time.Now(),
		db:            db,
		maxLiteralLen: maxLiteralLen,
		rendering:     map[string]bool{},
		newline:       "\n",
	}
}

const (
	kwColor     = termenv.ANSICyan
	faintColor  = termenv.ANSIBrightBlack
	moduleColor = termenv.ANSIMagenta
)

func (r *renderer) indent(out TermOutput, depth int) {
	fmt.Fprint(out, out.String(strings.Repeat(VertDash3+" ", depth)).
		Foreground(termenv.ANSIBrightBlack).
		Faint())
}

func (r *renderer) fancyIndent(out TermOutput, row *dagui.TraceRow, selfBar, selfHoriz bool) {
	// like indent, but render tree-style prefixes with status-colored symbols
	// ◐ for running, ● for completed/successful, ◯ for pending/failed
	// ├─ for intermediate children, └─ for last child

	// Collect parent spans and their tree context from root to current
	var parentRows []*dagui.TraceRow
	current := row.Parent
	for current != nil {
		parentRows = append(parentRows, current)
		current = current.Parent
	}

	// Print tree symbols from root to current (reverse order)
	for i := len(parentRows) - 1; i >= 0; i-- {
		parent := parentRows[i]
		var nextChild *dagui.TraceRow
		if i > 0 {
			nextChild = parentRows[i-1]
		} else {
			nextChild = row
		}
		span := parent.Span
		color := restrainedStatusColor(span)

		var prefix string
		if i == 0 && selfHoriz && !row.Span.Reveal && len(parent.Span.RevealedSpans.Order) == 0 {
			if row.Next != nil {
				prefix = VertRightBar + HorizHalfLeftBar
			} else {
				prefix = CornerBottomLeft + HorizHalfLeftBar
			}
		} else if nextChild.Next != nil && !row.Span.Reveal && len(parent.Span.RevealedSpans.Order) == 0 {
			prefix = VertBar + " "
		} else {
			prefix = "  "
		}

		fmt.Fprint(out, out.String(prefix).
			Foreground(color).
			Faint())
	}

	if selfBar {
		span := row.Span
		color := restrainedStatusColor(span)

		var symbol string
		if row.ShowingChildren && !row.Span.Reveal {
			symbol = VertBar
		} else {
			symbol = " "
		}
		fmt.Fprint(out, out.String(symbol+" ").
			Foreground(color).
			Faint())
	}
}

func (r *renderer) renderIDBase(out TermOutput, call *callpbv1.Call) {
	typeName := call.Type.ToAST().Name()
	parent := out.String(typeName)
	if call.Module != nil {
		parent = parent.Foreground(moduleColor)
	}
	fmt.Fprint(out, parent.String())
	if r.Verbosity > dagui.ShowDigestsVerbosity && call.ReceiverDigest != "" {
		fmt.Fprint(out, out.String(fmt.Sprintf("@%s", call.ReceiverDigest)).Foreground(faintColor))
	}
}

func (r *renderer) renderCall(
	out TermOutput,
	span *dagui.Span,
	call *callpbv1.Call,
	prefix string,
	chained bool,
	depth int,
	internal bool,
	row *dagui.TraceRow,
) error {
	if r.rendering[call.Digest] {
		fmt.Fprintf(out, "<cycle detected: %s>", call.Digest)
		return nil
	}
	r.rendering[call.Digest] = true
	defer func() { delete(r.rendering, call.Digest) }()

	if call.ReceiverDigest != "" {
		if !chained {
			r.renderIDBase(out, r.db.MustCall(call.ReceiverDigest))
		}
		fmt.Fprint(out, out.String("."))
	}

	fmt.Fprint(out, out.String(call.Field).Bold())

	if len(call.Args) > 0 {
		fmt.Fprint(out, out.String("("))
		var needIndent bool
		for _, arg := range call.Args {
			if arg.GetValue().GetCallDigest() != "" {
				needIndent = true
				break
			}
			if r.maxLiteralLen > 0 && r.renderedLen(arg.GetValue()) > r.maxLiteralLen {
				needIndent = true
				break
			}
		}
		if needIndent {
			fmt.Fprint(out, r.newline)
			depth++
			depth++
			for _, arg := range call.Args {
				fmt.Fprint(out, prefix)
				indentLevel := depth
				if row != nil {
					r.fancyIndent(out, row, true, false)
					indentLevel -= row.Depth
					indentLevel -= 1
				}
				r.indent(out, indentLevel)
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.GetName())
				val := arg.GetValue()
				fmt.Fprint(out, out.String(" "))
				if argDig := val.GetCallDigest(); argDig != "" {
					forceSimplify := false
					argSpan := r.db.MostInterestingSpan(argDig)
					if argSpan != nil {
						forceSimplify = argSpan.Internal && !internal // only for the first internal call (not it's children)
						internal = internal || argSpan.Internal
						if span == nil {
							argSpan = nil
						}
					}
					argCall := r.db.Simplify(r.db.MustCall(argDig), forceSimplify)
					if err := r.renderCall(out, argSpan, argCall, prefix, false, depth-1, internal, row); err != nil {
						return err
					}
				} else {
					r.renderLiteral(out, arg.GetValue())
				}
				fmt.Fprint(out, r.newline)
			}
			depth--
			fmt.Fprint(out, prefix)
			indentLevel := depth
			if row != nil {
				r.fancyIndent(out, row, true, false)
				indentLevel -= row.Depth
				indentLevel -= 1
			}
			r.indent(out, indentLevel)
			depth-- //nolint:ineffassign
		} else {
			for i, arg := range call.Args {
				if i > 0 {
					fmt.Fprint(out, out.String(", "))
				}
				fmt.Fprintf(out, out.String("%s: ").Foreground(kwColor).String(), arg.GetName())
				r.renderLiteral(out, arg.GetValue())
			}
		}
		fmt.Fprint(out, out.String(")"))
	}

	if call.Type != nil {
		typeStr := out.String(": " + call.Type.ToAST().String()).Faint()
		fmt.Fprint(out, typeStr)
	}

	if r.Verbosity > dagui.ShowDigestsVerbosity {
		fmt.Fprint(out, out.String(fmt.Sprintf(" = %s", call.Digest)).Foreground(faintColor))
	}

	return nil
}

func (r *renderer) renderedLen(lit *callpbv1.Literal) int {
	var buf strings.Builder
	r.renderLiteral(
		termenv.NewOutput(&buf,
			// no colors, so we can more accurately estimate size without ANSI sequences in the way
			termenv.WithProfile(termenv.Ascii)),
		lit)
	return buf.Len()
}

func (r *renderer) renderSpan(
	out TermOutput,
	span *dagui.Span,
	name string,
) error {
	if name == "" {
		return nil
	}

	var contentType string
	if span != nil {
		contentType = span.ContentType
		if span.LLMTool != "" {
			if span.LLMToolServer != "" {
				fmt.Fprint(out,
					out.String(strcase.ToLowerCamel(span.LLMToolServer)).
						Foreground(termenv.ANSIBrightMagenta))
				fmt.Fprint(out, " ")
			}
			fmt.Fprint(out, out.String(strcase.ToCamel(span.LLMTool)).Bold())
			if len(span.LLMToolArgValues) > 0 {
				// for now, only print the first arg, the rest are likely to be noisy.
				fmt.Fprint(out, "(", span.LLMToolArgValues[0], ")")
			}
			return nil
		}
	}

	switch contentType {
	case "text/x-shellscript":
		quick.Highlight(out, name, "bash", "terminal16", highlightStyle())
	case "text/markdown":
		quick.Highlight(out, name, "markdown", "terminal16", highlightStyle())
	default:
		label := out.String(name)
		var isEffect bool
		if span != nil {
			for range span.CausalSpans {
				isEffect = true
				break
			}
		}
		if isEffect {
			label = label.Italic()
		}
		fmt.Fprint(out, label)
	}

	return nil
}

func (r *renderer) renderLiteral(out TermOutput, lit *callpbv1.Literal) {
	switch val := lit.GetValue().(type) {
	case *callpbv1.Literal_Bool:
		fmt.Fprint(out, out.String(fmt.Sprintf("%v", val.Bool)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Int:
		fmt.Fprint(out, out.String(fmt.Sprintf("%d", val.Int)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Float:
		fmt.Fprint(out, out.String(fmt.Sprintf("%f", val.Float)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_String_:
		fmt.Fprint(out, out.String(fmt.Sprintf("%q", val.String_)).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_CallDigest:
		fmt.Fprint(out, out.String(val.CallDigest).Foreground(termenv.ANSIMagenta))
	case *callpbv1.Literal_Enum:
		fmt.Fprint(out, out.String(val.Enum).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_Null:
		fmt.Fprint(out, out.String("null").Foreground(termenv.ANSIBrightBlack))
	case *callpbv1.Literal_List:
		fmt.Fprint(out, out.String("["))
		for i, item := range val.List.GetValues() {
			if i > 0 {
				fmt.Fprint(out, out.String(", "))
			}
			r.renderLiteral(out, item)
		}
		fmt.Fprint(out, out.String("]"))
	case *callpbv1.Literal_Object:
		fmt.Fprint(out, out.String("{"))
		for i, item := range val.Object.GetValues() {
			if i > 0 {
				fmt.Fprint(out, out.String(", "))
			}
			fmt.Fprintf(out, out.String("%s: ").String(), item.GetName())
			r.renderLiteral(out, item.GetValue())
		}
		fmt.Fprint(out, out.String("}"))
	}
}

func statusColor(span *dagui.Span) termenv.Color {
	switch {
	case span.IsRunningOrEffectsRunning():
		return termenv.ANSIYellow
	case span.IsCached():
		return termenv.ANSIBlue
	case span.IsCanceled():
		return termenv.ANSIBrightBlack
	case span.IsFailedOrCausedFailure():
		return termenv.ANSIRed
	case span.IsPending():
		return termenv.ANSIBrightBlack
	default:
		return termenv.ANSIGreen
	}
}

func restrainedStatusColor(span *dagui.Span) termenv.Color {
	switch {
	case span.IsRunningOrEffectsRunning():
		return termenv.ANSIYellow
	case span.IsFailedOrCausedFailure():
		return termenv.ANSIRed
	default:
		return termenv.ANSIBrightBlack
	}
}

func (r *renderer) renderDuration(out TermOutput, span *dagui.Span, space bool) {
	if space {
		fmt.Fprint(out, out.String(" "))
	}
	duration := out.String(dagui.FormatDuration(span.Activity.Duration(r.now)))
	if span.IsRunningOrEffectsRunning() {
		duration = duration.Foreground(termenv.ANSIYellow)
	} else {
		duration = duration.Faint()
	}
	fmt.Fprint(out, duration)
}

var metricsVerbosity = map[string]int{
	telemetry.IOStatDiskReadBytes:      3,
	telemetry.IOStatDiskWriteBytes:     3,
	telemetry.IOStatPressureSomeTotal:  3,
	telemetry.CPUStatPressureSomeTotal: 3,
	telemetry.CPUStatPressureFullTotal: 3,
	telemetry.MemoryCurrentBytes:       3,
	telemetry.MemoryPeakBytes:          3,
	telemetry.NetstatRxBytes:           3,
	telemetry.NetstatTxBytes:           3,
	telemetry.NetstatRxDropped:         3,
	telemetry.NetstatTxDropped:         3,
	telemetry.NetstatRxPackets:         3,
	telemetry.NetstatTxPackets:         3,
	telemetry.LLMInputTokens:           1,
	telemetry.LLMOutputTokens:          1,
}

func (r renderer) renderMetrics(out TermOutput, span *dagui.Span) {
	if span.CallDigest != "" {
		if metricsByName := r.db.MetricsByCall[span.CallDigest]; metricsByName != nil {
			// IO Stats
			r.renderMetric(out, metricsByName, telemetry.IOStatDiskReadBytes, "Disk Read", humanizeBytes)
			r.renderMetric(out, metricsByName, telemetry.IOStatDiskWriteBytes, "Disk Write", humanizeBytes)
			r.renderMetricIfNonzero(out, metricsByName, telemetry.IOStatPressureSomeTotal, "IO Pressure", durationString)

			// CPU Stats
			r.renderMetricIfNonzero(out, metricsByName, telemetry.CPUStatPressureSomeTotal, "CPU Pressure (some)", durationString)
			r.renderMetricIfNonzero(out, metricsByName, telemetry.CPUStatPressureFullTotal, "CPU Pressure (full)", durationString)

			// Memory Stats
			r.renderMetric(out, metricsByName, telemetry.MemoryCurrentBytes, "Memory Bytes (current)", humanizeBytes)
			r.renderMetric(out, metricsByName, telemetry.MemoryPeakBytes, "Memory Bytes (peak)", humanizeBytes)

			// Network Stats
			r.renderNetworkMetric(out, metricsByName, telemetry.NetstatRxBytes, telemetry.NetstatRxDropped, telemetry.NetstatRxPackets, "Network Rx")
			r.renderNetworkMetric(out, metricsByName, telemetry.NetstatTxBytes, telemetry.NetstatTxDropped, telemetry.NetstatTxPackets, "Network Tx")
		}
	}

	if metricsByName := r.db.MetricsBySpan[span.ID]; metricsByName != nil {
		// LLM Stats
		r.renderMetric(out, metricsByName, telemetry.LLMInputTokens, "Input Tokens", humanizeTokens)
		r.renderMetric(out, metricsByName, telemetry.LLMOutputTokens, "Output Tokens", humanizeTokens)
		r.renderMetric(out, metricsByName, telemetry.LLMInputTokensCacheReads, "Token Cache Reads", humanizeTokens)
		r.renderMetric(out, metricsByName, telemetry.LLMInputTokensCacheWrites, "Token Cache Writes", humanizeTokens)
	}
}

func (r renderer) renderMetric(
	out TermOutput,
	metricsByName map[string][]metricdata.DataPoint[int64],
	metricName string, label string,
	formatValue func(int64) string,
) {
	if v, ok := metricsVerbosity[metricName]; ok && v > r.Verbosity {
		return
	}
	if dataPoints := metricsByName[metricName]; len(dataPoints) > 0 {
		lastPoint := dataPoints[len(dataPoints)-1]
		fmt.Fprint(out, out.String(" "+Diamond+" ").Faint())
		displayMetric := out.String(fmt.Sprintf("%s: %s", label, formatValue(lastPoint.Value)))
		displayMetric = displayMetric.Foreground(termenv.ANSIBrightBlack)
		fmt.Fprint(out, displayMetric)
	}
}

func (r renderer) renderMetricIfNonzero(
	out TermOutput,
	metricsByName map[string][]metricdata.DataPoint[int64],
	metricName string, label string,
	formatValue func(int64) string,
) {
	if dataPoints := metricsByName[metricName]; len(dataPoints) > 0 {
		lastPoint := dataPoints[len(dataPoints)-1]
		if lastPoint.Value == 0 {
			return
		}
		r.renderMetric(out, metricsByName, metricName, label, formatValue)
	}
}

func (r renderer) renderNetworkMetric(
	out TermOutput,
	metricsByName map[string][]metricdata.DataPoint[int64],
	bytesMetric, droppedMetric, packetsMetric, label string,
) {
	r.renderMetricIfNonzero(out, metricsByName, bytesMetric, label, humanizeBytes)
	if dataPoints := metricsByName[bytesMetric]; len(dataPoints) > 0 {
		renderPacketLoss(out, metricsByName, droppedMetric, packetsMetric)
	}
}

func renderPacketLoss(
	out TermOutput,
	metricsByName map[string][]metricdata.DataPoint[int64],
	droppedMetric, packetsMetric string,
) {
	if drops := metricsByName[droppedMetric]; len(drops) > 0 {
		if packets := metricsByName[packetsMetric]; len(packets) > 0 {
			lastDrops := drops[len(drops)-1]
			lastPackets := packets[len(packets)-1]
			if lastDrops.Value > 0 && lastPackets.Value > 0 {
				droppedPercent := (float64(lastDrops.Value) / float64(lastPackets.Value)) * 100
				if droppedPercent > 0 {
					displaydropped := out.String(fmt.Sprintf(" (%.3g%% dropped)", droppedPercent))
					displaydropped = displaydropped.Foreground(termenv.ANSIRed)
					fmt.Fprint(out, displaydropped)
				}
			}
		}
	}
}

func durationString(microseconds int64) string {
	duration := time.Duration(microseconds) * time.Microsecond
	return duration.String()
}

func humanizeBytes(v int64) string {
	return humanize.Bytes(uint64(v))
}

func humanizeTokens(v int64) string {
	return humanize.Commaf(float64(v))
}

// var (
// 	progChars = []string{"⠀", "⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
// )

// func (r *renderer) renderVertexTasks(out *termenv.Output, span *Span, depth int) error {
// 	tasks := r.db.Tasks[span.SpanContext().SpanID()]
// 	if len(tasks) == 0 {
// 		return nil
// 	}
// 	var spaced bool
// 	for _, t := range tasks {
// 		var sym termenv.Style
// 		if t.Total != 0 {
// 			percent := int(100 * (float64(t.Current) / float64(t.Total)))
// 			idx := (len(progChars) - 1) * percent / 100
// 			chr := progChars[idx]
// 			sym = out.String(chr)
// 		} else {
// 			// TODO: don't bother printing non-progress-bar tasks for now
// 			// else if t.Completed != nil {
// 			// sym = out.String(IconSuccess)
// 			// } else if t.Started != nil {
// 			// sym = out.String(DotFilled)
// 			// }
// 			continue
// 		}
// 		if t.Completed.IsZero() {
// 			sym = sym.Foreground(termenv.ANSIYellow)
// 		} else {
// 			sym = sym.Foreground(termenv.ANSIGreen)
// 		}
// 		if !spaced {
// 			fmt.Fprint(out, " ")
// 			spaced = true
// 		}
// 		fmt.Fprint(out, sym)
// 	}
// 	return nil
// }

func renderPrimaryOutput(w io.Writer, db *dagui.DB) error {
	logs := db.PrimaryLogs[db.PrimarySpan]
	if len(logs) == 0 {
		return nil
	}

	fmt.Fprintln(w)

	for _, l := range logs {
		data := l.Body().AsString()
		var stream int
		l.WalkAttributes(func(attr log.KeyValue) bool {
			if attr.Key == telemetry.StdioStreamAttr {
				stream = int(attr.Value.AsInt64())
				return false
			}
			return true
		})
		switch stream {
		case 1: // stdout
			if _, err := fmt.Fprint(os.Stdout, data); err != nil {
				return err
			}
		case 2: // stderr
			fallthrough
		default:
			if _, err := fmt.Fprint(w, data); err != nil {
				return err
			}
		}
	}

	trailingLn := false
	if len(logs) > 0 {
		trailingLn = strings.HasSuffix(logs[len(logs)-1].Body().AsString(), "\n")
	}
	if !trailingLn && term.IsTerminal(int(os.Stdout.Fd())) {
		// NB: ensure there's a trailing newline if stdout is a TTY, so we don't
		// encourage module authors to add one of their own
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

func skipLoggedOutTraceMsg() bool {
	for _, env := range SkipLoggedOutTraceMsgEnvs {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

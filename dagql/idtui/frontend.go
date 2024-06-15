package idtui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type FrontendOpts struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// Silent tells the frontend to not display progress at all.
	Silent bool

	// Verbosity is the level of detail to show in the TUI.
	Verbosity int

	// Don't show things that completed beneath this duration. (default 100ms)
	TooFastThreshold time.Duration

	// Remove completed things after this duration. (default 1s)
	GCThreshold time.Duration
}

type Frontend interface {
	// Run starts a frontend, and runs the target function.
	Run(ctx context.Context, opts FrontendOpts, f func(context.Context) error) error

	// SetPrimary tells the frontend which span should be treated like the focal
	// point of the command. Its output will be displayed at the end, and its
	// children will be promoted to the "top-level" of the TUI.
	SetPrimary(spanID trace.SpanID)
	Background(cmd tea.ExecCommand) error

	// Can consume otel spans and logs.
	SpanExporter() sdktrace.SpanExporter
	LogExporter() sdklog.Exporter

	// ConnectedToEngine is called when the CLI connects to an engine.
	ConnectedToEngine(ctx context.Context, name string, version string, clientID string)
	// ConnectedToCloud is called when the CLI has started emitting events to The Cloud.
	ConnectedToCloud(ctx context.Context, url string, msg string)
}

// DumpID is exposed for troubleshooting.
func DumpID(out *termenv.Output, id *call.ID) error {
	if id.Base() != nil {
		if err := DumpID(out, id.Base()); err != nil {
			return err
		}
	}
	dag, err := id.ToProto()
	if err != nil {
		return err
	}

	db := NewDB()
	for dig, call := range dag.CallsByDigest {
		db.Calls[dig] = call
	}
	r := newRenderer(db, -1, FrontendOpts{})
	return r.renderCall(out, nil, id.Call(), "", 0, false, false)
}

type renderer struct {
	FrontendOpts

	db        *DB
	width     int
	rendering map[string]bool
}

func newRenderer(db *DB, width int, fe FrontendOpts) renderer {
	return renderer{
		FrontendOpts: fe,
		db:           db,
		width:        width,
		rendering:    map[string]bool{},
	}
}

const (
	kwColor     = termenv.ANSICyan
	faintColor  = termenv.ANSIBrightBlack
	moduleColor = termenv.ANSIMagenta
)

func (r renderer) indent(out io.Writer, depth int) {
	fmt.Fprint(out, strings.Repeat("  ", depth))
}

func (r renderer) renderIDBase(out *termenv.Output, call *callpbv1.Call) error {
	typeName := call.Type.ToAST().Name()
	parent := out.String(typeName)
	if call.Module != nil {
		parent = parent.Foreground(moduleColor)
	}
	fmt.Fprint(out, parent.String())
	if r.Verbosity > 2 && call.ReceiverDigest != "" {
		fmt.Fprint(out, out.String(fmt.Sprintf("@%s", call.ReceiverDigest)).Foreground(faintColor))
	}
	return nil
}

func (r renderer) renderCall(
	out *termenv.Output,
	span *Span,
	call *callpbv1.Call,
	prefix string,
	depth int,
	inline bool,
	internal bool,
) error {
	if r.rendering[call.Digest] {
		slog.Warn("cycle detected while rendering call", "span", span.Name(), "call", call.String())
		return nil
	}
	r.rendering[call.Digest] = true
	defer func() { delete(r.rendering, call.Digest) }()

	if !inline {
		fmt.Fprint(out, prefix)
		r.indent(out, depth)
	}

	if span != nil {
		r.renderStatus(out, span)
	}

	if call.ReceiverDigest != "" {
		if err := r.renderIDBase(out, r.db.MustCall(call.ReceiverDigest)); err != nil {
			return err
		}
		fmt.Fprint(out, ".")
	}

	fmt.Fprint(out, out.String(call.Field).Bold())

	if len(call.Args) > 0 {
		fmt.Fprint(out, "(")
		var needIndent bool
		for _, arg := range call.Args {
			if arg.GetValue().GetCallDigest() != "" {
				needIndent = true
				break
			}
		}
		if needIndent {
			fmt.Fprintln(out)
			depth++
			depth++
			for _, arg := range call.Args {
				fmt.Fprint(out, prefix)
				r.indent(out, depth)
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.GetName())
				val := arg.GetValue()
				fmt.Fprint(out, " ")
				if argDig := val.GetCallDigest(); argDig != "" {
					forceSimplify := false
					internal := internal
					argSpan := r.db.MostInterestingSpan(argDig)
					if argSpan != nil {
						forceSimplify = argSpan.Internal && !internal // only for the first internal call (not it's children)
						internal = internal || argSpan.Internal
						if span == nil {
							argSpan = nil
						}
					}
					argCall := r.db.Simplify(r.db.MustCall(argDig), forceSimplify)
					if err := r.renderCall(out, argSpan, argCall, prefix, depth-1, true, internal); err != nil {
						return err
					}
				} else {
					r.renderLiteral(out, arg.GetValue())
				}
				fmt.Fprintln(out)
			}
			depth--
			fmt.Fprint(out, prefix)
			r.indent(out, depth)
			depth-- //nolint:ineffassign
		} else {
			for i, arg := range call.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.GetName())
				r.renderLiteral(out, arg.GetValue())
			}
		}
		fmt.Fprint(out, ")")
	}

	if call.Type != nil {
		typeStr := out.String(": " + call.Type.ToAST().String()).Faint()
		fmt.Fprint(out, typeStr)
	}

	if r.Verbosity > 2 {
		fmt.Fprint(out, out.String(fmt.Sprintf(" = %s", call.Digest)).Foreground(faintColor))
	}

	if span != nil {
		r.renderDuration(out, span)
	}

	return nil
}

func (r renderer) renderVertex(out *termenv.Output, span *Span, name string, prefix string, depth int) error {
	fmt.Fprint(out, prefix)
	r.indent(out, depth)

	if span != nil {
		r.renderStatus(out, span)
	}

	fmt.Fprint(out, name)

	if span != nil {
		// TODO: when a span has child spans that have progress, do 2-d progress
		// fe.renderVertexTasks(out, span, depth)
		r.renderDuration(out, span)
	}

	return nil
}

func (r renderer) renderLiteral(out *termenv.Output, lit *callpbv1.Literal) {
	switch val := lit.GetValue().(type) {
	case *callpbv1.Literal_Bool:
		fmt.Fprint(out, out.String(fmt.Sprintf("%v", val.Bool)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Int:
		fmt.Fprint(out, out.String(fmt.Sprintf("%d", val.Int)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Float:
		fmt.Fprint(out, out.String(fmt.Sprintf("%f", val.Float)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_String_:
		if r.width != -1 && len(val.Value()) > r.width {
			display := string(digest.FromString(val.Value()))
			fmt.Fprint(out, out.String("ETOOBIG:"+display).Foreground(termenv.ANSIYellow))
			return
		}
		fmt.Fprint(out, out.String(fmt.Sprintf("%q", val.String_)).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_CallDigest:
		fmt.Fprint(out, out.String(val.CallDigest).Foreground(termenv.ANSIMagenta))
	case *callpbv1.Literal_Enum:
		fmt.Fprint(out, out.String(val.Enum).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_Null:
		fmt.Fprint(out, out.String("null").Foreground(termenv.ANSIBrightBlack))
	case *callpbv1.Literal_List:
		fmt.Fprint(out, "[")
		for i, item := range val.List.GetValues() {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			r.renderLiteral(out, item)
		}
		fmt.Fprint(out, "]")
	case *callpbv1.Literal_Object:
		fmt.Fprint(out, "{")
		for i, item := range val.Object.GetValues() {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fmt.Fprintf(out, "%s: ", item.GetName())
			r.renderLiteral(out, item.GetValue())
		}
		fmt.Fprint(out, "}")
	}
}

func (r renderer) renderStatus(out *termenv.Output, span *Span) {
	var symbol string
	var color termenv.Color
	switch {
	case span.IsRunning:
		symbol = DotFilled
		color = termenv.ANSIYellow
	case span.Canceled:
		symbol = IconSkipped
		color = termenv.ANSIBrightBlack
	case span.Status().Code == codes.Error:
		symbol = IconFailure
		color = termenv.ANSIRed
	default:
		symbol = IconSuccess
		color = termenv.ANSIGreen
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol)

	if r.Debug {
		fmt.Fprintf(out, "%s ", out.String(span.ID.String()).Foreground(termenv.ANSIBrightBlack))
	}
}

func (r renderer) renderDuration(out *termenv.Output, span *Span) {
	fmt.Fprint(out, " ")
	duration := out.String(fmtDuration(span.Duration()))
	if span.IsRunning {
		duration = duration.Foreground(termenv.ANSIYellow)
	} else {
		duration = duration.Faint()
	}
	fmt.Fprint(out, duration)
}

// var (
// 	progChars = []string{"⠀", "⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
// )

// func (r renderer) renderVertexTasks(out *termenv.Output, span *Span, depth int) error {
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

func (opts FrontendOpts) ShouldShow(tree *TraceTree) bool {
	if opts.Debug {
		// debug reveals all
		return true
	}
	span := tree.Span
	if span.IsInternal() && opts.Verbosity < 2 {
		// internal steps are hidden by default
		return false
	}
	if tree.Parent != nil && (span.Encapsulated || tree.Parent.Span.Encapsulate) && tree.Parent.Span.Err() == nil && opts.Verbosity < 2 {
		// encapsulated steps are hidden (even on error) unless their parent errors
		return false
	}
	if span.Err() != nil {
		// show errors
		return true
	}
	if opts.TooFastThreshold > 0 && span.Duration() < opts.TooFastThreshold && opts.Verbosity < 3 {
		// TODO(vito): bring this back
		// if tree.Parent != nil && tree.Parent.Span.SpanContext().SpanID() != opts.PrimarySpan {
		// 	return false
		// }
		// ignore fast steps; signal:noise is too poor
		return false
	}
	if tree.IsRunningOrChildRunning {
		// show running steps
		return true
	}
	// if opts.GCThreshold > 0 && time.Since(span.EndTime()) > opts.GCThreshold && opts.Verbosity < 1 {
	// 	// stop showing steps that ended after a given threshold
	// 	return false
	// }
	return true
}

func renderPrimaryOutput(db *DB) error {
	logs := db.PrimaryLogs[db.PrimarySpan]
	if len(logs) == 0 {
		return nil
	}

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
		case 1:
			if _, err := fmt.Fprint(os.Stdout, data); err != nil {
				return err
			}
		case 2:
			if _, err := fmt.Fprint(os.Stderr, data); err != nil {
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

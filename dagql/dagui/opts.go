package dagui

import (
	"slices"
	"strings"
	"time"
)

type FrontendOpts struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// Silent tells the frontend to not display progress at all.
	Silent bool

	// Verbosity is the level of detail to show in the TUI.
	Verbosity int

	// A distinct option from Verbosity just for disabling the 'reveal: true'
	// mechanism - mostly for tests.
	RevealNoisySpans bool

	// Whether to leave steps expanded when they complete.
	ExpandCompleted bool

	// Don't show things that completed beneath this duration. (default 100ms)
	TooFastThreshold time.Duration

	// Remove completed things after this duration. (default 1s)
	GCThreshold time.Duration

	// Open web browser with the trace URL as soon as pipeline starts.
	OpenWeb bool

	// Leave the TUI running instead of exiting after completion.
	NoExit bool

	// DotOutputFilePath is the path to write the DOT output to after execution, if any
	DotOutputFilePath string

	// DotFocusField is the field name to focus on in the DOT output, if any
	DotFocusField string

	// DotShowInternal indicates whether to include internal steps in the DOT output
	DotShowInternal bool

	// ZoomedSpan configures a span to be zoomed in on, revealing
	// its child spans.
	ZoomedSpan SpanID

	// FocusedSpan is the currently selected span, i.e. the cursor position.
	FocusedSpan SpanID

	// SpanVerbosity tracks per-span verbosity.
	SpanVerbosity map[SpanID]int

	// Whether the span has been expanded by the user.
	SpanExpanded map[SpanID]bool

	// Filter is applied while constructing the tree.
	Filter func(*Span) WalkDecision

	// StrictSubtree confines the walk to the REAL-PARENTAGE descendants of
	// ZoomedSpan: a span whose ParentSpan chain doesn't reach the zoomed span
	// is never rendered, no matter how it was attached.
	//
	// The DB deliberately blurs containment for the live TUI: a cause-linked
	// span is added to its cause's ChildSpans (see DB.integrateSpan and
	// DB.linkResumedOutput), and WalkSpans additionally walks a span's
	// CausalSpans inline so a chained call shows what produced its input.
	// Both are the right call when the tree is "everything that happened",
	// and both are wrong when the tree IS the answer to "what did THIS span
	// do" -- a scoped report (see idtui.ReportRenderOpts.ScopedSubtree), whose
	// invariant is that it renders exactly the root span's own subtree.
	// Without this, a report scoped to one LLM tool call could render an
	// unrelated (even still-running) subtree from a previous call, reached via
	// a cause link.
	StrictSubtree bool

	// UsingCloudEngine indicates whether the connected engine is a Cloud Engine
	UsingCloudEngine bool

	// RerunSuggestion, when set, replaces the body (and optionally the heading)
	// of the final report's "RUN LOCALLY" section, which by default suggests
	// `dagger check "<name>"` commands.
	//
	// It exists because the report is not always read in a terminal: when it is
	// rendered headlessly as the result of an LLM tool call, the reader is an
	// agent that has *tools* rather than a `dagger` CLI, so a shell command is
	// useless to it. The renderer keeps owning the layout while the caller owns
	// the vocabulary -- no knowledge of any particular harness or tool name
	// belongs in the frontend.
	//
	// It is given the re-runnable (outermost, failed) check names in report
	// order and returns the replacement heading and body lines. An empty heading
	// means "keep the default heading"; empty body lines mean "omit the section
	// entirely". When nil, the default `dagger check ...` body is rendered.
	RerunSuggestion func(checkNames []string) (heading string, body []string)

	// AgentStyle renders for an AI agent rather than a human at a terminal:
	// section headings become flat, greppable "== TITLE ==" markers instead of
	// bold TTY text, bodies stay at the margin, purely decorative elements (the
	// braille roll-up dots) are dropped, and span IDs are surfaced as handles.
	//
	// It is an option rather than pure environment detection because rendering
	// also happens INSIDE the engine, where there is no agent env var to sniff
	// (the engine is a daemon) but every report is assembled for an LLM: core
	// sets it on the idtui.ReportRenderOpts it renders trace reports with. For
	// the CLI it stays false and idtui.RunningInAgent() supplies the answer;
	// idtui.agentStyle is where the two halves are combined.
	AgentStyle bool
}

const (
	HideErrorsVerbosity       = -1
	HideCompletedVerbosity    = 0
	ShowCompletedVerbosity    = 1
	ShowInternalVerbosity     = 3
	ShowEncapsulatedVerbosity = 3
	ShowSpammyVerbosity       = 4
	ShowDigestsVerbosity      = 4
	ExpandCompletedVerbosity  = 5
)

func (opts FrontendOpts) ShouldShow(db *DB, span *Span) bool {
	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[span.ID]; ok {
		verbosity = v
	}
	if opts.Debug {
		// debug reveals all
		return true
	}
	if opts.FocusedSpan == span.ID {
		// prevent focused span from disappearing
		return true
	}
	if span.Ignore {
		// absolutely 100% boring spans, like 'id' and 'sync'
		//
		// this is ahead of failed check because 'sync' is often failed and is
		// _still_ not interesting
		return false
	}
	if span.IsFailedOrCausedFailure() && verbosity > HideErrorsVerbosity &&
		!span.EncapsulationHidden(opts) {
		// prioritize showing failed things, even if they're internal - but not
		// encapsulated failures whose parent succeeded (e.g. a registry's
		// routine 401 auth challenge); those are handled internal details
		return true
	}
	if span.Call() != nil {
		if strings.HasPrefix(span.Call().Field, "_") && verbosity < ShowInternalVerbosity {
			// treat underscore-prefixed calls as internal
			//
			// NOTE: this should arguably be done at emitting side, but we'll "defense
			// in depth" against ugliness anyhow
			return false
		}
		if span.Call().ReceiverDigest == "" {
			if ShouldSkipFunction("Query", span.Call().Field) {
				return false
			}
		} else {
			rcvr := db.MustCall(span.Call().ReceiverDigest)
			if ShouldSkipFunction(rcvr.Type.NamedType, span.Call().Field) {
				return false
			}
		}
	}

	if span.Hidden(opts) {
		return false
	}
	if span.IsPending() {
		// reveal pending spans so the user can see what's queued to run
		return true
	}
	if span.IsRunningOrEffectsRunning() {
		return true
	}
	if span.CheckName != "" {
		return true
	}
	// TODO: avoid breaking chains
	// if opts.TooFastThreshold > 0 &&
	// 	span.ActiveDuration(time.Now()) < opts.TooFastThreshold &&
	// 	opts.Verbosity < ShowSpammyVerbosity {
	// 	// ignore fast steps; signal:noise is too poor
	// 	return false
	// }
	if opts.GCThreshold > 0 &&
		time.Since(span.EndTime) > opts.GCThreshold &&
		verbosity < ShowCompletedVerbosity {
		// stop showing steps that ended after a given threshold
		return false
	}
	return true
}

func ShouldSkipFunction(obj, field string) bool {
	// TODO: make this configurable in the API but may not be easy to
	// generalize because an "internal" field may still need to exist in
	// codegen, for example. Could expose if internal via the TypeDefs though.
	skip := map[string][]string{
		"Query": {
			// for SDKs only
			"_builtinContainer",
			"generatedCode",
			"currentFunctionCall",
			"currentModule",
			"typeDef",
			"sourceMap",
			"function",
			// not useful until the CLI accepts ID inputs
			"cacheVolume",
			"setSecret",
			// entrypoint routing — synthetic, the CLI reads its args
			// directly to build root flags; users never call it
			"with",
			// deprecated
			"pipeline",
		},
		// for SDKs only
		"TypeDef":  nil,
		"Function": nil,
		"Module": {
			"withDescription",
			"withObject",
			"withInterface",
			"withEnum",
		},
	}
	if fields, ok := skip[obj]; ok {
		if fields == nil {
			// if no sub-fields specified, skip all fields
			return true
		}
		return slices.Contains(fields, field)
	}
	return false
}

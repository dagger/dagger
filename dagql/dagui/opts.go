package dagui

import "time"

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

	// Open web browser with the trace URL as soon as pipeline starts.
	OpenWeb bool

	// RevealAllSpans tells the frontend to show all spans, not just the spans
	// beneath the primary span.
	RevealAllSpans bool

	// Leave the TUI running instead of exiting after completion.
	NoExit bool

	// Run a custom function on exit.
	CustomExit func()

	// DotOutputFilePath is the path to write the DOT output to after execution, if any
	DotOutputFilePath string

	// DotFocusField is the field name to focus on in the DOT output, if any
	DotFocusField string

	// DotShowInternal indicates whether to include internal steps in the DOT output
	DotShowInternal bool
}

const (
	HideCompletedVerbosity    = 0
	ShowCompletedVerbosity    = 1
	ExpandCompletedVerbosity  = 2
	ShowInternalVerbosity     = 3
	ShowEncapsulatedVerbosity = 3
	ShowSpammyVerbosity       = 4
	ShowDigestsVerbosity      = 4
)

func (opts FrontendOpts) ShouldShow(tree *TraceTree) bool {
	if opts.Debug {
		// debug reveals all
		return true
	}
	span := tree.Span
	if span.IsInternal() && opts.Verbosity < ShowInternalVerbosity {
		// internal steps are hidden by default
		return false
	}
	if tree.Parent != nil && (span.Encapsulated || tree.Parent.Span.Encapsulate) && tree.Parent.Span.Err() == nil && opts.Verbosity < ShowEncapsulatedVerbosity {
		// encapsulated steps are hidden (even on error) unless their parent errors
		return false
	}
	if span.Err() != nil {
		// show errors
		return true
	}
	if tree.IsRunningOrChildRunning {
		// show running steps
		return true
	}
	if tree.Parent != nil && (opts.TooFastThreshold > 0 && span.ActiveDuration(time.Now()) < opts.TooFastThreshold && opts.Verbosity < ShowSpammyVerbosity) {
		// ignore fast steps; signal:noise is too poor
		return false
	}
	if opts.GCThreshold > 0 && time.Since(span.EndTime()) > opts.GCThreshold && opts.Verbosity < ShowCompletedVerbosity {
		// stop showing steps that ended after a given threshold
		return false
	}
	return true
}

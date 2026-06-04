package main

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestTraceUsesGlobalFrontendOpts(t *testing.T) {
	prevFrontend := Frontend
	prevOpts := opts
	prevTraceSpanFlag := traceSpanFlag
	t.Cleanup(func() {
		Frontend = prevFrontend
		opts = prevOpts
		traceSpanFlag = prevTraceSpanFlag
	})

	opts = dagui.FrontendOpts{
		Debug:             true,
		Silent:            true,
		Verbosity:         dagui.ShowSpammyVerbosity,
		RevealNoisySpans:  true,
		ExpandCompleted:   true,
		OpenWeb:           true,
		DotOutputFilePath: "trace.dot",
		DotFocusField:     "focus",
		DotShowInternal:   true,
	}
	traceSpanFlag = ""

	var gotOpts dagui.FrontendOpts
	Frontend = &idtui.FrontendMock{
		RunFunc: func(ctx context.Context, runOpts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
			gotOpts = runOpts
			return nil
		},
	}

	err := Trace(&cobra.Command{}, []string{"2f123ba77bf7bd2d4db2f70ed20613e8"})
	require.NoError(t, err)

	require.Equal(t, opts, gotOpts)
}

func TestTraceValidatesSpanFlag(t *testing.T) {
	prevTraceSpanFlag := traceSpanFlag
	t.Cleanup(func() {
		traceSpanFlag = prevTraceSpanFlag
	})
	traceSpanFlag = "not-a-span"

	err := Trace(&cobra.Command{}, []string{"2f123ba77bf7bd2d4db2f70ed20613e8"})
	require.ErrorContains(t, err, "invalid span ID")
}

func TestTraceSpanFlagIncludesZoomedSpan(t *testing.T) {
	prevFrontend := Frontend
	prevTraceSpanFlag := traceSpanFlag
	t.Cleanup(func() {
		Frontend = prevFrontend
		traceSpanFlag = prevTraceSpanFlag
	})
	traceSpanFlag = "1112131415161718"

	var gotOpts dagui.FrontendOpts
	Frontend = &idtui.FrontendMock{
		RunFunc: func(ctx context.Context, runOpts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
			gotOpts = runOpts
			return nil
		},
	}

	err := Trace(&cobra.Command{}, []string{"2f123ba77bf7bd2d4db2f70ed20613e8"})
	require.NoError(t, err)

	require.True(t, gotOpts.ZoomedSpanIncludeSelf)
}

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
	t.Cleanup(func() {
		Frontend = prevFrontend
		opts = prevOpts
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

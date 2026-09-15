package daggercmd

import (
	"context"
	"errors"
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

	err := traceRun(&cobra.Command{}, []string{"2f123ba77bf7bd2d4db2f70ed20613e8"})
	require.NoError(t, err)

	require.Equal(t, opts, gotOpts)
}

func TestFetchGroupGoDuringWait(t *testing.T) {
	var fg fetchGroup

	release := make(chan struct{})
	fg.Go(func() error {
		<-release
		return nil
	})

	waitDone := make(chan error, 1)
	go func() { waitDone <- fg.Wait() }()

	// Spawn more fetches while Wait is parked -- the TUI event loop does this
	// when the user expands spans during the run goroutine's drains. With a
	// sync.WaitGroup/errgroup this interleaving is documented misuse.
	for range 10 {
		fg.Go(func() error { return nil })
	}
	fg.Go(func() error { return errors.New("boom") })
	fg.Go(func() error { return context.Canceled }) // interrupt: not a failure

	close(release)
	err := <-waitDone
	require.ErrorContains(t, err, "boom")
	require.NotErrorIs(t, err, context.Canceled)
}

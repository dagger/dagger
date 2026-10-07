package networkmetrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecMountWaitsAndRetainsFinalCounters(t *testing.T) {
	exited := make(chan struct{})
	waiting := make(chan struct{})
	command := &testCommandCounters{
		closed: make(chan struct{}),
		wait: func(context.Context) error {
			close(waiting)
			<-exited
			return nil
		},
	}
	command.reads.Store(1)
	r := &execMountResources{commands: []commandNetworkCounters{command}}
	finish := r.finish(command)
	done := make(chan error, 1)
	go func() { done <- finish() }()
	<-waiting
	select {
	case <-done:
		t.Fatal("release returned before the daemon exited")
	default:
	}
	close(exited)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("release did not observe daemon exit")
	}
	require.NoError(t, finish())
	select {
	case <-command.closed:
		t.Fatal("counters closed before the final exec sample")
	default:
	}
	sample, err := r.Sample()
	require.NoError(t, err)
	require.True(t, sample.ScopeSupported)
	require.EqualValues(t, 19, sample.ExternalTxBytes)
	require.NoError(t, r.Close())
	<-command.closed
}

package pipe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPipe(t *testing.T) {
	t.Parallel()

	runCh := make(chan struct{})
	f := func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-runCh:
			return "res0", nil
		}
	}

	waitSignal := make(chan struct{}, 10)
	signalled := 0
	signal := func() {
		signalled++
		waitSignal <- struct{}{}
	}

	p, start := NewWithFunction[any](f)
	p.OnSendCompletion = signal
	go start()
	require.Equal(t, false, p.Receiver.Receive())

	st := p.Receiver.Status()
	require.Equal(t, false, st.Completed)
	require.Equal(t, false, st.Canceled)
	require.Zero(t, st.Value)
	require.Equal(t, 0, signalled)

	close(runCh)
	<-waitSignal

	p.Receiver.Receive()
	st = p.Receiver.Status()
	require.Equal(t, true, st.Completed)
	require.Equal(t, false, st.Canceled)
	require.NoError(t, st.Err)
	require.Equal(t, "res0", st.Value)
}

func TestPipeCancel(t *testing.T) {
	t.Parallel()

	runCh := make(chan struct{})
	f := func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-runCh:
			return "res0", nil
		}
	}

	waitSignal := make(chan struct{}, 10)
	signalled := 0
	signal := func() {
		signalled++
		waitSignal <- struct{}{}
	}

	p, start := NewWithFunction[any](f)
	p.OnSendCompletion = signal
	go start()
	p.Receiver.Receive()

	st := p.Receiver.Status()
	require.Equal(t, false, st.Completed)
	require.Equal(t, false, st.Canceled)
	require.Zero(t, st.Value)
	require.Equal(t, 0, signalled)

	p.Receiver.Cancel()
	<-waitSignal

	p.Receiver.Receive()
	st = p.Receiver.Status()
	require.Equal(t, true, st.Completed)
	require.Equal(t, true, st.Canceled)
	require.Error(t, st.Err)
	require.ErrorIs(t, st.Err, context.Canceled)
}

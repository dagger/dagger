package pipe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPipe(t *testing.T) {
	t.Parallel()

	runCh := make(chan struct{})
	f := func(ctx context.Context) (interface{}, error) {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
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

	p, start := NewWithFunction(f)
	p.OnSendCompletion = signal
	go start()
	require.Equal(t, false, p.Receiver.Receive())

	st := p.Receiver.Status()
	require.Equal(t, st.Completed, false)
	require.Equal(t, st.Canceled, false)
	require.Nil(t, st.Value)
	require.Equal(t, signalled, 0)

	close(runCh)
	<-waitSignal

	p.Receiver.Receive()
	st = p.Receiver.Status()
	require.Equal(t, st.Completed, true)
	require.Equal(t, st.Canceled, false)
	require.NoError(t, st.Err)
	require.Equal(t, st.Value.(string), "res0")
}

func TestPipeCancel(t *testing.T) {
	t.Parallel()

	runCh := make(chan struct{})
	f := func(ctx context.Context) (interface{}, error) {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
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

	p, start := NewWithFunction(f)
	p.OnSendCompletion = signal
	go start()
	p.Receiver.Receive()

	st := p.Receiver.Status()
	require.Equal(t, st.Completed, false)
	require.Equal(t, st.Canceled, false)
	require.Nil(t, st.Value)
	require.Equal(t, signalled, 0)

	p.Receiver.Cancel()
	<-waitSignal

	p.Receiver.Receive()
	st = p.Receiver.Status()
	require.Equal(t, st.Completed, true)
	require.Equal(t, st.Canceled, true)
	require.Error(t, st.Err)
	require.ErrorIs(t, st.Err, context.Canceled)
}

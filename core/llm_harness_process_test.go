package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLLMHarnessCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind LLMHarnessKind
		want []string
	}{
		{
			name: "codex app server",
			kind: LLMHarnessCodex,
			want: []string{"codex", "app-server"},
		},
		{
			name: "persistent claude stream json",
			kind: LLMHarnessClaude,
			want: []string{
				"claude",
				"-p",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := llmHarnessCommand(test.kind)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}

	_, err := llmHarnessCommand(LLMHarnessKind("OTHER"))
	require.EqualError(t, err, `unsupported LLM harness kind "OTHER"`)
}

func TestLLMHarnessProcessPersistentLifecycle(t *testing.T) {
	t.Parallel()

	var starts atomic.Int32
	var releases atomic.Int32
	interrupts := make(chan syscall.Signal, 1)

	process, err := startLLMHarnessProcessService("/work", []string{"fake", "server"}, func(sio *ServiceIO) (*RunningService, func(), error) {
		starts.Add(1)
		done := make(chan struct{})
		var doneOnce sync.Once
		finish := func() { doneOnce.Do(func() { close(done) }) }

		go func() {
			defer finish()
			defer sio.Stdout.Close()
			defer sio.Stderr.Close()
			scanner := bufio.NewScanner(sio.Stdin)
			for scanner.Scan() {
				_, _ = fmt.Fprintf(sio.Stdout, "echo:%s\n", scanner.Text())
			}
		}()

		return &RunningService{
			Signal: func(_ context.Context, signal syscall.Signal) error {
				interrupts <- signal
				return nil
			},
			Stop: func(_ context.Context, _ bool) error {
				_ = sio.Close()
				finish()
				return nil
			},
			Wait: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-done:
					return nil
				}
			},
		}, func() { releases.Add(1) }, nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), starts.Load())
	require.Equal(t, []string{"fake", "server"}, process.Command())
	require.Equal(t, "/work", process.Workdir())

	stdout := bufio.NewReader(process)
	for _, turn := range []string{"one", "two"} {
		_, err := io.WriteString(process, turn+"\n")
		require.NoError(t, err)
		line, err := stdout.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "echo:"+turn+"\n", line)
	}

	require.NoError(t, process.Interrupt(context.Background()))
	require.Equal(t, syscall.SIGINT, <-interrupts)

	// Interrupt does not close the protocol pipes; the same process handles the
	// following turn without another service start.
	_, err = io.WriteString(process, "three\n")
	require.NoError(t, err)
	line, err := stdout.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "echo:three\n", line)
	require.Equal(t, int32(1), starts.Load())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, process.Stop(ctx))
	require.NoError(t, process.Wait(ctx))
	require.NoError(t, process.Close(), "io.Closer cleanup is idempotent")
	require.Equal(t, int32(1), releases.Load())

	_, err = stdout.ReadByte()
	require.ErrorIs(t, err, io.EOF, "adapter stdout reader must terminate after process exit")
	stderr, err := io.ReadAll(process.Stderr())
	require.NoError(t, err)
	require.Empty(t, stderr)

	_, err = process.Write([]byte("late\n"))
	require.ErrorIs(t, err, ErrLLMHarnessProcessClosed)
	require.ErrorIs(t, process.Interrupt(ctx), ErrLLMHarnessProcessClosed)
}

func TestLLMHarnessProcessNaturalExitClosesAdapterPipes(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	process, err := startLLMHarnessProcessService("/work", []string{"fake"}, func(sio *ServiceIO) (*RunningService, func(), error) {
		go func() {
			_, _ = io.WriteString(sio.Stdout, "final\n")
			// Deliberately leave ServiceIO open. The runner must close it when
			// Wait reports natural exit so protocol readers observe EOF.
			close(done)
		}()
		return &RunningService{
			Stop: func(context.Context, bool) error { return nil },
			Wait: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-done:
					return nil
				}
			},
		}, func() {}, nil
	})
	require.NoError(t, err)

	stdout := bufio.NewReader(process)
	line, err := stdout.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "final\n", line)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, process.Wait(ctx))
	_, err = stdout.ReadByte()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, process.Close())
}

func TestLLMHarnessProcessStartFailureClosesPipes(t *testing.T) {
	t.Parallel()

	want := errors.New("boom")
	_, err := startLLMHarnessProcessService("/work", []string{"fake"}, func(sio *ServiceIO) (*RunningService, func(), error) {
		return nil, nil, want
	})
	require.ErrorIs(t, err, want)
}

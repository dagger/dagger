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
			want: []string{"sh", "-c", `exec codex app-server -c "mcp_servers.dagger.url=\"http://127.0.0.1:${DAGGER_SESSION_PORT}/_dagger/exec-http\"" -c 'mcp_servers.dagger.bearer_token_env_var="DAGGER_SESSION_HTTP_TOKEN"' -c 'mcp_servers.dagger.required=true' -c 'mcp_servers.dagger.default_tools_approval_mode="approve"'`},
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

	resumed, err := llmHarnessCommand(LLMHarnessClaude, "session-1")
	require.NoError(t, err)
	require.Equal(t, []string{
		"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--resume", "session-1",
	}, resumed)

	_, err = llmHarnessCommand(LLMHarnessKind("OTHER"))
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

func TestLLMHarnessOutputPipeRuntimeBackpressure(t *testing.T) {
	t.Parallel()

	reader, writer := newLLMHarnessOutputPipe()
	startup := "startup output"
	runtime := "runtime output"
	_, err := io.WriteString(writer, startup)
	require.NoError(t, err)
	writer.activate()
	attempting := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		close(attempting)
		_, err := io.WriteString(writer, runtime)
		written <- err
	}()
	<-attempting
	select {
	case err := <-written:
		t.Fatalf("runtime write completed without a reader: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	startupBuf := make([]byte, len(startup))
	_, err = io.ReadFull(reader, startupBuf)
	require.NoError(t, err)
	require.Equal(t, startup, string(startupBuf))
	runtimeBuf := make([]byte, len(runtime))
	_, err = io.ReadFull(reader, runtimeBuf)
	require.NoError(t, err)
	require.Equal(t, runtime, string(runtimeBuf))
	select {
	case err := <-written:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runtime write remained blocked after output was read")
	}
	require.NoError(t, writer.Close())
	_, err = reader.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, reader.Close())
}

func TestLLMHarnessOutputPipeCloseUnblocksRuntimeWrite(t *testing.T) {
	t.Parallel()

	reader, writer := newLLMHarnessOutputPipe()
	writer.activate()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "blocked runtime output")
		written <- err
	}()
	select {
	case err := <-written:
		t.Fatalf("runtime write completed without a reader: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, writer.Close())
	select {
	case err := <-written:
		require.ErrorIs(t, err, io.ErrClosedPipe)
	case <-time.After(time.Second):
		t.Fatal("closing output did not unblock runtime write")
	}
	require.NoError(t, reader.Close())
}

func TestLLMHarnessProcessSynchronousStartupOutput(t *testing.T) {
	t.Parallel()

	want := errors.New("harness startup exited")
	stdout := "startup protocol output\n"
	stderr := "startup diagnostic output\n"
	type startResult struct {
		process *LLMHarnessProcess
		err     error
	}
	started := make(chan startResult, 1)
	go func() {
		process, err := startLLMHarnessProcessService("/work", []string{"fake"}, func(sio *ServiceIO) (*RunningService, func(), error) {
			// StartInteractive can synchronously receive output while it waits for
			// the service to either become healthy or exit. Neither stream may
			// block merely because the process adapter has not been returned yet.
			if _, err := io.WriteString(sio.Stdout, stdout); err != nil {
				return nil, nil, err
			}
			if _, err := io.WriteString(sio.Stderr, stderr); err != nil {
				return nil, nil, err
			}
			// A service that exits during startup closes its output before
			// StartInteractive returns. Buffered bytes must still flush before EOF.
			if err := errors.Join(sio.Stdout.Close(), sio.Stderr.Close()); err != nil {
				return nil, nil, err
			}
			return &RunningService{
				Stop: func(context.Context, bool) error { return nil },
				Wait: func(context.Context) error { return want },
			}, func() {}, nil
		})
		started <- startResult{process: process, err: err}
	}()

	var result startResult
	select {
	case result = <-started:
	case <-time.After(time.Second):
		t.Fatal("service starter blocked writing startup output")
	}
	require.NoError(t, result.err)
	require.NotNil(t, result.process)

	gotStdout, err := io.ReadAll(result.process)
	require.Equal(t, stdout, string(gotStdout))
	require.ErrorIs(t, err, want)
	gotStderr, err := io.ReadAll(result.process.Stderr())
	require.NoError(t, err)
	require.Equal(t, stderr, string(gotStderr))
	require.ErrorIs(t, result.process.Wait(t.Context()), want)
	require.ErrorIs(t, result.process.Close(), want)
}

func TestLLMHarnessProcessExitErrorPropagatesToAdapter(t *testing.T) {
	t.Parallel()

	want := errors.New("harness container exited")
	exited := make(chan struct{})
	process, err := startLLMHarnessProcessService("/work", []string{"fake"}, func(sio *ServiceIO) (*RunningService, func(), error) {
		go func() {
			// Let the adapter's initialize request reach the harness before it
			// exits, reproducing a service that starts successfully and then fails.
			_, _ = bufio.NewReader(sio.Stdin).ReadString('\n')
			// Real container services close ServiceIO immediately before Wait
			// publishes the exit error. The process reader must not lose that race
			// and report this closure as a generic EOF.
			_ = sio.Close()
			close(exited)
		}()
		return &RunningService{
			Stop: func(context.Context, bool) error { return nil },
			Wait: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-exited:
					return want
				}
			},
		}, func() {}, nil
	})
	require.NoError(t, err)

	adapter := NewCodexLLMHarnessAdapter(process)
	_, err = adapter.Start(t.Context(), LLMHarnessStart{})
	require.ErrorIs(t, err, want)
	require.ErrorContains(t, err, "initialize Codex app-server")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorIs(t, process.Wait(ctx), want)
	_, err = process.Write([]byte("late\n"))
	require.ErrorIs(t, err, want)
	require.ErrorIs(t, adapter.Close(ctx), want)
}

func TestLLMHarnessProcessStartFailureClosesPipes(t *testing.T) {
	t.Parallel()

	want := errors.New("boom")
	_, err := startLLMHarnessProcessService("/work", []string{"fake"}, func(sio *ServiceIO) (*RunningService, func(), error) {
		return nil, nil, want
	})
	require.ErrorIs(t, err, want)
}

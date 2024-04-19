package tests

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/containerd/containerd/namespaces"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/source/containerimage"
	"github.com/moby/buildkit/worker/base"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func NewBusyboxSourceSnapshot(ctx context.Context, t *testing.T, w *base.Worker, sm *session.Manager) cache.ImmutableRef {
	img, err := containerimage.NewImageIdentifier("docker.io/library/busybox:latest")
	require.NoError(t, err)
	src, err := w.SourceManager.Resolve(ctx, img, sm, nil)
	require.NoError(t, err)
	_, _, _, _, err = src.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	snap, err := src.Snapshot(ctx, nil)
	require.NoError(t, err)
	return snap
}

func NewCtx(s string) context.Context {
	return namespaces.WithNamespace(context.Background(), s)
}

func TestWorkerExec(t *testing.T, w *base.Worker) {
	ctx := NewCtx("buildkit-test")
	ctx, cancel := context.WithCancelCause(ctx)
	sm, err := session.NewManager()
	require.NoError(t, err)

	snap := NewBusyboxSourceSnapshot(ctx, t, w, sm)
	root, err := w.CacheMgr.New(ctx, snap, nil)
	require.NoError(t, err)

	id := identity.NewID()

	// verify pid1 exits when stdin sees EOF
	ctxTimeout, cancelTimeout := context.WithTimeoutCause(ctx, 5*time.Second, nil)
	started := make(chan struct{})
	pipeR, pipeW := io.Pipe()
	go func() {
		select {
		case <-ctxTimeout.Done():
			t.Error("Unexpected timeout waiting for pid1 to start")
		case <-started:
			pipeW.Write([]byte("hello"))
			pipeW.Close()
		}
	}()
	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	_, err = w.WorkerOpt.Executor.Run(ctxTimeout, id, execMount(root), nil, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"cat"},
			Cwd:  "/",
			Env:  []string{"PATH=/bin:/usr/bin:/sbin:/usr/sbin"},
		},
		Stdin:  pipeR,
		Stdout: &nopCloser{stdout},
		Stderr: &nopCloser{stderr},
	}, started)
	cancelTimeout()
	t.Logf("Stdout: %s", stdout.String())
	t.Logf("Stderr: %s", stderr.String())
	require.NoError(t, err)
	require.Equal(t, "hello", stdout.String())
	require.Empty(t, stderr.String())

	// first start pid1 in the background
	eg := errgroup.Group{}
	started = make(chan struct{})
	eg.Go(func() error {
		_, err := w.WorkerOpt.Executor.Run(ctx, id, execMount(root), nil, executor.ProcessInfo{
			Meta: executor.Meta{
				Args: []string{"sleep", "10"},
				Cwd:  "/",
				Env:  []string{"PATH=/bin:/usr/bin:/sbin:/usr/sbin"},
			},
		}, started)
		return err
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Error("Unexpected timeout waiting for pid1 to start")
	}

	stdout.Reset()
	stderr.Reset()

	// verify pid1 is the sleep command via Exec
	err = w.WorkerOpt.Executor.Exec(ctx, id, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"ps", "-o", "pid,comm"},
		},
		Stdout: &nopCloser{stdout},
		Stderr: &nopCloser{stderr},
	})
	t.Logf("Stdout: %s", stdout.String())
	t.Logf("Stderr: %s", stderr.String())
	require.NoError(t, err)
	// verify pid1 is sleep
	require.Contains(t, stdout.String(), "1 sleep")
	require.Empty(t, stderr.String())

	// simulate: echo -n "hello" | cat > /tmp/msg
	stdin := bytes.NewReader([]byte("hello"))
	stdout.Reset()
	stderr.Reset()
	err = w.WorkerOpt.Executor.Exec(ctx, id, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"sh", "-c", "cat > /tmp/msg"},
		},
		Stdin:  io.NopCloser(stdin),
		Stdout: &nopCloser{stdout},
		Stderr: &nopCloser{stderr},
	})
	require.NoError(t, err)
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())

	// verify contents of /tmp/msg
	stdout.Reset()
	stderr.Reset()
	err = w.WorkerOpt.Executor.Exec(ctx, id, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"cat", "/tmp/msg"},
		},
		Stdout: &nopCloser{stdout},
		Stderr: &nopCloser{stderr},
	})
	t.Logf("Stdout: %s", stdout.String())
	t.Logf("Stderr: %s", stderr.String())
	require.NoError(t, err)
	require.Equal(t, "hello", stdout.String())
	require.Empty(t, stderr.String())

	// stop pid1
	cancel(errors.WithStack(context.Canceled))

	err = eg.Wait()
	// we expect pid1 to get canceled after we test the exec
	require.True(t, errors.Is(err, context.Canceled))

	err = snap.Release(ctx)
	require.NoError(t, err)
}

func TestWorkerExecFailures(t *testing.T, w *base.Worker) {
	ctx := NewCtx("buildkit-test")
	sm, err := session.NewManager()
	require.NoError(t, err)

	snap := NewBusyboxSourceSnapshot(ctx, t, w, sm)
	root, err := w.CacheMgr.New(ctx, snap, nil)
	require.NoError(t, err)

	id := identity.NewID()

	// pid1 will start but only long enough for /bin/false to run
	eg := errgroup.Group{}
	started := make(chan struct{})
	eg.Go(func() error {
		_, err := w.WorkerOpt.Executor.Run(ctx, id, execMount(root), nil, executor.ProcessInfo{
			Meta: executor.Meta{
				Args: []string{"/bin/false"},
				Cwd:  "/",
			},
		}, started)
		return err
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Error("Unexpected timeout waiting for pid1 to start")
	}

	// this should fail since pid1 has already exited
	err = w.WorkerOpt.Executor.Exec(ctx, id, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"/bin/true"},
		},
	})
	require.Error(t, err) // pid1 no longer running

	err = eg.Wait()
	require.Error(t, err) // process returned non-zero exit code: 1

	// pid1 will not start, bogus pid1 command
	eg = errgroup.Group{}
	started = make(chan struct{})
	eg.Go(func() error {
		_, err := w.WorkerOpt.Executor.Run(ctx, id, execMount(root), nil, executor.ProcessInfo{
			Meta: executor.Meta{
				Args: []string{"bogus"},
			},
		}, started)
		return err
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Error("Unexpected timeout waiting for pid1 to start")
	}

	// this should fail since pid1 never started
	err = w.WorkerOpt.Executor.Exec(ctx, id, executor.ProcessInfo{
		Meta: executor.Meta{
			Args: []string{"/bin/true"},
		},
	})
	require.Error(t, err) // container has exited with error

	err = eg.Wait()
	require.Error(t, err) // pid1 did not terminate successfully

	err = snap.Release(ctx)
	require.NoError(t, err)
}

func TestWorkerCancel(t *testing.T, w *base.Worker) {
	ctx := NewCtx("buildkit-test")
	sm, err := session.NewManager()
	require.NoError(t, err)

	snap := NewBusyboxSourceSnapshot(ctx, t, w, sm)
	root, err := w.CacheMgr.New(ctx, snap, nil)
	require.NoError(t, err)

	id := identity.NewID()

	started := make(chan struct{})

	pid1Ctx, pid1Cancel := context.WithCancelCause(ctx)
	defer pid1Cancel(errors.WithStack(context.Canceled))

	var (
		pid1Err, pid2Err error
		pid1Done         = make(chan struct{})
		pid2Done         = make(chan struct{})
	)

	go func() {
		defer close(pid1Done)
		_, pid1Err = w.WorkerOpt.Executor.Run(pid1Ctx, id, execMount(root), nil, executor.ProcessInfo{
			Meta: executor.Meta{
				Args: []string{"/bin/sleep", "10"},
				Cwd:  "/",
			},
		}, started)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Error("Unexpected timeout waiting for pid1 to start")
	}

	pid2Ctx, pid2Cancel := context.WithCancelCause(ctx)
	defer pid2Cancel(errors.WithStack(context.Canceled))

	started = make(chan struct{})

	go func() {
		defer close(pid2Done)
		// TODO why doesn't Exec allow for started channel??  Fake it for now
		go func() {
			<-time.After(2 * time.Second)
			close(started)
		}()
		pid2Err = w.WorkerOpt.Executor.Exec(pid2Ctx, id, executor.ProcessInfo{
			Meta: executor.Meta{
				Args: []string{"/bin/sleep", "10"},
				Cwd:  "/",
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Error("Unexpected timeout waiting for pid2 to start")
	}

	pid2Cancel(errors.WithStack(context.Canceled))
	<-pid2Done
	require.Contains(t, pid2Err.Error(), "exit code: 137", "pid2 exits with sigkill")

	pid1Cancel(errors.WithStack(context.Canceled))
	<-pid1Done
	require.Contains(t, pid1Err.Error(), "exit code: 137", "pid1 exits with sigkill")
}

type nopCloser struct {
	io.Writer
}

func (n *nopCloser) Close() error {
	return nil
}

func execMount(m cache.Mountable) executor.Mount {
	return executor.Mount{Src: &mountable{m: m}}
}

type mountable struct {
	m cache.Mountable
}

func (m *mountable) Mount(ctx context.Context, readonly bool) (snapshot.Mountable, error) {
	return m.m.Mount(ctx, readonly, nil)
}

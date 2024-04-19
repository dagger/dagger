//go:build linux
// +build linux

package runc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/tests"
	"github.com/stretchr/testify/require"
)

func newWorkerOpt(t *testing.T, processMode oci.ProcessMode) base.WorkerOpt {
	tmpdir := t.TempDir()

	snFactory := SnapshotterFactory{
		Name: "overlayfs",
		New: func(root string) (ctdsnapshot.Snapshotter, error) {
			return overlay.NewSnapshotter(root)
		},
	}
	rootless := false
	workerOpt, err := NewWorkerOpt(tmpdir, snFactory, rootless, processMode, nil, nil, netproviders.Opt{Mode: "host"}, nil, "", "", false, nil, "", "")
	require.NoError(t, err)

	return workerOpt
}

func checkRequirement(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root")
	}

	if _, err := exec.LookPath("runc"); err != nil {
		if _, err := exec.LookPath("buildkit-runc"); err != nil {
			t.Skipf("no runc found: %s", err.Error())
		}
	}
}

func TestRuncWorker(t *testing.T) {
	t.Parallel()
	checkRequirement(t)

	workerOpt := newWorkerOpt(t, oci.ProcessSandbox)
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	ctx := tests.NewCtx("buildkit-test")
	sm, err := session.NewManager()
	require.NoError(t, err)
	snap := tests.NewBusyboxSourceSnapshot(ctx, t, w, sm)

	mounts, err := snap.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mounts)

	target, err := lm.Mount()
	require.NoError(t, err)

	f, err := os.Open(target)
	require.NoError(t, err)

	names, err := f.Readdirnames(-1)
	require.NoError(t, err)
	require.True(t, len(names) > 5)

	err = f.Close()
	require.NoError(t, err)

	lm.Unmount()
	require.NoError(t, err)

	du, err := w.CacheMgr.DiskUsage(ctx, client.DiskUsageInfo{})
	require.NoError(t, err)

	// for _, d := range du {
	// 	t.Logf("du: %+v\n", d)
	// }

	for _, d := range du {
		require.True(t, d.Size >= 8192)
	}

	meta := executor.Meta{
		Args: []string{"/bin/sh", "-c", "mkdir /run && echo \"foo\" > /run/bar"},
		Cwd:  "/",
	}

	stderr := bytes.NewBuffer(nil)
	_, err = w.WorkerOpt.Executor.Run(ctx, "", execMount(snap, true), nil, executor.ProcessInfo{Meta: meta, Stderr: &nopCloser{stderr}}, nil)
	require.Error(t, err) // Read-only root
	// typical error is like `mkdir /.../rootfs/proc: read-only file system`.
	// make sure the error is caused before running `echo foo > /bar`.
	require.Contains(t, stderr.String(), "read-only file system")

	root, err := w.CacheMgr.New(ctx, snap, nil, cache.CachePolicyRetain)
	require.NoError(t, err)

	_, err = w.WorkerOpt.Executor.Run(ctx, "", execMount(root, false), nil, executor.ProcessInfo{Meta: meta, Stderr: &nopCloser{stderr}}, nil)
	require.NoError(t, err)

	meta = executor.Meta{
		Args: []string{"/bin/ls", "/etc/resolv.conf"},
		Cwd:  "/",
	}

	_, err = w.WorkerOpt.Executor.Run(ctx, "", execMount(root, false), nil, executor.ProcessInfo{Meta: meta, Stderr: &nopCloser{stderr}}, nil)
	require.NoError(t, err)

	rf, err := root.Commit(ctx)
	require.NoError(t, err)

	mounts, err = rf.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm = snapshot.LocalMounter(mounts)

	target, err = lm.Mount()
	require.NoError(t, err)

	//Verifies fix for issue https://github.com/moby/buildkit/issues/429
	dt, err := os.ReadFile(filepath.Join(target, "run", "bar"))

	require.NoError(t, err)
	require.Equal(t, string(dt), "foo\n")

	lm.Unmount()
	require.NoError(t, err)

	err = rf.Release(ctx)
	require.NoError(t, err)

	err = snap.Release(ctx)
	require.NoError(t, err)

	retry := 0
	var du2 []*client.UsageInfo
	for {
		du2, err = w.CacheMgr.DiskUsage(ctx, client.DiskUsageInfo{})
		require.NoError(t, err)
		if len(du2)-len(du) != 1 && retry == 0 {
			t.Logf("invalid expected size: du1: %+v du2: %+v", formatDiskUsage(du), formatDiskUsage(du2))
			time.Sleep(300 * time.Millisecond) // make race non-fatal if it fixes itself
			retry++
			continue
		}
		break
	}
	require.Equal(t, 1, len(du2)-len(du), "du1: %+v du2: %+v", formatDiskUsage(du), formatDiskUsage(du2))
}

func formatDiskUsage(du []*client.UsageInfo) string {
	buf := new(bytes.Buffer)
	for _, d := range du {
		fmt.Fprintf(buf, "%+v ", d)
	}
	return buf.String()
}

func TestRuncWorkerNoProcessSandbox(t *testing.T) {
	t.Parallel()
	checkRequirement(t)

	workerOpt := newWorkerOpt(t, oci.NoProcessSandbox)
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	ctx := tests.NewCtx("buildkit-test")
	sm, err := session.NewManager()
	require.NoError(t, err)
	snap := tests.NewBusyboxSourceSnapshot(ctx, t, w, sm)
	root, err := w.CacheMgr.New(ctx, snap, nil)
	require.NoError(t, err)

	// ensure the procfs is shared
	selfPID := os.Getpid()
	selfCmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", selfPID))
	require.NoError(t, err)
	meta := executor.Meta{
		Args: []string{"/bin/cat", fmt.Sprintf("/proc/%d/cmdline", selfPID)},
		Cwd:  "/",
	}
	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	_, err = w.WorkerOpt.Executor.Run(ctx, "", execMount(root, false), nil, executor.ProcessInfo{Meta: meta, Stdout: &nopCloser{stdout}, Stderr: &nopCloser{stderr}}, nil)
	require.NoError(t, err, fmt.Sprintf("stdout=%q, stderr=%q", stdout.String(), stderr.String()))
	require.Equal(t, string(selfCmdline), stdout.String())
}

func TestRuncWorkerExec(t *testing.T) {
	t.Parallel()
	checkRequirement(t)

	workerOpt := newWorkerOpt(t, oci.ProcessSandbox)
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerExec(t, w)
}

func TestRuncWorkerExecFailures(t *testing.T) {
	t.Parallel()
	checkRequirement(t)

	workerOpt := newWorkerOpt(t, oci.ProcessSandbox)
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerExecFailures(t, w)
}

func TestRuncWorkerCancel(t *testing.T) {
	t.Parallel()
	checkRequirement(t)

	workerOpt := newWorkerOpt(t, oci.ProcessSandbox)
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerCancel(t, w)
}

type nopCloser struct {
	io.Writer
}

func (n *nopCloser) Close() error {
	return nil
}

func execMount(m cache.Mountable, readonly bool) executor.Mount {
	return executor.Mount{Src: &mountable{m: m}, Readonly: readonly}
}

type mountable struct {
	m cache.Mountable
}

func (m *mountable) Mount(ctx context.Context, readonly bool) (snapshot.Mountable, error) {
	return m.m.Mount(ctx, readonly, nil)
}

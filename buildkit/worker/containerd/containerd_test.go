//go:build !windows
// +build !windows

package containerd

import (
	"context"
	"testing"

	"github.com/dagger/dagger/buildkit/util/network/netproviders"
	"github.com/dagger/dagger/buildkit/util/testutil/integration"
	"github.com/dagger/dagger/buildkit/util/testutil/workers"
	"github.com/dagger/dagger/buildkit/worker/base"
	"github.com/dagger/dagger/buildkit/worker/tests"
	"github.com/stretchr/testify/require"
)

func init() {
	workers.InitContainerdWorker()
}

func TestContainerdWorkerIntegration(t *testing.T) {
	checkRequirement(t)
	integration.Run(t, integration.TestFuncs(
		testContainerdWorkerExec,
		testContainerdWorkerExecFailures,
		testContainerdWorkerCancel,
	))
}

func newWorkerOpt(t *testing.T, addr string) base.WorkerOpt {
	tmpdir := t.TempDir()
	rootless := false
	options := WorkerOptions{
		Root:            tmpdir,
		Address:         addr,
		SnapshotterName: "overlayfs",
		Namespace:       "buildkit-test",
		CgroupParent:    "",
		Rootless:        rootless,
		Labels:          nil,
		DNS:             nil,
		NetworkOpt:      netproviders.Opt{Mode: "host"},
		ApparmorProfile: "",
		Selinux:         false,
		ParallelismSem:  nil,
		TraceSocket:     "",
		Runtime:         nil,
	}
	workerOpt, err := NewWorkerOpt(options)
	require.NoError(t, err)
	return workerOpt
}

func testContainerdWorkerExec(t *testing.T, sb integration.Sandbox) {
	if sb.Rootless() {
		t.Skip("requires root")
	}
	workerOpt := newWorkerOpt(t, sb.ContainerdAddress())
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerExec(t, w)
}

func testContainerdWorkerExecFailures(t *testing.T, sb integration.Sandbox) {
	if sb.Rootless() {
		t.Skip("requires root")
	}
	workerOpt := newWorkerOpt(t, sb.ContainerdAddress())
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerExecFailures(t, w)
}

func testContainerdWorkerCancel(t *testing.T, sb integration.Sandbox) {
	if sb.Rootless() {
		t.Skip("requires root")
	}
	workerOpt := newWorkerOpt(t, sb.ContainerdAddress())
	w, err := base.NewWorker(context.TODO(), workerOpt)
	require.NoError(t, err)

	tests.TestWorkerCancel(t, w)
}

//go:build !windows
// +build !windows

package containerd

import (
	"context"
	"testing"

	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/tests"
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
	workerOpt, err := NewWorkerOpt(tmpdir, addr, "overlayfs", "buildkit-test", rootless, nil, nil, netproviders.Opt{Mode: "host"}, "", false, nil, "", nil)
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

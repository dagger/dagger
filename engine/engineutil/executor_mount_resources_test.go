package engineutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	runc "github.com/containerd/go-runc"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/executor"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

type testMountResources struct {
	active bool
	events *[]string
}

func (r *testMountResources) Active() bool    { return r.active }
func (r *testMountResources) Available() bool { return true }
func (r *testMountResources) Sample() (*resourcestypes.NetworkSample, error) {
	*r.events = append(*r.events, "final sample")
	return &resourcestypes.NetworkSample{ScopeSupported: true, ExternalTxBytes: 37}, nil
}
func (r *testMountResources) Prepare(*exec.Cmd) (func() error, error) {
	r.active = true
	return func() error { *r.events = append(*r.events, "mount exited"); return nil }, nil
}
func (r *testMountResources) Close() error {
	*r.events = append(*r.events, "counters closed")
	return nil
}

type testAccountingMount struct {
	release  func() error
	mountErr error
}

func (m *testAccountingMount) Mount(ctx context.Context, _ bool) (executor.MountableRef, error) {
	var err error
	m.release, err = enginetel.PrepareExecMountCommand(ctx, &exec.Cmd{Path: "sshfs"})
	if err == nil && m.mountErr != nil {
		return nil, errors.Join(m.mountErr, m.release())
	}
	return &testAccountingMountRef{release: m.release}, err
}

type testAccountingMountRef struct{ release func() error }

func (m *testAccountingMountRef) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: "/tmp", Options: []string{"bind"}}}, m.release, nil
}

type testAccountingNamespace struct{ noopNetworkNamespace }

func (*testAccountingNamespace) Sample() (*resourcestypes.NetworkSample, error) {
	return &resourcestypes.NetworkSample{ScopeSupported: true}, nil
}

func TestExecMountResourceOwnershipAndCleanup(t *testing.T) {
	var events []string
	var path string
	enginetel.SetExecMountResourcesHook(func(p string) enginetel.ExecMountResources {
		path = p
		return &testMountResources{events: &events}
	})
	t.Cleanup(func() { enginetel.SetExecMountResourcesHook(nil) })
	truePath, err := exec.LookPath("true")
	require.NoError(t, err)
	client := &Client{Opts: &Opts{
		DefaultCgroupParent: "/workloads",
		ExecutorRoot:        t.TempDir(),
		Runc:                &runc.Runc{Command: truePath},
	}}
	state := &execState{
		id:               "exec-id",
		execMD:           &ExecutionMetadata{CallDigest: "xxh3:exec-call"},
		procInfo:         &executor.ProcessInfo{Meta: executor.Meta{Args: []string{"sh"}, Cwd: "/"}},
		mounts:           []executor.Mount{{Src: &testAccountingMount{}, Dest: "/data"}},
		networkNamespace: &testAccountingNamespace{},
		rootfsPath:       filepath.Join(t.TempDir(), "missing-rootfs"),
		cleanups:         &cleanups.Cleanups{},
	}
	require.NoError(t, client.generateBaseSpec(t.Context(), state))
	require.Equal(t, "/workloads/exec/exec-id", path)
	require.Equal(t, path, state.resourceCgroupPath)
	require.Equal(t, path+"/container", state.spec.Linux.CgroupsPath)
	// Exercise the real sampler setup and cleanup registration. A missing
	// rootfs stops execution before privileged mount or runc operations;
	// only the runtime's delete command is stubbed.
	require.ErrorContains(t, client.runContainer(t.Context(), state), "open rootfs path")
	require.NotNil(t, state.stopResourceSampler)
	t.Cleanup(state.stopResourceSampler)
	// Kernel workload handles still need a stand-in in this unit test.
	state.closeWorkloadNetwork = func() error {
		events = append(events, "workload counters closed")
		return nil
	}
	require.NoError(t, state.cleanups.Run())
	require.Equal(t, []string{"mount exited", "final sample", "workload counters closed", "counters closed"}, events)
}

func TestFailedExecSetupReportsMountResources(t *testing.T) {
	for _, mountFails := range []bool{true, false} {
		name := "later setup failure"
		if mountFails {
			name = "mount failure"
		}
		t.Run(name, func(t *testing.T) {
			var events []string
			enginetel.SetExecMountResourcesHook(func(string) enginetel.ExecMountResources {
				return &testMountResources{events: &events}
			})
			t.Cleanup(func() { enginetel.SetExecMountResourcesHook(nil) })
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
			span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
			ctx := trace.ContextWithSpanContext(telemetry.WithMeterProvider(t.Context(), provider), span)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			mnt := &testAccountingMount{}
			if mountFails {
				mnt.mountErr = errors.New("SSH authentication failed")
			}
			state := &execState{
				id:               "exec-id",
				execMD:           &ExecutionMetadata{CallDigest: "xxh3:exec-call"},
				procInfo:         &executor.ProcessInfo{Meta: executor.Meta{Args: []string{"sh"}, Cwd: "/"}},
				mounts:           []executor.Mount{{Src: mnt, Dest: "/data"}},
				networkNamespace: &noopNetworkNamespace{},
				cleanups:         &cleanups.Cleanups{},
			}
			client := &Client{Opts: &Opts{}}
			err := client.generateBaseSpec(ctx, state)
			if mountFails {
				require.ErrorIs(t, err, mnt.mountErr)
			} else {
				require.NoError(t, err)
			}
			// Stand in for the retained cgroup's files without requiring a
			// writable host cgroup mount. No container sampler was started.
			path := t.TempDir()
			for file, data := range map[string]string{
				"cpu.stat":       "usage_usec 15000\nuser_usec 10000\nsystem_usec 5000\n",
				"memory.current": "8192\n", "memory.peak": "16384\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(path, file), []byte(data), 0o600))
			}
			state.resourceCgroupPath, err = filepath.Rel("/sys/fs/cgroup", path)
			require.NoError(t, err)
			cancel()
			require.NoError(t, state.cleanups.Run())
			require.Equal(t, []string{"mount exited", "final sample", "counters closed"}, events)
			var data metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(context.Background(), &data))
			values := map[string]int64{}
			for _, scope := range data.ScopeMetrics {
				for _, m := range scope.Metrics {
					points := m.Data.(metricdata.Gauge[int64]).DataPoints
					require.Len(t, points, 1)
					id, ok := points[0].Attributes.Value(telemetry.MetricsSpanIDAttr)
					require.True(t, ok)
					require.Equal(t, span.SpanID().String(), id.AsString())
					values[m.Name] = points[0].Value
				}
			}
			require.EqualValues(t, 15000, values[telemetry.CPUStatUsage])
			require.EqualValues(t, 8192, values[telemetry.MemoryCurrentBytes])
			require.EqualValues(t, 16384, values[telemetry.MemoryPeakBytes])
			require.EqualValues(t, 37, values[telemetryattrs.NetworkExternalTxBytes])
			require.EqualValues(t, 1, values[telemetryattrs.NetworkAvailable])
		})
	}
}

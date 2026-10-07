package engineutil

import (
	"context"
	"errors"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"github.com/dagger/dagger/engine/engineutil/resources"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// cgroupMountpoint is where the engine sees the unified cgroup hierarchy that
// OCI cgroup paths are relative to.
const cgroupMountpoint = "/sys/fs/cgroup"

// workloadNetnsCookie returns the cookie of the network namespace the
// workload will run in, before runc creates its container.
func workloadNetnsCookie(ctx context.Context, state *execState) (uint64, error) {
	switch state.procInfo.Meta.NetMode {
	case pb.NetMode_NONE:
		// runc creates a namespace with only loopback, which is not counted.
		return 0, errors.New("workload has no network")
	case pb.NetMode_HOST:
		// The workload shares the engine's namespace.
		return nettracer.CurrentNetnsCookie()
	}
	if state.networkNamespace == nil {
		return 0, errors.New("workload has no network namespace")
	}
	var spec specs.Spec
	if err := state.networkNamespace.Set(&spec); err != nil {
		return 0, err
	}
	hasPath := false
	if spec.Linux != nil {
		for _, ns := range spec.Linux.Namespaces {
			if ns.Type == specs.NetworkNamespace && ns.Path != "" {
				hasPath = true
			}
		}
	}
	if !hasPath {
		return 0, errors.New("workload network namespace has no path")
	}
	return runInNetNS(ctx, state, nettracer.CurrentNetnsCookie)
}

// workloadNetworkSampler adds a workload's attributed eBPF counters to its
// network namespace's interface totals.
type workloadNetworkSampler struct {
	netNS    resources.BKNetworkSampler
	workload *nettracer.Workload
}

func (s workloadNetworkSampler) Sample() (*resourcestypes.NetworkSample, error) {
	sample := &resourcestypes.NetworkSample{}
	if s.netNS != nil {
		nsSample, err := s.netNS.Sample()
		if err != nil {
			return nil, err
		}
		if nsSample != nil {
			*sample = *nsSample
		}
	}
	attributed, err := s.workload.Sample()
	if err != nil {
		bklog.L.Debugf("attributed network accounting sample unavailable: %s", err)
		return sample, nil
	}
	sample.InternalRxBytes = int64(attributed.InternalRX)
	sample.InternalTxBytes = int64(attributed.InternalTX)
	sample.ExternalRxBytes = int64(attributed.ExternalRX)
	sample.ExternalTxBytes = int64(attributed.ExternalTX)
	sample.ScopeSupported = true
	return sample, nil
}

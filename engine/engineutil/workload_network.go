package engineutil

import (
	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"github.com/dagger/dagger/engine/engineutil/resources"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

// cgroupMountpoint is where the engine sees the unified cgroup hierarchy that
// OCI cgroup paths are relative to.
const cgroupMountpoint = "/sys/fs/cgroup"

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

package ctrns

import (
	"github.com/containerd/containerd/v2/core/leases"
	snapshots "github.com/dagger/dagger/engine/snapshots"
)

type LeasesManagerNamespace = snapshots.LeaseManager

func LeasesWithNamespace(leases leases.Manager, ns string) leases.Manager {
	return snapshots.NewLeaseManager(leases, ns)
}

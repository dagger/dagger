package ctrns

import (
	"github.com/containerd/containerd/leases"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
)

type LeasesManagerNamespace = leaseutil.Manager

func LeasesWithNamespace(leases leases.Manager, ns string) leases.Manager {
	return leaseutil.WithNamespace(leases, ns)
}

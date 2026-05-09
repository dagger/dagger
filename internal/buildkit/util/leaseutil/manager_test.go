package leaseutil

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/stretchr/testify/require"
)

type testLeaseManager struct {
	mu        sync.Mutex
	created   int
	deleted   int
	resources map[string][]leases.Resource
}

func (lm *testLeaseManager) Create(_ context.Context, opts ...leases.Opt) (leases.Lease, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	l := leases.Lease{ID: fmt.Sprintf("lease-%d", lm.created+1)}
	for _, opt := range opts {
		if err := opt(&l); err != nil {
			return leases.Lease{}, err
		}
	}
	if l.ID == "" {
		l.ID = fmt.Sprintf("lease-%d", lm.created+1)
	}
	lm.created++
	return l, nil
}

func (lm *testLeaseManager) Delete(_ context.Context, _ leases.Lease, _ ...leases.DeleteOpt) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.deleted++
	return nil
}

func (lm *testLeaseManager) List(context.Context, ...string) ([]leases.Lease, error) {
	return nil, nil
}

func (lm *testLeaseManager) AddResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.resources == nil {
		lm.resources = map[string][]leases.Resource{}
	}
	lm.resources[lease.ID] = append(lm.resources[lease.ID], resource)
	return nil
}

func (lm *testLeaseManager) DeleteResource(context.Context, leases.Lease, leases.Resource) error {
	return nil
}

func (lm *testLeaseManager) ListResources(_ context.Context, lease leases.Lease) ([]leases.Resource, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return append([]leases.Resource(nil), lm.resources[lease.ID]...), nil
}

func TestLazyLeaseCreatesOnEnsure(t *testing.T) {
	lm := &testLeaseManager{}
	ctx, release, err := WithLazyLease(context.Background(), lm)
	require.NoError(t, err)
	require.False(t, HasLazyLease(context.Background()))
	require.True(t, HasLazyLease(ctx))
	require.Equal(t, 0, lm.created)

	leaseCtx, err := EnsureLease(ctx)
	require.NoError(t, err)
	leaseID, ok := leases.FromContext(leaseCtx)
	require.True(t, ok)
	require.NotEmpty(t, leaseID)
	require.Equal(t, 1, lm.created)

	leaseCtx, err = EnsureLease(ctx)
	require.NoError(t, err)
	leaseID2, ok := leases.FromContext(leaseCtx)
	require.True(t, ok)
	require.Equal(t, leaseID, leaseID2)
	require.Equal(t, 1, lm.created)

	require.NoError(t, release(context.Background()))
	require.Equal(t, 1, lm.deleted)
}

func TestLazyLeaseReleaseWithoutEnsureDoesNotCreate(t *testing.T) {
	lm := &testLeaseManager{}
	_, release, err := WithLazyLease(context.Background(), lm)
	require.NoError(t, err)
	require.NoError(t, release(context.Background()))
	require.Equal(t, 0, lm.created)
	require.Equal(t, 0, lm.deleted)
}

func TestWithoutLazyLeaseSuppressesEnsure(t *testing.T) {
	lm := &testLeaseManager{}
	ctx, release, err := WithLazyLease(context.Background(), lm)
	require.NoError(t, err)
	defer release(context.Background())

	ctx = WithoutLazyLease(ctx)
	leaseCtx, err := EnsureLease(ctx)
	require.NoError(t, err)
	leaseID, ok := leases.FromContext(leaseCtx)
	require.False(t, ok && leaseID != "")
	require.Equal(t, 0, lm.created)
}

package resolver

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
)

const imagePullLeaseLabel = "dagger.io/image-pull"

// A pull owns its lease until the caller imports the image and releases it.
// Neither a fixed expiry nor an ambient caller lease can cover that lifetime.
func (r *Resolver) newPullLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
	l, err := r.leaseManager.Create(ctx, leases.WithRandomID(), leases.WithLabels(map[string]string{
		imagePullLeaseLabel: "true",
	}))
	if err != nil {
		return nil, nil, err
	}
	return leases.WithLease(ctx, l.ID), func(ctx context.Context) error {
		err := r.leaseManager.Delete(context.WithoutCancel(ctx), l)
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}, nil
}

// ReleasePullLeasesAfterRestart removes leases whose owners died with the
// previous engine. Call after restoring durable owners and before serving work.
func ReleasePullLeasesAfterRestart(ctx context.Context, lm leases.Manager) error {
	previous, err := lm.List(ctx)
	if err != nil {
		return fmt.Errorf("list image pull leases: %w", err)
	}
	var rerr error
	for _, l := range previous {
		if l.Labels[imagePullLeaseLabel] != "true" {
			continue
		}
		if err := lm.Delete(ctx, l); err != nil && !cerrdefs.IsNotFound(err) {
			rerr = errors.Join(rerr, fmt.Errorf("release previous image pull lease %s: %w", l.ID, err))
		}
	}
	return rerr
}

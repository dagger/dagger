package snapshots

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	telemetry "github.com/dagger/otel-go"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// maxLayers is the most layers a snapshot's chain holds under an overlay
// snapshotter. Mounting a snapshot lists each layer of its chain as an overlay
// lower directory, and the mount fails past 500 of them (the kernel's limit)
// or past one page of mount options (containerd's, which names each lower
// directory relative to the snapshots directory: "35873/fs:" for a 5-digit
// snapshot ID, so about 440 layers). 128 layers fit one page whatever the
// length of the snapshot IDs.
const maxLayers = 128

// flattenedSuffix names a parent's flattened copy after the parent.
const flattenedSuffix = "-flattened"

// Prepare prepares key on parent. When parent's chain already holds maxLayers
// layers, it prepares key on a flattened copy of parent instead: a single
// layer with parent's files, hard-linked from parent's layers where the
// snapshotter can. So no series of edits outgrows what a mount can stack.
// Every snapshot prepared on parent shares its one flattened copy.
func (sn *mergeSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) (rerr error) {
	if parent == "" || !sn.overlayBased {
		return sn.Snapshotter.Prepare(ctx, key, parent, opts...)
	}
	layers, err := sn.chainLayers(ctx, parent)
	if err != nil {
		return err
	}
	if layers < maxLayers {
		return sn.Snapshotter.Prepare(ctx, key, parent, opts...)
	}

	// The flattened copy is kept by the lease until key, its child, keeps it.
	ctx, err = EnsureLease(ctx)
	if err != nil {
		return errors.Wrap(err, "ensure lease for snapshot flatten")
	}
	ctx, done, err := WithLease(ctx, sn.lm, MakeTemporary)
	if err != nil {
		return errors.Wrap(err, "create temporary lease for snapshot flatten")
	}
	defer func() {
		if err := done(context.WithoutCancel(ctx)); err != nil && rerr == nil {
			rerr = errors.Wrap(err, "release temporary lease for snapshot flatten")
		}
	}()
	flattened, err := sn.flatten(ctx, parent, layers)
	if err != nil {
		return err
	}
	return sn.Snapshotter.Prepare(ctx, key, flattened, opts...)
}

// chainLayers counts the layers of key's chain, up to maxLayers.
func (sn *mergeSnapshotter) chainLayers(ctx context.Context, key string) (int, error) {
	layers := 0
	for ; key != "" && layers < maxLayers; layers++ {
		info, err := sn.Stat(ctx, key)
		if err != nil {
			return 0, errors.Wrapf(err, "stat snapshot %s", key)
		}
		key = info.Parent
	}
	return layers, nil
}

// flatten returns the key of parent's flattened copy, making it unless it
// exists. ctx must have a lease, which keeps the copy.
func (sn *mergeSnapshotter) flatten(ctx context.Context, parent string, layers int) (_ string, rerr error) {
	key := parent + flattenedSuffix
	release, err := sn.flattenLocker.acquire(ctx, key)
	if err != nil {
		return "", err
	}
	defer release()

	// The lease keeps an existing copy from being collected between Stat and
	// the Prepare on it.
	leaseID, _ := leases.FromContext(ctx)
	if err := sn.lm.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
		ID:   key,
		Type: "snapshots/" + sn.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return "", errors.Wrapf(err, "add flattened snapshot %s to lease", key)
	}
	if _, err := sn.Stat(ctx, key); err == nil {
		return key, nil
	} else if !cerrdefs.IsNotFound(err) {
		return "", errors.Wrapf(err, "stat flattened snapshot %s", key)
	}

	ctx, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(instrumentationLibrary).
		Start(ctx, "flatten snapshot", telemetry.Internal(), trace.WithAttributes(
			attribute.String("dagger.snapshot.flatten.parent", parent),
			attribute.Int("dagger.snapshot.flatten.layers", layers),
		))
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("dagger.snapshot.flatten.ms", time.Since(start).Milliseconds()))
		telemetry.EndWithCause(span, &rerr)
	}()
	// Merging parent alone applies its whole tree, root included, onto an
	// empty snapshot.
	if err := sn.Merge(ctx, key, []Diff{{Upper: parent, Root: true}}); err != nil {
		return "", errors.Wrapf(err, "flatten snapshot %s", parent)
	}
	return key, nil
}

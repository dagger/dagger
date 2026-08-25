package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"slices"

	ctdleases "github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// snapshotWritableMount commits the current generation of a live writable
// service mount and immediately remounts a fresh mutable child. The returned
// Directory is immutable; later service writes cannot mutate it.
func (running *RunningService) snapshotWritableMount(
	ctx context.Context,
	target string,
	source dagql.ObjectResult[*Directory],
	srv *dagql.Server,
) (res dagql.ObjectResult[*Directory], rerr error) {
	if running == nil {
		return res, fmt.Errorf("running service is nil")
	}
	running.workspaceMu.Lock()
	defer running.workspaceMu.Unlock()
	ctx, err := running.workspaceLeaseContext(ctx)
	if err != nil {
		return res, err
	}

	state, err := running.writableMountStateLocked(target)
	if err != nil {
		return res, err
	}
	if state.ActiveRef == nil {
		return res, fmt.Errorf("writable service mount %q has no active generation", target)
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return res, err
	}
	if err := cache.Evaluate(ctx, source); err != nil {
		return res, fmt.Errorf("evaluate service mount source: %w", err)
	}
	dirPath, err := source.Self().Dir.GetOrEval(ctx, source.Result)
	if err != nil {
		return res, fmt.Errorf("get service mount source path: %w", err)
	}

	snapshot, err := state.ActiveRef.Commit(ctx)
	if err != nil {
		return res, fmt.Errorf("commit writable service mount %q: %w", target, err)
	}
	state.ActiveRef = nil
	defer func() {
		if rerr != nil && snapshot != nil {
			_ = snapshot.Release(context.WithoutCancel(ctx))
		}
	}()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	next, err := query.SnapshotManager().New(ctx, snapshot,
		bkcache.WithDescription("service workspace generation"))
	if err != nil {
		return res, fmt.Errorf("create next writable service mount %q: %w", target, err)
	}
	if err := running.ProtectRef(ctx, next); err != nil {
		_ = next.Release(context.WithoutCancel(ctx))
		return res, fmt.Errorf("protect next writable service mount %q: %w", target, err)
	}
	if err := remountServiceDirectory(ctx, running.ContainerID, target, next, dirPath); err != nil {
		_ = next.Release(context.WithoutCancel(ctx))
		return res, err
	}
	state.ActiveRef = next

	dir := &Directory{
		Platform: source.Self().Platform,
		Services: slices.Clone(source.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue(dirPath)
	dir.Snapshot.setValue(snapshot)

	if srv == nil {
		return res, fmt.Errorf("service workspace DAGQL server is required")
	}
	res, err = dagql.NewObjectResultForCall(dir, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		Type:        dagql.NewResultCallType(dir.Type()),
		SyntheticOp: "service_workspace_snapshot",
		ImplicitInputs: []*dagql.ResultCallArg{
			{
				Name: "workspaceGenerationNonce",
				Value: &dagql.ResultCallLiteral{
					Kind:        dagql.ResultCallLiteralKindString,
					StringValue: rand.Text(),
				},
			},
		},
	})
	if err != nil {
		return res, err
	}
	snapshot = nil // owned by the synthetic Directory result
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return res, fmt.Errorf("get service workspace client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return res, fmt.Errorf("attach service workspace snapshot: empty session ID")
	}
	attached, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, res)
	if err != nil {
		return res, fmt.Errorf("attach service workspace snapshot: %w", err)
	}
	res, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return res, fmt.Errorf("attach service workspace snapshot: expected Directory, got %T", attached)
	}
	return res, nil
}

// replaceWritableMount advances a live service mount to an immutable Directory
// produced by Dagger, leaving the service with a mutable child generation.
func (running *RunningService) replaceWritableMount(
	ctx context.Context,
	target string,
	source dagql.ObjectResult[*Directory],
) (rerr error) {
	if running == nil {
		return fmt.Errorf("running service is nil")
	}
	running.workspaceMu.Lock()
	defer running.workspaceMu.Unlock()
	ctx, err := running.workspaceLeaseContext(ctx)
	if err != nil {
		return err
	}

	state, err := running.writableMountStateLocked(target)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, source); err != nil {
		return fmt.Errorf("evaluate replacement service mount source: %w", err)
	}
	ref, err := source.Self().Snapshot.GetOrEval(ctx, source.Result)
	if err != nil {
		return fmt.Errorf("get replacement service mount snapshot: %w", err)
	}
	dirPath, err := source.Self().Dir.GetOrEval(ctx, source.Result)
	if err != nil {
		return fmt.Errorf("get replacement service mount path: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	next, err := query.SnapshotManager().New(ctx, ref,
		bkcache.WithDescription("service workspace replacement"))
	if err != nil {
		return fmt.Errorf("create replacement writable service mount %q: %w", target, err)
	}
	defer func() {
		if rerr != nil && next != nil {
			_ = next.Release(context.WithoutCancel(ctx))
		}
	}()
	if err := running.ProtectRef(ctx, next); err != nil {
		return fmt.Errorf("protect replacement writable service mount %q: %w", target, err)
	}
	if err := remountServiceDirectory(ctx, running.ContainerID, target, next, dirPath); err != nil {
		return err
	}

	previous := state.ActiveRef
	state.ActiveRef = next
	next = nil
	if previous != nil {
		if err := previous.Release(context.WithoutCancel(ctx)); err != nil {
			slog.Warn("failed to release previous writable service mount", "target", target, "error", err)
		}
	}
	return nil
}

func (running *RunningService) writableMountStateLocked(target string) (*execMountState, error) {
	for _, state := range running.mountStates {
		if state.Dest == target {
			if state.Readonly {
				return nil, fmt.Errorf("service mount %q is read-only", target)
			}
			return state, nil
		}
	}
	return nil, fmt.Errorf("service mount %q is not available", target)
}

func (running *RunningService) workspaceLeaseContext(ctx context.Context) (context.Context, error) {
	running.refsMu.Lock()
	leaseID := running.resourceLeaseID
	running.refsMu.Unlock()
	if leaseID == "" {
		return nil, fmt.Errorf("service resource lease is not initialized")
	}
	return ctdleases.WithLease(ctx, leaseID), nil
}

func remountServiceDirectory(ctx context.Context, containerID, target string, ref bkcache.MutableRef, dirPath string) error {
	return MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return fmt.Errorf("resolve service mount source %q: %w", dirPath, err)
		}
		if err := mountIntoContainer(ctx, containerID, resolvedDir, target); err != nil {
			return fmt.Errorf("remount service directory at %q: %w", target, err)
		}
		return nil
	})
}

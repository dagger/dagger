package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type privateLazyOperation struct {
	run     func(context.Context) error
	capture func(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error)
	release func(context.Context) error
}

func (p *privateLazyOperation) Run(ctx context.Context) error { return p.run(ctx) }
func (p *privateLazyOperation) Capture(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return p.capture(ctx, enc)
}
func (p *privateLazyOperation) Release(ctx context.Context) error { return p.release(ctx) }
func (family foreignFamilyCodec) PrepareLazyOperation(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, route dagql.LazyOperationRoute) (dagql.LazyOperationInvocation, error) {
	if !route.HasLazyOperation {
		return nil, fmt.Errorf("missing saved operation")
	}
	ctx = dagql.ContextWithCall(ctx, record.Call)
	switch family {
	case "Directory":
		var p persistedDirectoryPayload
		if err := json.Unmarshal(record.Envelope.ObjectJSON, &p); err != nil {
			return nil, err
		}
		lazy, err := decodePersistedDirectoryLazy(ctx, dec, p.LazyKind, p.LazyJSON)
		if err != nil {
			return nil, err
		}
		services, err := decodePersistedServiceBindings(ctx, dec, "private Directory", p.Services)
		if err != nil {
			return nil, err
		}
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: p.Platform, Services: services, Lazy: lazy}
		return &privateLazyOperation{run: func(ctx context.Context) error { return dir.LazyEvalFunc()(ctx) }, capture: dir.EncodePersistedObject, release: dir.OnRelease}, nil
	case "File":
		var p persistedFilePayload
		if err := json.Unmarshal(record.Envelope.ObjectJSON, &p); err != nil {
			return nil, err
		}
		lazy, err := decodePersistedFileLazy(ctx, dec, p.LazyKind, p.LazyJSON)
		if err != nil {
			return nil, err
		}
		services, err := decodePersistedServiceBindings(ctx, dec, "private File", p.Services)
		if err != nil {
			return nil, err
		}
		file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: p.Platform, Services: services, Lazy: lazy}
		return &privateLazyOperation{run: func(ctx context.Context) error { return file.LazyEvalFunc()(ctx) }, capture: file.EncodePersistedObject, release: file.OnRelease}, nil
	case "Container":
		var p persistedContainerPayload
		if err := json.Unmarshal(record.Envelope.ObjectJSON, &p); err != nil {
			return nil, err
		}
		if record.Call == nil {
			return nil, fmt.Errorf("private Container: missing recorded call")
		}
		lazy, err := decodePersistedContainerRecipe(ctx, dec, record.Call, p.LazyJSON)
		if err != nil {
			return nil, err
		}
		// Decode scalar configuration and exact handles into a private shell. The
		// recipe's own fresh latches compute metadata again inside Running.
		p.OperationState = transferOperationState(false, true)
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		typed, err := (*Container)(nil).DecodePersistedObject(ctx, dec, raw)
		if err != nil {
			return nil, err
		}
		ctr := typed.(*Container)
		ctr.acquiredOutput.Store(nil)
		ctr.Lazy = lazy
		run := func(ctx context.Context) (rerr error) {
			// Only the gated integration fixture observes the private FS handle.
			// Its wrapper reports after the real ref's Release returns.
			if observe := dagql.TransferFixtureLazyReleaseObserver(ctx); observe != nil {
				defer func() {
					if rerr == nil {
						observePrivateContainerFSRelease(ctr, observe)
					}
				}()
			}
			ctx = dagql.ContextWithCall(ctx, record.Call)
			if route.Group.Group == dagql.LazyGroupWhole {
				return ctr.Evaluate(ctx)
			}
			op, ok := lazy.(LazyContainerParts)
			if !ok {
				return fmt.Errorf("private Container: refined route has whole operation")
			}
			if err := ctr.runLazyGroup(ctx, op, ContainerLazyGroupMetadata); err != nil {
				return err
			}
			if route.Group.Group != ContainerLazyGroupMetadata {
				return ctr.runLazyGroup(ctx, op, route.Group.Group)
			}
			return nil
		}
		return &privateLazyOperation{run: run, capture: ctr.EncodePersistedObject, release: ctr.OnRelease}, nil
	default:
		return nil, fmt.Errorf("no saved operation for %s", family)
	}
}

type partFixtureReleaseRef struct {
	bkcache.ImmutableRef
	observe func(string, error)
}

func (r *partFixtureReleaseRef) Release(ctx context.Context) error {
	id := r.SnapshotID()
	err := r.ImmutableRef.Release(ctx)
	r.observe(id, err)
	return err
}

// Only the private operation's FS handle is decorated; installed receiver,
// metadata and mount accessors retain their own concrete ref types.
func observePrivateContainerFSRelease(ctr *Container, observe func(string, error)) {
	if dir, ok := ctr.FS.Peek(); ok && dir != nil {
		if ref, ok := dir.Snapshot.Peek(); ok && ref != nil {
			dir.Snapshot.setValue(&partFixtureReleaseRef{ImmutableRef: ref, observe: observe})
		}
	}
}

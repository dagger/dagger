package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// Keep the real Container routing and LazyState latches, with controlled
// bodies so capture can be attempted between a mutation and completion.
type captureContainerGroupsOp struct {
	ContainerWithLabelLazy
	body        func(*Container, dagql.LazyGroupKey)
	encode      func() error
	encodeCalls atomic.Int32
}

func (op *captureContainerGroupsOp) Evaluate(ctx context.Context, ctr *Container) error {
	return ctr.evaluateAllLazyGroups(ctx, op)
}

func (op *captureContainerGroupsOp) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	return op.LazyState.EvaluateGroup(ctx, "capture-test", group, func(context.Context) error {
		if op.body != nil {
			op.body(ctr, group)
		}
		return nil
	})
}

func (op *captureContainerGroupsOp) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	op.encodeCalls.Add(1)
	if op.encode != nil {
		if err := op.encode(); err != nil {
			return nil, err
		}
	}
	return op.ContainerWithLabelLazy.EncodePersisted(ctx, enc)
}

func newCaptureContainer(t *testing.T) (context.Context, *dagql.Cache, dagql.ObjectResult[*Container], *captureContainerGroupsOp) {
	t.Helper()
	ctx, cache, srv, session := newContainerPartsTestCtx(t)
	platform := Platform{OS: "linux", Architecture: "amd64"}
	parentCtr := NewContainer(platform)
	// Keep parent parts pending so the final-delegation sweep does not
	// force the deliberately parked sibling body during another demand.
	parentCtr.Lazy = &containerPartsTestBaseOp{LazyState: NewLazyState()}
	parent := attachContainerPartsTestResult(t, ctx, cache, srv, session, "capture-parent", parentCtr)
	ctr := NewContainer(platform)
	op := &captureContainerGroupsOp{ContainerWithLabelLazy: ContainerWithLabelLazy{
		LazyState: NewLazyState(), Parent: parent, Name: "capture", Value: "inputs",
	}}
	ctr.Lazy = op
	res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "capture-child", ctr)
	return ctx, cache, res, op
}

func TestCapturePersistedContainerDirectEvaluation(t *testing.T) {
	for _, test := range []struct {
		name  string
		part  dagql.PartKey
		whole bool
	}{
		{name: "metadata", part: ContainerPartMetadata},
		{name: "snapshot", part: ContainerPartFS},
		{name: "whole", part: ContainerPartMetadata, whole: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cache, res, op := newCaptureContainer(t)
			ctr := res.Self()
			entered, allow := make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(allow) })
			defer finish()
			op.body = func(ctr *Container, group dagql.LazyGroupKey) {
				if group != dagql.LazyGroupKey(test.part) {
					return
				}
				if test.part == ContainerPartMetadata {
					ctr.Config.WorkingDir = "/half-written"
				} else {
					ctr.FS.SetValue(containerPersistenceTestDirectory("half-written", "/"))
				}
				close(entered)
				<-allow
				if test.part == ContainerPartMetadata {
					ctr.Config.WorkingDir = "/final"
				} else {
					ctr.FS.SetValue(containerPersistenceTestDirectory("final", "/"))
				}
			}
			before, err := cache.CapturePersistedRecord(ctx, res)
			require.NoError(t, err)
			select {
			case <-entered:
				t.Fatal("capture started evaluation")
			default:
			}
			calls := op.encodeCalls.Load()
			done := make(chan error, 1)
			go func() {
				if test.whole {
					done <- ctr.Evaluate(ctx)
				} else {
					done <- ctr.evaluatePartsDirect(ctx, test.part)
				}
			}()
			<-entered
			_, err = cache.CapturePersistedRecord(ctx, res)
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
			require.Equal(t, calls, op.encodeCalls.Load(), "busy bodies must not reach recipe encoding")
			if test.part == ContainerPartFS {
				// A refused capture releases its locks, and a different group
				// completes while the filesystem body remains parked.
				require.NoError(t, ctr.evaluatePartsDirect(ctx, ContainerPartExecMeta))
				require.True(t, op.GroupConsumed(dagql.LazyGroupKey(ContainerPartExecMeta)))
			}
			finish()
			require.NoError(t, <-done)
			after, err := cache.CapturePersistedRecord(ctx, res)
			require.NoError(t, err)
			var initial, final persistedContainerPayload
			require.NoError(t, json.Unmarshal(before.Envelope.ObjectJSON, &initial))
			require.NoError(t, json.Unmarshal(after.Envelope.ObjectJSON, &final))
			require.JSONEq(t, string(initial.LazyJSON), string(final.LazyJSON))
			if test.part == ContainerPartMetadata {
				require.Equal(t, "/final", final.Metadata.Value.Config.WorkingDir)
				require.True(t, final.Metadata.Consumed)
			} else {
				require.Contains(t, after.SnapshotLinks, dagql.PersistedSnapshotRefLink{Role: "fs", RefKey: "final"})
			}
		})
	}
}

func TestCapturePersistedContainerExcludesNewDirectGroups(t *testing.T) {
	ctx, cache, res, op := newCaptureContainer(t)
	entered, allow := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(allow) })
	defer finish()
	op.encode = func() error { close(entered); <-allow; return nil }
	var ran atomic.Bool
	op.body = func(ctr *Container, group dagql.LazyGroupKey) {
		if group == ContainerLazyGroupMetadata {
			ran.Store(true)
			ctr.Config.WorkingDir = "/after-capture"
		}
	}
	captured := make(chan error, 1)
	go func() { _, err := cache.CapturePersistedRecord(ctx, res); captured <- err }()
	<-entered
	requested, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(requested)
		done <- res.Self().evaluatePartsDirect(ctx, ContainerPartMetadata)
	}()
	<-requested
	// A mutex wait isn't a durable synctest wait. Check the held latch
	// directly: all new EvaluateGroup runners must pass it before running.
	locked := op.LazyMu.TryLock()
	if locked {
		op.LazyMu.Unlock()
	}
	require.False(t, locked)
	require.False(t, ran.Load())
	finish()
	require.NoError(t, <-captured)
	require.NoError(t, <-done)
	require.True(t, ran.Load())
}

type captureContainerWholeOp struct {
	LazyState
	body func(*Container)
}

func (op *captureContainerWholeOp) Evaluate(ctx context.Context, ctr *Container) error {
	return op.LazyState.Evaluate(ctx, "capture-whole-test", func(context.Context) error {
		op.body(ctr)
		return nil
	})
}

func (*captureContainerWholeOp) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*captureContainerWholeOp) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return json.RawMessage(`{"input":"whole"}`), nil
}

func TestCapturePersistedContainerUnrefinedBody(t *testing.T) {
	for _, outputsSet := range []bool{false, true} {
		name := "active"
		if outputsSet {
			name = "outputs-set-before-return"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cache, srv, session := newContainerPartsTestCtx(t)
			entered, allow := make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(allow) })
			defer finish()
			op := &captureContainerWholeOp{LazyState: NewLazyState(), body: func(ctr *Container) {
				ctr.Config.WorkingDir = "/half-written"
				if outputsSet {
					ctr.Config.WorkingDir = "/final"
				}
				close(entered)
				<-allow
				if !outputsSet {
					ctr.Config.WorkingDir = "/final"
				}
			}}
			ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
			ctr.Lazy = op
			res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "capture-whole", ctr)
			done := make(chan error, 1)
			go func() { done <- ctr.Evaluate(ctx) }()
			<-entered
			_, err := cache.CapturePersistedRecord(ctx, res)
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
			finish()
			require.NoError(t, <-done)
			rec, err := cache.CapturePersistedRecord(ctx, res)
			require.NoError(t, err)
			var payload persistedContainerPayload
			require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &payload))
			require.Equal(t, "/final", payload.Metadata.Value.Config.WorkingDir)
			require.True(t, payload.Metadata.Consumed)
		})
	}
}

func TestCapturePersistedContainerReleasesLocksOnCodecError(t *testing.T) {
	ctx, cache, res, op := newCaptureContainer(t)
	require.NoError(t, res.Self().evaluatePartsDirect(ctx, ContainerPartMetadata))
	injected := errors.New("recipe encoding failed")
	op.encode = func() error { return injected }
	_, err := cache.CapturePersistedRecord(ctx, res)
	require.ErrorIs(t, err, injected)
	op.encode = nil
	require.NoError(t, res.Self().Evaluate(ctx))
	_, err = cache.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
}

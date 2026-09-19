package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type syncSnapshotOwnerValue struct {
	persistSnapshotValue
	reads int
	err   error
}

func (*syncSnapshotOwnerValue) PersistedOutputRevision() (OutputRevision, error) {
	return 0, ErrPersistStateNotReady
}

func (v *syncSnapshotOwnerValue) ReadSnapshotOwner() (OutputRevision, []PersistedSnapshotRefLink, error) {
	v.reads++
	return 1, v.PersistedSnapshotRefLinks(), v.err
}

func TestSnapshotOwnerSyncReadSelection(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", &fakeSnapshotManager{}, nil)
	require.NoError(t, err)
	session := cacheTestSessionID(t, ctx)
	value := &syncSnapshotOwnerValue{persistSnapshotValue: persistSnapshotValue{SnapshotID: "owned"}}
	call := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(value.Type()), Field: "owner"}
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	require.NoError(t, err, "fresh publication uses the blocking read")
	require.Equal(t, 1, value.reads, "links are coherent under the reader's publication guard")
	_, err = desiredSnapshotLinksForResult(res.cacheSharedResult(), false)
	require.ErrorIs(t, err, ErrPersistStateNotReady, "boot/import retain the nonblocking collector")
	require.Equal(t, 1, value.reads)
	require.NoError(t, c.SyncResultSnapshotOwnerLeases(ctx, res))
	require.Equal(t, 2, value.reads)
	value.err = errors.New("ownership read failed")
	require.ErrorIs(t, c.SyncResultSnapshotOwnerLeases(ctx, res), value.err, "real errors retain their bookkeeping meaning")
	value.err = nil
	require.NoError(t, c.ReleaseSession(ctx, session))
	require.NoError(t, c.Close(ctx))
}

func TestSnapshotOwnerSyncInlineRead(t *testing.T) {
	value := &syncSnapshotOwnerValue{persistSnapshotValue: persistSnapshotValue{SnapshotID: "owned"}}
	self := DynamicResultArrayOutput{Elem: value, Values: []AnyResult{newDetachedResult(nil, value)}}
	frame := persistCodecFrame("inline", self)
	_, err := collectSnapshotOwnerLinks(self, frame, false)
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	links, err := collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, PersistedRefPath{}.Field("items").Index(0), links[0].OutputPath)
	require.Equal(t, 1, value.reads)
}

// changingSnapshotOwnerValue returns an atomic revision/link snapshot, then
// permits another publication before its caller can validate that revision.
type changingSnapshotOwnerValue struct {
	persistSnapshotValue
	mu          sync.Mutex
	revision    OutputRevision
	read        chan struct{}
	published   chan struct{}
	readOnce    sync.Once
	publishOnce sync.Once
	waitCtx     context.Context
	readErr     error
}

func (v *changingSnapshotOwnerValue) PersistedOutputRevision() (OutputRevision, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.revision, nil
}

func (v *changingSnapshotOwnerValue) ReadSnapshotOwner() (OutputRevision, []PersistedSnapshotRefLink, error) {
	v.mu.Lock()
	revision := v.revision
	links := v.persistSnapshotValue.PersistedSnapshotRefLinks()
	v.mu.Unlock()
	if v.read != nil {
		v.readOnce.Do(func() {
			close(v.read)
			v.readErr = waitSnapshotOwnerSignal(v.waitCtx, v.published, "typed publication")
		})
	}
	return revision, links, v.readErr
}

func waitSnapshotOwnerSignal(ctx context.Context, ch <-chan struct{}, description string) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for %s: %w", description, context.Cause(ctx))
	}
}

// Cleanup releases both handoffs and joins the writer even if an assertion
// fails before the reader runs. The writer reports errors to the test goroutine.
func startSnapshotOwnerWriter(t *testing.T, ctx context.Context, value *changingSnapshotOwnerValue, afterPublish func(context.Context) error) func() error {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	value.waitCtx = waitCtx
	value.read, value.published = make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	var writerErr error
	go func() {
		defer close(done)
		if writerErr = waitSnapshotOwnerSignal(waitCtx, value.read, "snapshot owner read"); writerErr != nil {
			return
		}
		value.mu.Lock()
		value.revision++
		value.SnapshotID = "after"
		value.mu.Unlock()
		value.publishOnce.Do(func() { close(value.published) })
		if afterPublish != nil {
			writerErr = afterPublish(waitCtx)
		}
	}()
	t.Cleanup(func() {
		cancel()
		value.publishOnce.Do(func() { close(value.published) })
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if err := waitSnapshotOwnerSignal(cleanupCtx, done, "snapshot owner writer cleanup"); err != nil {
			t.Error(err)
		}
	})
	return func() error {
		if err := waitSnapshotOwnerSignal(waitCtx, done, "snapshot owner writer completion"); err != nil {
			return err
		}
		return writerErr
	}
}

func TestSnapshotOwnerPublicationUsesCoherentRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(cacheTestContext(t.Context()), 5*time.Second)
	defer cancel()
	manager := &fakeSnapshotManager{}
	c, err := NewCache(ctx, "", manager, nil)
	require.NoError(t, err)
	session := cacheTestSessionID(t, ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		done := make(chan error, 1)
		go func() {
			done <- errors.Join(c.ReleaseSession(cleanupCtx, session), c.Close(cleanupCtx))
		}()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-cleanupCtx.Done():
			t.Errorf("snapshot owner cache cleanup: %v", context.Cause(cleanupCtx))
		}
	})
	value := &changingSnapshotOwnerValue{
		persistSnapshotValue: persistSnapshotValue{SnapshotID: "before"},
		revision:             1,
	}
	original, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: persistCodecFrame("original", value)}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	require.NoError(t, err)

	joinWriter := startSnapshotOwnerWriter(t, ctx, value, func(writerCtx context.Context) error {
		// A completed writer must still reconcile its own publication.
		return c.SyncResultSnapshotOwnerLeases(writerCtx, original)
	})
	// Like from(tag) returning from(digest), this call adopts a result
	// that another consumer can already evaluate and publish into.
	alias, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: persistCodecFrame("alias", value)}, func(context.Context) (AnyResult, error) {
		return original, nil
	})
	require.NoError(t, joinWriter())
	require.NoError(t, err, "a later typed publication must not turn a coherent owner read into a call error")
	require.Same(t, original.cacheSharedResult(), alias.cacheSharedResult())
	links := original.cacheSharedResult().loadSnapshotOwnerLinks()
	require.Len(t, links, 1)
	require.Equal(t, "after", links[0].RefKey, "the writer reconciles the newer publication after the earlier sync")
}

func TestSnapshotOwnerSyncInlinePublication(t *testing.T) {
	value := &changingSnapshotOwnerValue{
		persistSnapshotValue: persistSnapshotValue{SnapshotID: "before"},
		revision:             1,
	}
	self := DynamicResultArrayOutput{Elem: value, Values: []AnyResult{newDetachedResult(nil, value)}}
	frame := persistCodecFrame("inline", self)
	joinWriter := startSnapshotOwnerWriter(t, t.Context(), value, nil)
	links, err := collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, joinWriter())
	require.NoError(t, err)
	require.Equal(t, []PersistedSnapshotRefLink{{RefKey: "before", Role: "snapshot", OutputPath: PersistedRefPath{}.Field("items").Index(0)}}, links)
	links, err = collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, err)
	require.Equal(t, "after", links[0].RefKey)
}

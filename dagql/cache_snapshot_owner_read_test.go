package dagql

import (
	"context"
	"errors"
	"sync"
	"testing"

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
	mu        sync.Mutex
	revision  OutputRevision
	read      chan struct{}
	published chan struct{}
	readOnce  sync.Once
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
			<-v.published
		})
	}
	return revision, links, nil
}

func TestSnapshotOwnerPublicationUsesCoherentRead(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	manager := &fakeSnapshotManager{}
	c, err := NewCache(ctx, "", manager, nil)
	require.NoError(t, err)
	session := cacheTestSessionID(t, ctx)
	t.Cleanup(func() {
		require.NoError(t, c.ReleaseSession(ctx, session))
		require.NoError(t, c.Close(ctx))
	})
	value := &changingSnapshotOwnerValue{
		persistSnapshotValue: persistSnapshotValue{SnapshotID: "before"},
		revision:             1,
	}
	original, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: persistCodecFrame("original", value)}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	require.NoError(t, err)

	value.read, value.published = make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		<-value.read
		value.mu.Lock()
		value.revision++
		value.SnapshotID = "after"
		value.mu.Unlock()
		close(value.published)
		// A completed writer must still reconcile its own publication.
		writerDone <- c.SyncResultSnapshotOwnerLeases(ctx, original)
	}()
	// Like from(tag) returning from(digest), this call adopts a result
	// that another consumer can already evaluate and publish into.
	alias, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: persistCodecFrame("alias", value)}, func(context.Context) (AnyResult, error) {
		return original, nil
	})
	require.NoError(t, <-writerDone)
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
		read:                 make(chan struct{}),
		published:            make(chan struct{}),
	}
	self := DynamicResultArrayOutput{Elem: value, Values: []AnyResult{newDetachedResult(nil, value)}}
	frame := persistCodecFrame("inline", self)
	go func() {
		<-value.read
		value.mu.Lock()
		value.revision++
		value.SnapshotID = "after"
		value.mu.Unlock()
		close(value.published)
	}()
	links, err := collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, err)
	require.Equal(t, []PersistedSnapshotRefLink{{RefKey: "before", Role: "snapshot", OutputPath: PersistedRefPath{}.Field("items").Index(0)}}, links)
	links, err = collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, err)
	require.Equal(t, "after", links[0].RefKey)
}

package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type httpPinRef struct {
	bkcache.ImmutableRef
	releases   atomic.Int64
	releaseErr error
}

func (r *httpPinRef) Release(ctx context.Context) error {
	r.releases.Add(1)
	return errors.Join(r.ImmutableRef.Release(ctx), r.releaseErr)
}

type httpPinManager struct {
	bkcache.SnapshotManager
	pinErr    error
	pinned    *httpPinRef
	beforePin func()
	beforeNew func(bkcache.ImmutableRef) error
}

func (m *httpPinManager) PinSnapshot(ctx context.Context, id string) (bkcache.ImmutableRef, error) {
	if m.beforePin != nil {
		m.beforePin()
	}
	if m.pinErr != nil {
		return nil, m.pinErr
	}
	ref, err := m.SnapshotManager.PinSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	m.pinned = &httpPinRef{ImmutableRef: ref}
	return m.pinned, nil
}
func (m *httpPinManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	if m.beforeNew != nil {
		if err := m.beforeNew(parent); err != nil {
			return nil, err
		}
	}
	return m.SnapshotManager.New(ctx, parent, opts...)
}

func TestHTTPLocalBodyOwnership(t *testing.T) {
	ctx, store, cache, srv, server := executionFixture(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	var body atomic.Value
	body.Store("saved")
	var requests atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, body.Load().(string)) }))
	defer origin.Close()
	var stateResult dagql.ObjectResult[*HTTPState]
	require.NoError(t, srv.Select(ctx, srv.Root(), &stateResult, dagql.Selector{Field: "_httpState", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}}}))
	state := stateResult.Self()
	out, err := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "temporary")
	require.NoError(t, err)
	require.NoError(t, out.File.OnRelease(ctx))
	require.NoError(t, cache.SyncResultSnapshotOwnerLeases(ctx, stateResult))
	oldID := state.snapshotID
	entered, proceed := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	manager := &httpPinManager{SnapshotManager: store.Manager}
	manager.beforeNew = func(parent bkcache.ImmutableRef) error {
		if parent != nil && parent.SnapshotID() == oldID && paused.CompareAndSwap(false, true) {
			close(entered)
			<-proceed
		}
		return nil
	}
	server.cacheManager = manager
	file := &File{Platform: query.Platform(), File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Lazy: &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0644, BodyDigest: digest.FromString("saved")}}
	finished := make(chan error, 1)
	go func() { finished <- file.Lazy.Evaluate(ctx, file) }()
	<-entered
	// The state may advance once the independent pin is obtained.
	body.Store("changed")
	out, err = state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "temporary")
	require.NoError(t, err)
	require.NoError(t, out.File.OnRelease(ctx))
	require.NotEqual(t, oldID, state.snapshotID)
	require.NoError(t, cache.SyncResultSnapshotOwnerLeases(ctx, stateResult))
	owners, err := store.Leases.List(ctx)
	require.NoError(t, err)
	kept := 0
	for _, owner := range owners {
		resources, err := store.Leases.ListResources(ctx, owner)
		require.NoError(t, err)
		for _, resource := range resources {
			if resource.ID != oldID {
				continue
			}
			if owner.Labels["dagger.io/snapshot-transfer"] == "true" {
				kept++
				break
			}
			require.NoError(t, store.Manager.RemoveLease(ctx, owner.ID))
			break
		}
	}
	require.Equal(t, 1, kept, "the independent pin must be the only old snapshot owner")
	store.GC(t)
	_, err = store.Snapshots.Stat(ctx, oldID)
	require.NoError(t, err)
	close(proceed)
	require.NoError(t, <-finished)
	data, _ := producedFileContents(t, file)
	require.Equal(t, "saved", string(data))
	require.EqualValues(t, 2, requests.Load(), "only the two outer resolutions fetched")
	require.EqualValues(t, 1, manager.pinned.releases.Load())
	require.NoError(t, file.OnRelease(ctx))
	owners, err = store.Leases.List(ctx)
	require.NoError(t, err)
	for _, owner := range owners {
		require.NotEqual(t, "true", owner.Labels["dagger.io/snapshot-transfer"])
	}
}

func TestHTTPLocalBodyFailures(t *testing.T) {
	ctx, store, _, srv, server := executionFixture(t)
	var requests atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Empty(t, r.Header.Get("If-None-Match"))
		fmt.Fprint(w, "saved")
	}))
	defer origin.Close()
	var result dagql.ObjectResult[*HTTPState]
	require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "_httpState", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}}}))
	state := result.Self()
	snapshot, _ := store.Build(t, nil, "contents", "saved")
	fault := errors.New("injected pin or release failure")
	for _, test := range []struct {
		name                                  string
		pinErr                                error
		wantFetch                             bool
		wantErr                               error
		foreign, advanced, absent, derivation bool
	}{
		{name: "matching"}, {name: "absent", absent: true, wantFetch: true}, {name: "foreign", foreign: true, wantFetch: true}, {name: "advanced", advanced: true, wantFetch: true},
		{name: "unavailable", pinErr: cerrdefs.ErrNotFound, wantFetch: true},
		{name: "pin error", pinErr: fault, wantErr: fault}, {name: "cancel", pinErr: context.Canceled, wantErr: context.Canceled},
		{name: "unavailable plus cleanup", pinErr: errors.Join(cerrdefs.ErrNotFound, fault), wantErr: fault},
		{name: "derivation plus cleanup", derivation: true, wantErr: fault},
	} {
		t.Run(test.name, func(t *testing.T) {
			state.snapshotID = snapshot.SnapshotID()
			state.ContentDigest = digest.FromString("saved")
			state.foreignUninitialized = test.foreign
			if test.absent {
				state.snapshotID = ""
			}
			if test.advanced {
				state.ContentDigest = digest.FromString("other")
			}
			manager := &httpPinManager{SnapshotManager: store.Manager, pinErr: test.pinErr}
			if test.derivation {
				manager.beforeNew = func(bkcache.ImmutableRef) error {
					manager.pinned.releaseErr = fault
					return errors.New("derive failure")
				}
			}
			server.cacheManager = manager
			file := freshProducerFile()
			lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0644, BodyDigest: digest.FromString("saved")}
			before := requests.Load()
			err := lazy.Evaluate(ctx, file)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				_, ready := file.Snapshot.Peek()
				require.False(t, ready)
			} else {
				require.NoError(t, err)
				require.NoError(t, file.OnRelease(ctx))
			}
			if test.derivation {
				require.ErrorContains(t, err, "derive failure")
			}
			want := int64(0)
			if test.wantFetch {
				want = 1
			}
			require.Equal(t, want, requests.Load()-before)
			if manager.pinned != nil {
				require.EqualValues(t, 1, manager.pinned.releases.Load())
			}
		})
	}
}

func TestHTTPPinLockCost(t *testing.T) {
	ctx, store, _, _, _ := executionFixture(t)
	ref, _ := store.Build(t, nil, "contents", "saved")
	state := &HTTPState{ContentDigest: digest.FromString("saved"), snapshotID: ref.SnapshotID()}
	var elapsed time.Duration
	for range 20 {
		start := time.Now()
		pin, err := state.pinHTTPBody(ctx, store.Manager, state.ContentDigest)
		elapsed += time.Since(start)
		require.NoError(t, err)
		require.NoError(t, pin.Release(ctx))
	}
	state.mu.Lock()
	started := make(chan struct{})
	done := make(chan time.Duration, 1)
	go func() {
		close(started)
		start := time.Now()
		pin, err := state.pinHTTPBody(ctx, store.Manager, digest.FromString("saved"))
		if err == nil {
			err = pin.Release(ctx)
		}
		require.NoError(t, err)
		done <- time.Since(start)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	state.mu.Unlock()
	t.Logf("HTTP local pin average=%s; acquisition with a 20ms occupied state lock=%s", elapsed/20, <-done)
}

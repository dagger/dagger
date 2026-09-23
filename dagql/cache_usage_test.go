package dagql

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type usageCallbackValue struct {
	Int
	callback  func(string)
	onRelease func()
}

func (v *usageCallbackValue) CacheUsageIdentities() []string {
	v.callback("identities")
	return []string{"shared-snapshot"}
}

func (v *usageCallbackValue) CacheUsageMayChange() bool {
	v.callback("may-change")
	return true
}

func (v *usageCallbackValue) CacheUsageSize(context.Context, CacheUsageSizeProvider, string) (int64, bool, error) {
	v.callback("size")
	return 64, true, nil
}

func (v *usageCallbackValue) OnRelease(context.Context) error {
	if v.onRelease != nil {
		v.onRelease()
	}
	return nil
}

func publishUsageValue(t *testing.T, c *Cache, field string, v *usageCallbackValue) AnyResult {
	t.Helper()
	frame := cacheTestIntCall(field)
	res, err := c.GetOrInitCall(t.Context(), "provider", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(frame, v), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCacheUsageProviderCallbacksOutsideGraph(t *testing.T) {
	for _, mode := range []string{"measurement", "disk-prune"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewCache(t.Context(), "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := map[string]int{}
			v := &usageCallbackValue{Int: NewInt(1), callback: func(kind string) {
				calls[kind]++
				// No concurrent cache work in this test: failure means this
				// callback itself is being invoked under E. Check EVERY call,
				// including both historical disk-prune identity call sites.
				if c.egraphMu.TryLock() {
					c.egraphMu.Unlock()
				} else {
					t.Errorf("%s called under E", kind)
				}
			}}
			publishUsageValue(t, c, "callbacks", v)
			if mode == "measurement" {
				c.measureAllResultSizes(t.Context())
			} else {
				_, err := c.snapshotPruneStateCancelable(nil, pruneSnapshotDisk, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls["identities"] == 0 || calls["may-change"] == 0 {
				t.Fatalf("missing provider callbacks: %v", calls)
			}
			if mode == "measurement" && calls["size"] != 1 {
				t.Fatalf("size calls: %v", calls)
			}
		})
	}
}

func TestCacheUsageRetainsRowsThroughSizing(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	v := &usageCallbackValue{Int: NewInt(1), onRelease: func() { released = true }}
	v.callback = func(kind string) {
		if kind == "size" {
			if err := c.ReleaseSession(t.Context(), "provider"); err != nil {
				t.Error(err)
			}
			if released {
				t.Error("provider released during size callback")
			}
		}
	}
	publishUsageValue(t, c, "size-lifetime", v)
	c.measureAllResultSizes(t.Context())
	if !released || c.Size() != 0 {
		t.Fatal("measurement hold was not collected after sizing")
	}
}

func TestCacheUsageCancellationReleasesRows(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	released := false
	v := &usageCallbackValue{Int: NewInt(1), onRelease: func() { released = true }}
	v.callback = func(kind string) {
		if kind == "identities" {
			if err := c.ReleaseSession(t.Context(), "provider"); err != nil {
				t.Error(err)
			}
			cancel()
		}
	}
	publishUsageValue(t, c, "canceled-usage", v)
	_, err = c.collectUsageMeasurementInputs(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !released || c.Size() != 0 || c.activeGlobalOperations.Load() != 0 {
		t.Fatal("canceled collection retained ownership or an operation")
	}
}

func TestCacheUsagePruneDefersChangedPopulation(t *testing.T) {
	for _, change := range []string{"new-sharer", "replaced-payload", "released-row"} {
		t.Run(change, func(t *testing.T) {
			c, err := NewCache(t.Context(), "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			v := &usageCallbackValue{Int: NewInt(1)}
			var row *sharedResult
			v.callback = func(kind string) {
				if kind != "identities" {
					return
				}
				switch change {
				case "new-sharer":
					publishUsageValue(t, c, "new-sharer", &usageCallbackValue{Int: NewInt(2), callback: func(string) {}})
				case "replaced-payload":
					row.payloadMu.Lock()
					row.self = &usageCallbackValue{Int: NewInt(3), callback: func(string) {}}
					row.payloadRevision++
					row.payloadMu.Unlock()
				case "released-row":
					if err := c.ReleaseSession(t.Context(), "provider"); err != nil {
						t.Error(err)
					}
				}
			}
			row = publishUsageValue(t, c, "changing-provider", v).cacheSharedResult()
			_, err = c.snapshotPruneStateCancelable(nil, pruneSnapshotDisk, 0, nil)
			if !errors.Is(err, errCacheUsageChanged) {
				t.Fatalf("expected deferred sample, got %v", err)
			}
			if c.activeGlobalOperations.Load() != 0 {
				t.Fatal("sample leaked operation")
			}
			if change == "released-row" && c.Size() != 0 {
				t.Fatal("temporary hold did not collect released row")
			}
		})
	}
}

func TestCacheUsagePruneSnapshotExcludesMeasurementHolds(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := publishUsageValue(t, c, "hold-count", &usageCallbackValue{Int: NewInt(1), callback: func(string) {}})
	row := res.cacheSharedResult()
	c.egraphMu.RLock()
	before := row.incomingOwnershipCount
	c.egraphMu.RUnlock()
	snapshot, err := c.snapshotPruneStateCancelable(nil, pruneSnapshotDisk, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.results[row.id].incomingCount; got != before {
		t.Fatalf("snapshot includes measurement hold: %d != %d", got, before)
	}
}

// The fixture models the operational mutex held by a remote fetch.
type usagePublicationValue struct {
	Int
	entered chan struct{}
	mu      sync.Mutex
	once    sync.Once
}

func (v *usagePublicationValue) CacheUsageIdentities() []string {
	v.once.Do(func() { close(v.entered) })
	v.mu.Lock()
	defer v.mu.Unlock()
	return nil
}

func TestCacheUsageIdentityDoesNotStallPublication(t *testing.T) {
	for _, mode := range []string{"measurement", "disk-prune"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			c, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			v := &usagePublicationValue{Int: NewInt(1), entered: make(chan struct{})}
			frame := cacheTestIntCall("diagnostic-usage-provider")
			_, err = c.GetOrInitCall(ctx, "provider", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
				return cacheTestDetachedResult(frame, v), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			beforeIndex := make(chan struct{})
			allowIndex := make(chan struct{})
			var indexOnce sync.Once
			resumeIndex := func() { indexOnce.Do(func() { close(allowIndex) }) }
			defer resumeIndex()
			c.testBeforePublicationIndex = func(*ongoingCall) {
				close(beforeIndex)
				<-allowIndex
			}
			publicationDone := make(chan error, 1)
			publicationExited := make(chan struct{})
			go func() {
				call := cacheTestIntCall("diagnostic-unrelated-scalar")
				_, err := c.GetOrInitCall(ctx, "publisher", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
					return cacheTestIntResult(call, 2), nil
				})
				publicationDone <- err
				close(publicationExited)
			}()
			await := func(ch <-chan struct{}, label string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for %s", label)
				}
			}
			defer func() {
				resumeIndex()
				select {
				case <-publicationExited:
				case <-time.After(5 * time.Second):
					t.Error("publication cleanup stalled")
				}
			}()
			await(beforeIndex, "publication pre-index gate")
			// The operation owns the mirror mutex before usage collection starts.
			// Cleanup can release this fixture lock to break a diagnosed cycle,
			// so even failing runs drain every goroutine instead of hanging.
			v.mu.Lock()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(v.mu.Unlock) }
			defer release()
			needGraph := make(chan struct{})
			var graphOnce sync.Once
			resumeGraph := func() { graphOnce.Do(func() { close(needGraph) }) }
			operationDone := make(chan struct{})
			var operationResultCount int
			go func() {
				<-needGraph
				// Model an operation that needs the graph while holding the
				// provider mutex; the completion signal publishes this read.
				c.egraphMu.Lock()
				operationResultCount = len(c.resultsByID)
				c.egraphMu.Unlock()
				release()
				close(operationDone)
			}()
			defer func() {
				resumeGraph()
				release()
				select {
				case <-operationDone:
				case <-time.After(5 * time.Second):
					t.Error("operation cleanup stalled")
				}
			}()
			measurementDone := make(chan struct{})
			go func() {
				if mode == "measurement" {
					snapshot, err := c.collectUsageMeasurementInputs(ctx)
					if err == nil {
						snapshot.closeAndLog(ctx)
					} else {
						t.Error(err)
					}
				} else {
					c.snapshotPruneState(pruneSnapshotDisk, 0)
				}
				close(measurementDone)
			}()
			defer func() {
				release()
				select {
				case <-measurementDone:
				case <-time.After(5 * time.Second):
					t.Error("measurement cleanup stalled")
				}
			}()
			await(v.entered, "identity provider")
			// Capture the lock state before admitting either graph writer.
			graphPinned := !c.egraphMu.TryLock()
			if !graphPinned {
				c.egraphMu.Unlock()
			}
			resumeIndex()
			resumeGraph()
			stalled := false
			select {
			case <-operationDone:
			case <-time.After(250 * time.Millisecond):
				stalled = true
			}
			release()
			await(operationDone, "operational graph acquisition after cleanup")
			if operationResultCount == 0 {
				t.Error("operational graph read did not observe the registered provider")
			}
			await(measurementDone, "usage collection")
			select {
			case err := <-publicationDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("publication did not finish after identity release")
			}
			if stalled {
				t.Errorf("stalled: identity collection pins E=%v while operational mutex holder needs E; unrelated scalar publication cannot finish", graphPinned)
			}
		})
	}
}

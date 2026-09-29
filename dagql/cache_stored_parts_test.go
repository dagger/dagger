package dagql

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func storedPartTestCache(t *testing.T, opts ...CacheOption) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	return ctx, c
}

func storedPartTestPublish(t *testing.T, ctx context.Context, c *Cache, session, field string, value *captureTestValue) AnyResult {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&captureTestValue{}).Type())}
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame.clone())
	})
	require.NoError(t, err)
	return res
}

func storedPartTestStored(c *Cache, res AnyResult) map[string]PersistedPartOffer {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.resultsByID[res.cacheSharedResult().id].storedParts
}

// Only a blob-backed cache stores parts. An engine's cache refuses them.
func TestSetStoredPartNeedsABlobStore(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t)
	res := storedPartTestPublish(t, ctx, c, "s", "engine-stored-part", &captureTestValue{text: "v"})
	stored, err := c.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), testLiveOffer(), 0)
	require.ErrorContains(t, err, "no blob store")
	require.False(t, stored)
	require.Empty(t, storedPartTestStored(c, res))
}

// A blob-backed cache stores a part for an entry with a value. It refuses a
// metadata part, which carries no bytes, an unknown entry, and an entry known
// only through holdings, which has no value for the part to belong to.
func TestSetStoredPart(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	res := storedPartTestPublish(t, ctx, c, "s", "stored-part", &captureTestValue{text: "v"})
	number := uint64(res.cacheSharedResult().id)
	key, err := partAddressKey(testLiveOffer().Address)
	require.NoError(t, err)

	stored, err := c.SetStoredPart(ctx, number, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, testLiveOffer(), storedPartTestStored(c, res)[key])

	metadata := testLiveOffer()
	metadata.Address.Part = "metadata"
	_, err = c.SetStoredPart(ctx, number, metadata, 0)
	require.ErrorContains(t, err, "metadata")

	_, err = c.SetStoredPart(ctx, number+1000, testLiveOffer(), 0)
	require.ErrorContains(t, err, "no entry")

	a := newCloudApplier(t, c)
	held := holdingOf("stored-part-held")
	a.call(callRow{key: HolderKey{"cache-a", 1}, session: "s", holding: held})
	c.egraphMu.RLock()
	heldEntry := c.entriesByRecipe[held.Recipe]
	c.egraphMu.RUnlock()
	_, err = c.SetStoredPart(ctx, uint64(heldEntry), testLiveOffer(), 0)
	require.ErrorContains(t, err, "no value")
}

// A part uploaded from a copy of the recipe is admitted against the value the
// entry holds when it is stored: an unexpired copy's part always, an expired
// copy's only while the entry's value has expired too.
func TestSetStoredPartAdmission(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, tc := range []struct {
		name         string
		copyExpires  int64
		entryExpired bool
		stored       bool
	}{
		{"unexpired copy, unexpired entry", now.Add(time.Hour).Unix(), false, true},
		{"unexpired copy, expired entry", 0, true, true},
		{"expired copy, unexpired entry", now.Add(-time.Hour).Unix(), false, false},
		{"expired copy, expired entry", now.Add(-time.Hour).Unix(), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, c := storedPartTestCache(t, WithBlobStore())
			res := storedPartTestPublish(t, ctx, c, "s", "stored-part-admission", &captureTestValue{text: "v"})
			if tc.entryExpired {
				currentEntryTestExpire(c, res)
			}
			stored, err := c.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), testLiveOffer(), tc.copyExpires)
			require.NoError(t, err)
			require.Equal(t, tc.stored, stored)
			require.Equal(t, tc.stored, len(storedPartTestStored(c, res)) == 1)
		})
	}
}

// Stored parts belong to the value: a replacement in place drops them, as it
// drops the offers.
func TestReplacementDropsStoredParts(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	old := storedPartTestPublish(t, ctx, c, "a", "stored-part-replaced", &captureTestValue{text: "old"})
	stored, err := c.SetStoredPart(ctx, uint64(old.cacheSharedResult().id), testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, c.ReleaseSession(ctx, "a"))
	currentEntryTestExpire(c, old)

	replaced := storedPartTestPublish(t, ctx, c, "b", "stored-part-replaced", &captureTestValue{text: "new"})
	require.Equal(t, old.cacheSharedResult().id, replaced.cacheSharedResult().id, "replaced in place")
	require.Empty(t, storedPartTestStored(c, replaced))
	require.NoError(t, c.ReleaseSession(ctx, "b"))
}

// Admission reads the clock under the graph lock: a copy that expires while
// SetStoredPart waits for the lock gives no part to an entry whose value is
// still live, although it had not expired when the call started. The call is
// observed blocked on the lock before the copy expires.
func TestSetStoredPartAdmitsUnderTheGraphLock(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	res := storedPartTestPublish(t, ctx, c, "s", "stored-part-clock", &captureTestValue{text: "live"})
	t.Cleanup(func() { require.NoError(t, c.ReleaseSession(context.Background(), "s")) })
	expires := time.Now().Unix() + 2
	c.egraphMu.Lock()
	locked := true
	defer func() {
		if locked {
			c.egraphMu.Unlock()
		}
	}()
	type outcome struct {
		stored bool
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		stored, err := c.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), testLiveOffer(), expires)
		done <- outcome{stored, err}
	}()
	blocked := false
	for deadline := time.Now().Add(time.Second); !blocked && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(stack, "(*Cache).SetStoredPart(") && strings.Contains(stack, "sync.(*RWMutex).Lock(") {
				blocked = true
				break
			}
		}
	}
	require.True(t, blocked, "SetStoredPart waits for the graph lock before the copy expires")
	require.Less(t, time.Now().Unix(), expires)
	time.Sleep(time.Until(time.Unix(expires, 0)) + 50*time.Millisecond)
	c.egraphMu.Unlock()
	locked = false
	got := <-done
	require.NoError(t, got.err)
	require.False(t, got.stored, "the expired copy's part does not attach to the live entry")
	require.Empty(t, storedPartTestStored(c, res))
}

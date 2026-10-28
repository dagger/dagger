package dagql

import (
	"fmt"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// Each result has its own call and output group: no large list to share.
func resultListSingletonFixture(t testing.TB, count int, persisted bool) *cacheDerivedIndexFixture {
	t.Helper()
	f := &cacheDerivedIndexFixture{setupSession: "result-list-singletons"}
	if persisted {
		f.dbPath = filepath.Join(t.TempDir(), "cache.db")
	}
	f.cache, f.ctx = newCacheDerivedIndexFixtureCache(t, f.setupSession, f.dbPath)
	for i := range count {
		call := cacheTestIntCall(fmt.Sprintf("singleton-%d", i))
		cacheDerivedIndexPublishPersistedResult(t, f, call, call, i)
	}
	assert.Equal(t, count, len(f.cache.resultsByID))
	assert.Equal(t, count, len(f.cache.outputEqClassResults))
	for _, results := range f.cache.outputEqClassResults {
		assert.Equal(t, 1, len(results))
	}
	assert.NilError(t, f.cache.ReleaseSession(f.ctx, f.setupSession))
	return f
}

func BenchmarkResultListSingletonImport(b *testing.B) {
	for _, count := range []int{128, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			f := resultListSingletonFixture(b, count, true)
			assert.NilError(b, f.cache.Close(b.Context()))
			f.cache = nil
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c, err := NewCache(f.ctx, f.dbPath, nil, nil)
				b.StopTimer()
				assert.NilError(b, err)
				assert.Equal(b, CachePersistenceResetNone, c.PersistenceResetReason())
				assert.Equal(b, count, len(c.resultsByID))
				assert.Equal(b, count, len(c.outputEqClassResults))
				for _, results := range c.outputEqClassResults {
					assert.Equal(b, 1, len(results))
				}
				c.egraphMu.Lock()
				indexErr := cacheDerivedIndexesErrorLocked(c)
				c.egraphMu.Unlock()
				assert.NilError(b, indexErr)
				assert.NilError(b, c.Close(b.Context()))
				b.StartTimer()
			}
		})
	}
}

func BenchmarkResultListFreshInsert(b *testing.B) {
	for _, count := range []int{128, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				f := &cacheDerivedIndexFixture{setupSession: "result-list-fresh"}
				f.cache, f.ctx = newCacheDerivedIndexFixtureCache(b, f.setupSession, "")
				b.StartTimer()
				for i := range count {
					call := cacheTestIntCall(fmt.Sprintf("fresh-%d", i))
					cacheDerivedIndexPublishPersistedResult(b, f, call, call, i)
				}
				b.StopTimer()
				assert.Equal(b, count, len(f.cache.resultsByID))
				f.close(b)
			}
		})
	}
}

func BenchmarkCacheImportWideOutputClass(b *testing.B) {
	for _, width := range []int{64, 256, 1024} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			f := cacheDerivedIndexWideOutputFixture(b, width, true)
			assert.NilError(b, f.cache.Close(b.Context()))
			f.cache = nil
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				c, err := NewCache(f.ctx, f.dbPath, nil, nil)
				assert.NilError(b, err)
				b.StopTimer()
				assert.Equal(b, CachePersistenceResetNone, c.PersistenceResetReason())
				assert.Equal(b, 2*width+1, len(c.resultsByID))
				assert.NilError(b, c.Close(b.Context()))
				b.StartTimer()
			}
		})
	}
}

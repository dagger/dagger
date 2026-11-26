package dagql

import (
	"fmt"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// BenchmarkStoreAndDropParts times storing parts under the cache's write
// hold (SetStoredParts) and dropping one in eight of them by layer
// (DropStoredParts), on the stored bundle benchmarks' cloud.
func BenchmarkStoreAndDropParts(b *testing.B) {
	ctx, cloud, batch := storedBenchCloud(b)
	writes := make([]StoredPartWrite, len(batch))
	b.ResetTimer()
	for i := range b.N {
		b.StopTimer()
		for j, number := range batch {
			part := storedBenchPart(uint64(1<<32 + i*len(batch) + j))
			part.Address.Part = PartKey(fmt.Sprintf("bench-%d", j%4))
			writes[j] = StoredPartWrite{Number: number, Part: part}
		}
		b.StartTimer()
		got, _, err := cloud.SetStoredParts(ctx, writes)
		require.NoError(b, err)
		require.True(b, got[0].Stored)
		for j := range batch {
			if j%8 == 0 {
				_, _, err := cloud.DropStoredParts(digest.FromString(fmt.Sprintf("stored-bench-layer-%d-%d", 1<<32+i*len(batch)+j, 0)))
				require.NoError(b, err)
			}
		}
	}
}

// BenchmarkInstallStoredBody times the body worker's install of a stored
// record's body under the write hold (installStoredBodyLocked), which gives
// the entry its light envelope: what each record a merge stores costs the
// lock once the worker has compressed it. It reports the time a record.
func BenchmarkInstallStoredBody(b *testing.B) {
	_, cloud, _ := storedBenchCloud(b)
	deadline := time.Now().Add(30 * time.Second)
	for {
		waiting := 0
		cloud.egraphMu.RLock()
		for _, res := range cloud.resultsByID {
			if res.persistedEnvelope != nil && res.stored == nil {
				waiting++
			}
		}
		cloud.egraphMu.RUnlock()
		if waiting == 0 {
			break
		}
		if time.Now().After(deadline) {
			b.Fatalf("%d records not compressed", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cloud.egraphMu.Lock()
	defer cloud.egraphMu.Unlock()
	type install struct {
		res   *sharedResult
		env   *PersistedResultEnvelope
		light *PersistedResultEnvelope
		body  *storedBody
	}
	var installs []install
	for _, res := range cloud.resultsByID {
		if res.stored == nil {
			continue
		}
		rec, err := res.stored.record(uint64(res.id))
		require.NoError(b, err)
		env := rec.Envelope
		installs = append(installs, install{res: res, env: &env, light: sharedLightStoredEnvelope(&env), body: res.stored})
	}
	require.NotEmpty(b, installs)
	// The benchmark times the install, not the tests' check of each shared
	// value against its key. Tests are over when benchmarks start.
	verify := verifySharedValues
	verifySharedValues = false
	defer func() { verifySharedValues = verify }()
	b.ResetTimer()
	for i := range b.N {
		in := installs[i%len(installs)]
		cloud.installStoredBodyLocked(in.res, in.env, in.light, in.body)
	}
}

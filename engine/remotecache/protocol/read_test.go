package protocol

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// readTestBody gives a merge request's body to decode in one pass.
func readTestBody(env Envelope) any {
	if env.Re == 0 && env.Type == TypeMerge {
		return &Merge{}
	}
	return nil
}

func readTestMessage(t testing.TB, id, re uint64, typ Type, body any) []byte {
	t.Helper()
	env, err := NewEnvelope(id, re, typ, body)
	require.NoError(t, err)
	raw, err := json.Marshal(env)
	require.NoError(t, err)
	return raw
}

// ReadEnvelope decodes a message NewEnvelope wrote, whose type comes before
// its body, straight into the body bodyFor gives, leaving the raw Body nil.
// Every other message reads exactly as json.Unmarshal into an Envelope reads
// it, its raw Body included, and a message Unmarshal refuses is refused.
func TestReadEnvelope(t *testing.T) {
	merge := Merge{Bundle: testBundle(), Lookup: 3}
	env, body, err := ReadEnvelope(readTestMessage(t, 9, 0, TypeMerge, merge), readTestBody)
	require.NoError(t, err)
	require.Equal(t, Envelope{ID: 9, Type: TypeMerge}, env)
	require.Equal(t, &merge, body)

	for name, raw := range map[string][]byte{
		"no body for the type": readTestMessage(t, 10, 4, TypeLookedUp, LookedUp{Misses: []LookedUpMiss{{Status: LookupMerging, Merge: 9}}}),
		"a reply":              readTestMessage(t, 11, 2, TypeMerge, merge),
		"body before type":     []byte(`{"body": {"bundle": {"version": 3}}, "id": 12, "type": "merge"}`),
		"other key case":       []byte(`{"ID": 13, "Type": "merge", "Body": {"bundle": {"version": 3}}}`),
		"unknown key":          []byte(`{"id": 14, "type": "merge", "extra": 1, "body": {"bundle": {"version": 3}}}`),
		"a body it can't read": []byte(`{"id": 15, "type": "merge", "body": {"bundle": {"version": "three"}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			var want Envelope
			require.NoError(t, json.Unmarshal(raw, &want))
			env, body, err := ReadEnvelope(raw, readTestBody)
			require.NoError(t, err)
			require.Nil(t, body)
			require.Equal(t, want, env)
		})
	}

	for name, raw := range map[string]string{
		"truncated":     `{"id": 1, "type": "merge", "body": {"bundle": `,
		"trailing data": `{"id": 1, "type": "merge", "body": {"bundle": {"version": 3}}} x`,
		"not an object": `[1]`,
	} {
		t.Run(name, func(t *testing.T) {
			var want Envelope
			require.Error(t, json.Unmarshal([]byte(raw), &want))
			_, _, err := ReadEnvelope([]byte(raw), readTestBody)
			require.Error(t, err)
		})
	}
}

// readTestBundle is a merge's bundle of n records of about 4 KB.
func readTestBundle(t testing.TB, n int) dagql.ValueBundle {
	t.Helper()
	bundle := dagql.ValueBundle{Version: 3, Roots: []dagql.TransferredRoot{{Ordinal: 1}}}
	payload, err := json.Marshal(map[string]string{"text": strings.Repeat("a module's function, described ", 128)})
	require.NoError(t, err)
	for i := range n {
		bundle.Values = append(bundle.Values, dagql.TransferredValue{
			Ordinal:      dagql.TransferOrdinal(i + 1),
			SenderNumber: uint64(i + 1),
			Record: dagql.PersistedRecord{
				ResultID: uint64(i + 1),
				Call:     &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: fmt.Sprint("field-", i), Type: dagql.NewResultCallType(dagql.NewString("").Type())},
				Envelope: dagql.PersistedResultEnvelope{Kind: "object", TypeName: "Module", ObjectJSON: payload},
			},
		})
	}
	return bundle
}

// readTestAllocated is how many bytes f allocates.
func readTestAllocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Reading a merge's body in one pass allocates less than json.Unmarshal of
// the envelope then Decode of its body: no copy of the body, nor of the
// message.
func TestReadEnvelopeAllocatesLess(t *testing.T) {
	raw := readTestMessage(t, 7, 0, TypeMerge, Merge{Bundle: readTestBundle(t, 200)})
	unmarshal := readTestAllocated(func() {
		var env Envelope
		require.NoError(t, json.Unmarshal(raw, &env))
		var m Merge
		require.NoError(t, env.Decode(&m))
	})
	read := readTestAllocated(func() {
		_, body, err := ReadEnvelope(raw, readTestBody)
		require.NoError(t, err)
		require.NotNil(t, body)
	})
	t.Logf("message %d bytes; allocated: unmarshal then decode %d, ReadEnvelope %d", len(raw), unmarshal, read)
	require.Less(t, read, unmarshal)
}

// BenchmarkReadEnvelope reads a merge of 2,000 records of about 4 KB, as
// json.Unmarshal of the envelope then Decode of its body, and as
// ReadEnvelope, in one run. It reports the time per record.
func BenchmarkReadEnvelope(b *testing.B) {
	const records = 2_000
	bundle := readTestBundle(b, records)
	raw := readTestMessage(b, 7, 0, TypeMerge, Merge{Bundle: bundle})
	b.Logf("message bytes: %d", len(raw))
	b.Run("unmarshal-then-decode", func(b *testing.B) {
		b.SetBytes(int64(len(raw)))
		for range b.N {
			var env Envelope
			require.NoError(b, json.Unmarshal(raw, &env))
			var m Merge
			require.NoError(b, env.Decode(&m))
		}
		b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*records), "µs/record")
	})
	b.Run("read-envelope", func(b *testing.B) {
		b.SetBytes(int64(len(raw)))
		for range b.N {
			_, body, err := ReadEnvelope(raw, readTestBody)
			require.NoError(b, err)
			require.NotNil(b, body)
		}
		b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*records), "µs/record")
	})
}

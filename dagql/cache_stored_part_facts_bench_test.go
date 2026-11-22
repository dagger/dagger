package dagql

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/snapshots/config"
)

// BenchmarkCloudPartAnswers times a blob-backed cache's answers for the parts
// of 500 records an engine's bundle merged, as a selection pass asks them: one
// batch (AvailableParts) of each record's absent, pending and complete part.
// Each record's payload is a body of 24 typed fields, about 5 KB, like a
// module's type definitions. It runs over the records held whole, as a merge
// installs them and as they stay until the body worker compresses them, and
// over the records compressed. It reports the time an answer, and the
// payloads the cache decoded an answer.
func BenchmarkCloudPartAnswers(b *testing.B) {
	const records = 500
	fields := make([]transferTestField, 24)
	for i := range fields {
		fields[i] = transferTestField{
			Name:        fmt.Sprintf("field%d", i),
			Description: strings.Repeat("A field of a module's type, described at about the length a module's documentation gives one. ", 2),
			Type:        "String!",
			Args:        []transferTestArg{{Name: "arg", Type: "Int", Default: "1"}},
		}
	}
	for _, compressed := range []bool{false, true} {
		name := "whole"
		if compressed {
			name = "compressed"
		}
		b.Run(name, func(b *testing.B) {
			text := "part-answers-bench-" + name
			ctx, engine, srv := transferTestCache(b)
			var roots []AnyResult
			for i := range records {
				roots = append(roots, persistedListTestResult(b, ctx, engine, srv, fmt.Sprintf("part-answers-bench-%d", i), &transferTestValue{Text: text, Fields: fields, Parts: partFactsTestParts}))
			}
			var bundle ValueBundle
			require.NoError(b, engine.WithExportedValues(ctx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
				bundle = values.Bundle
				return nil
			}))
			cctx, cloud := storedPartTestCache(b, WithBlobStore())
			storedTestHoldWhole(cloud)
			reply, err := cloud.MergeValues(cctx, "engine", bundle)
			require.NoError(b, err)
			require.True(b, reply.Committed)
			if compressed {
				storedTestCompress(cloud)
			}
			var queries []PartQuery
			for _, root := range reply.Imported() {
				for _, address := range []PersistedPartAddress{partFactsMount, partFactsSnapshot, partFactsFS} {
					queries = append(queries, PartQuery{Number: root.ResultID, Address: address})
				}
			}
			require.Len(b, queries, 3*records)
			now := time.Now().Unix()
			decoded := transferTestMappedCount(text)
			b.ResetTimer()
			for b.Loop() {
				for i, answer := range cloud.AvailableParts(queries, now) {
					if answer.Err != nil || (i%3 == 0) != (answer.State == PartAbsent) {
						b.Fatalf("answer %d: %v, %v", i, answer.State, answer.Err)
					}
				}
			}
			answers := float64(b.N) * float64(len(queries))
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/answers, "ns/answer")
			b.ReportMetric(float64(transferTestMappedCount(text)-decoded)/answers, "decodes/answer")
		})
	}
}

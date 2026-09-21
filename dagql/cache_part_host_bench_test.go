package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPartUnusedHostAllocatesNoGate(t *testing.T) {
	c := new(Cache)
	row := &sharedResult{id: 1}
	host := c.partHostFor(row)
	require.Same(t, host, c.partHostFor(row))
	require.Nil(t, row.partGate.gate.Load())
	require.Nil(t, row.lazyPartGroups)
	require.Nil(t, c.partContentSource.Load())
}

func BenchmarkPartNativeHostEntry(b *testing.B) {
	for _, withHost := range []bool{false, true} {
		name := "callback"
		if withHost {
			name = "activated-host"
		}
		b.Run(name, func(b *testing.B) {
			c := new(Cache)
			row := &sharedResult{id: 1}
			host := c.partHostFor(row)
			token := &PartTaskToken{row: row, generation: 1}
			token.active.Store(true)
			ctx := context.WithValue(context.Background(), partTaskContextKey{}, token)
			body := func(context.Context) error { return nil }
			parts := []PartKey{"snapshot"}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var err error
				if withHost {
					err = host.RunNative(ctx, LazyGroupWhole, parts, body)
				} else {
					err = body(ctx)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

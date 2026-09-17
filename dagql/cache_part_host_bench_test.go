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
	// Unused remote abilities leave no override or bridge, including on a
	// zero-value cache whose nil source keeps the default meaning.
	require.Nil(t, c.PartContentSource().loadOverride())
	require.Nil(t, c.PartContentSource().attachedBridge())
	constructed, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, constructed.CloseDiscardingPersistence()) }()
	source := constructed.PartContentSource()
	require.NotNil(t, source)
	require.Nil(t, source.override.Load())
	require.Nil(t, source.bridge.Load())
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

package dagql

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type scopedBenchOutput struct {
	mu       sync.Mutex
	revision OutputRevision
	id       string
}

func (*scopedBenchOutput) Type() *ast.Type {
	return &ast.Type{NamedType: "ScopedBenchOutput", NonNull: true}
}
func (v *scopedBenchOutput) PersistedOutputRevision() (OutputRevision, error) {
	if !v.mu.TryLock() {
		return 0, ErrPersistStateNotReady
	}
	defer v.mu.Unlock()
	return v.revision, nil
}
func (v *scopedBenchOutput) PersistedSnapshotRefLinksChecked() ([]PersistedSnapshotRefLink, error) {
	if !v.mu.TryLock() {
		return nil, ErrPersistStateNotReady
	}
	defer v.mu.Unlock()
	return []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: v.id}}, nil
}
func scopedBenchTree(width, depth int) (Typed, []*scopedBenchOutput) {
	var leaves []*scopedBenchOutput
	var build func(int) Typed
	build = func(d int) Typed {
		if d == 0 {
			v := &scopedBenchOutput{id: "snapshot"}
			leaves = append(leaves, v)
			return v
		}
		values := make([]AnyResult, width)
		var elem Typed
		for i := range width {
			elem = build(d - 1)
			values[i] = newDetachedResult(nil, elem)
		}
		return DynamicResultArrayOutput{Elem: elem, Values: values}
	}
	return build(depth), leaves
}
func BenchmarkScopedSnapshotCollector(b *testing.B) {
	for _, shape := range []struct{ width, depth int }{{1, 1}, {128, 1}, {8, 3}} {
		b.Run(fmt.Sprintf("width%d_depth%d", shape.width, shape.depth), func(b *testing.B) {
			self, leaves := scopedBenchTree(shape.width, shape.depth)
			frame := persistCodecFrame("inline", self)
			b.ReportAllocs()
			b.ReportMetric(float64(len(leaves)), "outputs")
			b.ResetTimer()
			for b.Loop() {
				links, err := collectSnapshotOwnerLinks(self, frame, false)
				if err != nil || len(links) != len(leaves) {
					b.Fatalf("links=%d err=%v", len(links), err)
				}
			}
		})
	}
}
func TestScopedCollectorConcurrentPublicationCost(t *testing.T) {
	for _, shape := range []struct{ width, depth int }{{128, 1}, {8, 3}} {
		t.Run(fmt.Sprintf("width%d_depth%d", shape.width, shape.depth), func(t *testing.T) {
			self, leaves := scopedBenchTree(shape.width, shape.depth)
			frame := persistCodecFrame("inline", self)
			// First force a guarded rejection; it must expose no partial desired map.
			leaves[len(leaves)/2].mu.Lock()
			links, err := collectSnapshotOwnerLinks(self, frame, false)
			leaves[len(leaves)/2].mu.Unlock()
			require.ErrorIs(t, err, ErrPersistStateNotReady)
			require.Nil(t, links)
			var publications atomic.Int64
			done := make(chan struct{})
			started := make(chan struct{})
			go func() {
				defer close(done)
				close(started)
				for i := range 8192 {
					v := leaves[i%len(leaves)]
					v.mu.Lock()
					v.revision++
					v.id = fmt.Sprintf("snapshot-%d", i)
					v.mu.Unlock()
					publications.Add(1)
					if i%8 == 0 {
						runtime.Gosched()
					}
				}
			}()
			<-started
			start := time.Now()
			attempts, retries, success := 0, 0, 0
			for success < 128 {
				attempts++
				links, err := collectSnapshotOwnerLinks(self, frame, false)
				if errors.Is(err, ErrPersistStateNotReady) {
					retries++
					require.Nil(t, links)
					continue
				}
				require.NoError(t, err)
				require.Len(t, links, len(leaves))
				success++
			}
			<-done
			t.Logf("typed_collector outputs=%d depth=%d publications=%d attempts=%d retries=%d accepted=%d elapsed=%s", len(leaves), shape.depth, publications.Load(), attempts, retries, success, time.Since(start))
		})
	}
}

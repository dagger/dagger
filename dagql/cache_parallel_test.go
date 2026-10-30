package dagql

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestForEachIndexParallelRunsEveryIndexOnce(t *testing.T) {
	n := 64*runtime.GOMAXPROCS(0) + 3
	calls := make([]atomic.Int32, n)
	err := forEachIndexParallel(n, func(i int) error {
		calls[i].Add(1)
		return nil
	})
	require.NoError(t, err)
	for i := range calls {
		require.Equal(t, int32(1), calls[i].Load(), "index %d", i)
	}
}

func TestForEachIndexParallelReturnsLowestFailedIndex(t *testing.T) {
	const n = 4000
	ran := make([]atomic.Bool, n)
	higherStarted := make(chan struct{})
	lowerFailed := make(chan struct{})
	err := forEachIndexParallel(n, func(i int) error {
		ran[i].Store(true)
		switch i {
		case 1700:
			// When workers run concurrently, both failing indexes run and
			// the higher one fails last.
			select {
			case <-higherStarted:
			case <-time.After(time.Second):
			}
			defer close(lowerFailed)
			return fmt.Errorf("index %d failed", i)
		case 1701:
			close(higherStarted)
			select {
			case <-lowerFailed:
				time.Sleep(100 * time.Millisecond)
			case <-time.After(time.Second):
			}
			return fmt.Errorf("index %d failed", i)
		}
		return nil
	})
	require.EqualError(t, err, "index 1700 failed")
	for i := range 1700 {
		require.True(t, ran[i].Load(), "index %d below the failed index did not run", i)
	}
}

func TestForEachIndexParallelStartsNoIndexAfterAFailure(t *testing.T) {
	const n = 1_000_000
	var ran atomic.Int64
	err := forEachIndexParallel(n, func(i int) error {
		ran.Add(1)
		if i == 0 {
			return fmt.Errorf("index %d failed", i)
		}
		return nil
	})
	require.EqualError(t, err, "index 0 failed")
	require.Less(t, ran.Load(), int64(n/2))
}

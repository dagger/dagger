package core

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheMapConcurrent(t *testing.T) {
	c := newCacheMap[int, int]()

	commonKey := 42
	initialized := map[int]bool{}

	wg := new(sync.WaitGroup)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := c.GetOrInitialize(commonKey, func() (int, error) {
				initialized[i] = true
				return i, nil
			})
			require.NoError(t, err)
			require.True(t, initialized[val])
		}()
	}

	wg.Wait()

	// only one of them should have initialized
	require.Len(t, initialized, 1)
}

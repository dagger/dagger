package core

import (
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCacheMapConcurrent(t *testing.T) {
	t.Parallel()
	c := NewCacheMap[int, int]()

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

func TestCacheMapErrors(t *testing.T) {
	t.Parallel()
	c := NewCacheMap[int, int]()

	commonKey := 42

	myErr := errors.New("nope")
	_, err := c.GetOrInitialize(commonKey, func() (int, error) {
		return 0, myErr
	})
	require.Equal(t, myErr, err)

	otherErr := errors.New("nope 2")
	_, err = c.GetOrInitialize(commonKey, func() (int, error) {
		return 0, otherErr
	})
	require.Equal(t, otherErr, err)

	res, err := c.GetOrInitialize(commonKey, func() (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, res)

	res, err = c.GetOrInitialize(commonKey, func() (int, error) {
		return 0, errors.New("ignored")
	})
	require.NoError(t, err)
	require.Equal(t, 1, res)
}

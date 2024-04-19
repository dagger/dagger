package throttle

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestThrottle(t *testing.T) {
	t.Parallel()

	var i int64
	f := func() {
		atomic.AddInt64(&i, 1)
	}

	f = Throttle(50*time.Millisecond, f)

	f()
	f()

	require.Equal(t, int64(0), atomic.LoadInt64(&i))

	// test that i is never incremented twice and at least once in next 600ms
	retries := 0
	for {
		require.True(t, retries < 10)
		time.Sleep(60 * time.Millisecond)
		v := atomic.LoadInt64(&i)
		if v > 1 {
			require.Fail(t, "invalid value %d", v)
		}
		if v == 1 {
			break
		}
		retries++
	}

	require.Equal(t, int64(1), atomic.LoadInt64(&i))

	f()

	retries = 0
	for {
		require.True(t, retries < 10)
		time.Sleep(60 * time.Millisecond)
		v := atomic.LoadInt64(&i)
		if v == 2 {
			break
		}
		retries++
	}
}

func TestAfter(t *testing.T) {
	t.Parallel()

	var i int64
	f := func() {
		atomic.AddInt64(&i, 1)
	}

	f = After(100*time.Millisecond, f)

	f()

	time.Sleep(10 * time.Millisecond)
	require.Equal(t, int64(1), atomic.LoadInt64(&i))
	f()
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, int64(1), atomic.LoadInt64(&i))

	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int64(2), atomic.LoadInt64(&i))
}

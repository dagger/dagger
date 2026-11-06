package engineutil

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A runtime starts with the mode its exec asked for, the pid reaches the
// caller, and the process's own mode is put back afterwards, also when the
// start fails.
func TestStartWithTHP(t *testing.T) {
	getMode := func() int {
		mode, err := unix.PrctlRetInt(unix.PR_GET_THP_DISABLE, 0, 0, 0, 0)
		require.NoError(t, err)
		return mode
	}
	own := getMode()
	if setTHPMode(3) != nil {
		t.Skip("the kernel lacks PR_THP_DISABLE_EXCEPT_ADVISED (Linux 6.18 and later)")
	}
	require.NoError(t, setTHPMode(own))

	for _, tc := range []struct {
		hugePages bool
		want      int
	}{
		{hugePages: false, want: 3},
		{hugePages: true, want: 0},
	} {
		started := make(chan int, 1)
		var during int
		err := startWithTHP(tc.hugePages, started, func(started chan<- int) error {
			defer close(started)
			during = getMode()
			started <- 42
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, tc.want, during)
		require.Equal(t, 42, <-started)
		require.Equal(t, own, getMode())
	}

	failed := errors.New("runtime failed to start")
	started := make(chan int, 1)
	err := startWithTHP(false, started, func(started chan<- int) error {
		close(started)
		return failed
	})
	require.ErrorIs(t, err, failed)
	require.Equal(t, own, getMode())
	_, ok := <-started
	require.False(t, ok, "a failed start closes the caller's channel without a pid")
}

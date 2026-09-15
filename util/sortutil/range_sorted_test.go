package sortutil_test

import (
	"testing"

	"github.com/dagger/dagger/util/sortutil"
	"github.com/stretchr/testify/require"
)

func TestRangeSorted(t *testing.T) {
	m := map[string]int{
		"delta": 3,
		"alpha": 9,
		"gamma": -14,
	}
	callNum := 0
	sortutil.RangeSorted(m, func(k string, v int) {
		switch callNum {
		case 0:
			require.Equal(t, k, "alpha")
			require.Equal(t, v, 9)
		case 1:
			require.Equal(t, k, "delta")
			require.Equal(t, v, 3)
		case 2:
			require.Equal(t, k, "gamma")
			require.Equal(t, v, -14)
		}
		callNum++
	})
	require.Equal(t, len(m), callNum)
}

package dagql

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The reselect watch warns first at its threshold and then at each doubling,
// keeps the last refusal cause, and does nothing else.
func TestPartReselectWatch(t *testing.T) {
	ctx := t.Context()
	watch := partReselectWatch{loop: "test"}
	address := PersistedPartAddress{Part: "fs"}
	for range partReselectWarnAfter - 1 {
		watch.again(ctx, nil, address)
	}
	require.Equal(t, uint64(partReselectWarnAfter), watch.next, "no warning before the threshold")
	first, second := errors.New("first refusal"), errors.New("second refusal")
	watch.refused(first)
	watch.refused(nil)
	require.Same(t, first, watch.cause, "a retry without a cause keeps the last one")
	watch.refused(second)
	watch.again(ctx, nil, address)
	require.Equal(t, uint64(2*partReselectWarnAfter), watch.next, "the first warning arms the next at the doubling")
	for range partReselectWarnAfter {
		watch.again(ctx, nil, address)
	}
	require.Equal(t, uint64(4*partReselectWarnAfter), watch.next)
	require.Same(t, second, watch.cause)
}

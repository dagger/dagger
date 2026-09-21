package dagql

import (
	"errors"
	"fmt"
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

// A refusal names its site for the watch's warning and is otherwise the bare
// sentinel: in the reselect class alone or joined with nothing else, out of it
// once a real error is joined to it.
func TestPartRefusalNamesItsSite(t *testing.T) {
	refused := partRefused("commit: part store busy")
	require.ErrorIs(t, refused, ErrPartReselect)
	require.True(t, partCanReselect(refused))
	require.True(t, partCanReselect(errors.Join(refused, nil)))
	require.True(t, partCanReselect(fmt.Errorf("obtain: %w", refused)))
	require.False(t, partCanReselect(errors.Join(refused, errors.New("cleanup failed"))))
	require.Contains(t, refused.Error(), "commit: part store busy")

	// The receiver probe's refusal keeps the state the capture reported, as
	// text: its class stays the reselect sentinel's.
	cause := fmt.Errorf("%w: encode persisted file: missing snapshot and lazy op", ErrPersistStateNotReady)
	probe := partRefusedBy("demand: receiver probe not ready", cause)
	require.ErrorIs(t, probe, ErrPartReselect)
	require.NotErrorIs(t, probe, ErrPersistStateNotReady)
	require.Contains(t, probe.Error(), "missing snapshot and lazy op")

	watch := partReselectWatch{loop: "test"}
	watch.refused(probe)
	require.Contains(t, watch.cause.Error(), "demand: receiver probe not ready")
}

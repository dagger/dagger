package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// countingCause counts how often its text is asked for.
type countingCause struct{ calls int }

func (c *countingCause) Error() string {
	c.calls++
	return "counted cause"
}

// TestObservePartOffGateDoesNoWork checks the disabled path of the one
// observe entry point: with no fixture state, an observation that carries a
// delegation proof or a cause allocates nothing and never formats the
// cause. Enabled, the same observations name their source and detail.
func TestObservePartOffGateDoesNoWork(t *testing.T) {
	c := &Cache{}
	parent := &sharedResult{id: 7}
	proof := &partDelegationProof{parent: parent, source: PersistedPartAddress{Part: "snapshot", OutputPath: PersistedRefPath{}.Field("a").Index(0)}}
	cause := &countingCause{}
	address := PersistedPartAddress{Part: "snapshot"}

	allocs := testing.AllocsPerRun(100, func() {
		c.observePart(nil, address, partObservation{kind: PartEventSelectedDelegation, delegation: proof})
		c.observePart(nil, address, partObservation{kind: PartEventShareSkipped, cause: cause})
	})
	require.Zero(t, allocs, "the disabled path allocates nothing")
	require.Zero(t, cause.calls, "the disabled path never formats the cause")

	c.EnableTransferFixtureParts()
	c.observePart(nil, address, partObservation{kind: PartEventSelectedDelegation, delegation: proof})
	c.observePart(nil, address, partObservation{kind: PartEventShareSkipped, cause: cause})
	events, overflowed := c.partFixtureEvents()
	require.False(t, overflowed)
	require.Len(t, events, 2)
	require.Equal(t, PartEventSelectedDelegation, events[0].Kind)
	require.Equal(t, &TransferFixturePartSource{ResultID: 7, Address: PersistedPartAddress{Part: "snapshot", OutputPath: PersistedRefPath{}.Field("a").Index(0)}}, events[0].Source)
	require.Equal(t, PartEventShareSkipped, events[1].Kind)
	require.Equal(t, "counted cause", events[1].Detail)
	require.Equal(t, 1, cause.calls, "the enabled path formats the cause once")
}

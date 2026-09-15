package dagql

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// ProfileSkip is the frame-homed wcprof profile-skip bit (set by core.AroundFunc
// from a static recipe predicate). These tests pin the three properties the
// frame-homing design relies on: it travels with the frame through clone()/fork()
// and JSON persistence (so derived/adopted/imported results carry the producer's
// decision with no per-site provenance audit), and it is EXCLUDED from every recipe
// digest (it is a pure function of the recipe, so excluding it is consistent, and it
// must never enter cache identity / callKey / concurrencyKey).

func TestResultCallProfileSkipTravelsThroughCloneAndFork(t *testing.T) {
	t.Parallel()

	frame := cacheTestIntCall("field")
	frame.ProfileSkip = true
	require.True(t, frame.clone().ProfileSkip, "clone() must carry ProfileSkip")
	require.True(t, frame.fork().ProfileSkip, "fork() must carry ProfileSkip")

	frame.ProfileSkip = false
	require.False(t, frame.clone().ProfileSkip)
	require.False(t, frame.fork().ProfileSkip)
}

func TestResultCallProfileSkipExcludedFromDigests(t *testing.T) {
	t.Parallel()

	skipped := cacheTestIntCall("field")
	skipped.ProfileSkip = true
	profiled := cacheTestIntCall("field")
	profiled.ProfileSkip = false

	dSkip, err := skipped.deriveRecipeDigest(nil)
	require.NoError(t, err)
	dProf, err := profiled.deriveRecipeDigest(nil)
	require.NoError(t, err)
	require.Equal(t, dProf, dSkip, "ProfileSkip must not enter the recipe digest")

	cSkip, err := skipped.deriveContentPreferredDigest(nil)
	require.NoError(t, err)
	cProf, err := profiled.deriveContentPreferredDigest(nil)
	require.NoError(t, err)
	require.Equal(t, cProf, cSkip, "ProfileSkip must not enter the content-preferred digest")

	sSkip, _, err := skipped.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	sProf, _, err := profiled.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Equal(t, sProf, sSkip, "ProfileSkip must not enter the self digest")
}

func TestResultCallProfileSkipRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	// A skipped frame persists the bit (closes the warm-run/import volume gap).
	frame := cacheTestIntCall("field")
	frame.ProfileSkip = true
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	require.Contains(t, string(data), `"profileSkip":true`)

	var got ResultCall
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, got.ProfileSkip, "imported/persisted frame must carry ProfileSkip")

	// omitempty: a profiled frame carries no profileSkip key.
	frame.ProfileSkip = false
	data, err = json.Marshal(frame)
	require.NoError(t, err)
	require.NotContains(t, string(data), "profileSkip")
}

func TestFrameProfileSkipNilSafe(t *testing.T) {
	t.Parallel()

	require.False(t, frameProfileSkip(nil))
	require.False(t, frameProfileSkip(cacheTestIntCall("field")))
	skipped := cacheTestIntCall("field")
	skipped.ProfileSkip = true
	require.True(t, frameProfileSkip(skipped))

	// sharedResult.profileSkip reads the stored producer frame (lazy gating relies
	// on the producer's flag, not a waiter's bit).
	require.False(t, (*sharedResult)(nil).profileSkip())
	res := &sharedResult{}
	res.storeResultCall(skipped)
	require.True(t, res.profileSkip())
}

package dagql

import (
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A union's repair walks the terms that named the class it absorbed, not
// every term of the class that survives: teaching an identity that joins a
// new entry's class to a class 200 calls take as their input walks none of
// those 200 terms. Two ways to the union are taught: by a shared content
// digest, when the used class has been joined before and outranks the new
// one; and by the new entry's request, when neither class has been joined
// and the used one would be absorbed by rank alone. Either way, each use
// is then found through the new entry's recipe as well.
func TestUnionRepairWalksOnlyTheAbsorbedClassTerms(t *testing.T) {
	t.Parallel()
	const uses = 200
	for _, tc := range []struct {
		name string
		// base and twin are the used entry's row and the new entry's.
		base, twin func() RemoteHolding
	}{
		{
			name: "by content, the used class joined before",
			base: func() RemoteHolding {
				h := holdingOf("base")
				h.ContentDigest = testDigest("shared-content")
				return h
			},
			twin: func() RemoteHolding {
				h := holdingOf("twin")
				h.ContentDigest = testDigest("shared-content")
				return h
			},
		},
		{
			name: "by request, neither class joined before",
			base: func() RemoteHolding { return holdingOf("base") },
			twin: func() RemoteHolding {
				h := holdingOf("twin")
				h.Request = testDigest("base")
				return h
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			a := newCloudApplier(t, cloud)
			base := tc.base()
			a.call(callRow{key: HolderKey{"cache-a", 1}, session: "a", holding: base})
			for i := range uses {
				use := fmt.Sprint("use-", i)
				a.call(callRow{key: HolderKey{"cache-a", uint64(i + 2)}, session: "a", holding: RemoteHolding{
					Recipe:   testDigest(use),
					Request:  testDigest(use),
					Field:    "use",
					TypeName: "Directory",
					Term:     &RemoteTerm{Self: testDigest(use + "-self"), Inputs: []digest.Digest{base.Recipe}},
				}})
			}
			walked := func() uint64 {
				cloud.egraphMu.RLock()
				defer cloud.egraphMu.RUnlock()
				return cloud.egraphRepairWalks
			}
			before := walked()
			twin := tc.twin()
			a.call(callRow{key: HolderKey{"cache-b", 1}, session: "b", holding: twin})
			got := walked() - before

			// The union happened: a use is found through the twin's recipe.
			found := cloud.TermEntries(testDigest("use-7-self"), []digest.Digest{twin.Recipe})
			require.Len(t, found, 1, "a use is found through the twin's recipe once their classes are one")
			require.Equal(t, cloud.TermEntries(testDigest("use-7-self"), []digest.Digest{base.Recipe}), found)

			require.Less(t, got, uint64(3), "the union's repair walked %d terms; the absorbed class named none of the %d uses", got, uses)
		})
	}
}

package snapshots

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/remotecache/pack"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// The headers an engine keeps hold at most packHeaderMembers members, the
// least recently used going first: a header read again stays.
func TestPackHeadersKeepTheirBound(t *testing.T) {
	t.Parallel()
	header := func(n int) (digest.Digest, []pack.Member) {
		members := make([]pack.Member, pack.MaxMembers)
		for i := range members {
			members[i] = pack.Member{Digest: digest.FromString(fmt.Sprintf("pack %d member %d", n, i)), Size: 1}
		}
		return digest.FromString(fmt.Sprintf("pack %d", n)), members
	}
	var h packHeaders
	fit := packHeaderMembers / pack.MaxMembers
	ids := make([]digest.Digest, fit+1)
	for n := range fit {
		id, members := header(n)
		ids[n] = id
		h.keep(id, members, 1)
	}
	for n := range fit {
		_, _, ok := h.get(ids[n])
		require.True(t, ok, "pack %d is kept while the headers fit", n)
	}
	// The first header is read again, so the second is the least recently
	// used when one more header passes the bound.
	_, _, ok := h.get(ids[0])
	require.True(t, ok)
	id, members := header(fit)
	ids[fit] = id
	h.keep(id, members, 1)
	require.LessOrEqual(t, h.members, packHeaderMembers)
	_, _, ok = h.get(ids[1])
	require.False(t, ok, "the least recently used header went")
	for _, n := range []int{0, fit} {
		_, _, ok = h.get(ids[n])
		require.True(t, ok, "pack %d stays", n)
	}
}

package core

import (
	"testing"

	bkcontenthash "github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	"github.com/stretchr/testify/require"
)

func TestChecksumOptsKeyDiffersForOptions(t *testing.T) {
	base := checksumOptsKey(bkcontenthash.ChecksumOpts{})

	follow := checksumOptsKey(bkcontenthash.ChecksumOpts{FollowLinks: true})
	require.NotEqual(t, base, follow)

	wild := checksumOptsKey(bkcontenthash.ChecksumOpts{Wildcard: true})
	require.NotEqual(t, base, wild)

	include := checksumOptsKey(bkcontenthash.ChecksumOpts{IncludePatterns: []string{"a"}})
	exclude := checksumOptsKey(bkcontenthash.ChecksumOpts{ExcludePatterns: []string{"a"}})
	require.NotEqual(t, include, exclude)

	// identical options should produce identical keys
	base2 := checksumOptsKey(bkcontenthash.ChecksumOpts{})
	require.Equal(t, base, base2)
}

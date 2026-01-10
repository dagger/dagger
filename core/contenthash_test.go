package core

import (
	bkcontenthash "github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChecksumOptsKeyDiffersForOptions(t *testing.T) {
	base := checksumOptsKey(bkcontenthash.ChecksumOpts{}, true)

	follow := checksumOptsKey(bkcontenthash.ChecksumOpts{FollowLinks: true}, true)
	require.NotEqual(t, base, follow)

	wild := checksumOptsKey(bkcontenthash.ChecksumOpts{Wildcard: true}, true)
	require.NotEqual(t, base, wild)

	include := checksumOptsKey(bkcontenthash.ChecksumOpts{IncludePatterns: []string{"a"}}, true)
	exclude := checksumOptsKey(bkcontenthash.ChecksumOpts{ExcludePatterns: []string{"a"}}, true)
	require.NotEqual(t, include, exclude)

	storeFalse := checksumOptsKey(bkcontenthash.ChecksumOpts{}, false)
	require.NotEqual(t, base, storeFalse)

	// identical options should produce identical keys
	base2 := checksumOptsKey(bkcontenthash.ChecksumOpts{}, true)
	require.Equal(t, base, base2)
}

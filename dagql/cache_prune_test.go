package dagql

import (
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestCachePrunePolicyMatchesLegacyRecordTypeListFilter(t *testing.T) {
	policy := CachePrunePolicy{
		Filters: []string{"type==source.local,type==exec.cachemount,type==source.git.checkout"},
	}

	for _, recordType := range []string{"source.local", "exec.cachemount", "source.git.checkout"} {
		require.True(t, cachePrunePolicyMatchesEntry(policy, CacheUsageEntry{
			RecordTypes: []string{recordType},
		}))
	}

	require.False(t, cachePrunePolicyMatchesEntry(policy, CacheUsageEntry{
		RecordTypes: []string{"regular"},
	}))
	require.True(t, cachePrunePolicyMatchesEntry(policy, CacheUsageEntry{
		RecordType:  "mixed",
		RecordTypes: []string{"regular", "exec.cachemount"},
	}))
}

func TestCachePrunePolicyMatchesExistingScalarFilters(t *testing.T) {
	entry := CacheUsageEntry{
		ID:           "entry-id",
		Description:  "entry description",
		RecordType:   "regular",
		ActivelyUsed: true,
	}

	require.True(t, cachePrunePolicyMatchesEntry(CachePrunePolicy{Filters: []string{
		"id==entry-id",
		"description==entry description",
		"recordtype==regular",
		"inuse==true",
	}}, entry))

	require.False(t, cachePrunePolicyMatchesEntry(CachePrunePolicy{Filters: []string{"inuse==false"}}, entry))
	require.False(t, cachePrunePolicyMatchesEntry(CachePrunePolicy{Filters: []string{"type==source.local"}}, entry))
}

func TestPruneLogLiteralValueRendersBytesOpaque(t *testing.T) {
	contents := []byte("binary contents must stay opaque")

	got := pruneLogLiteralValue(nil, &ResultCallLiteral{
		Kind:       ResultCallLiteralKindBytes,
		BytesValue: contents,
	}, 1)

	require.Equal(t, call.DisplayBytes(contents), got)
	require.NotContains(t, got, string(contents))
}

package netaccounting

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

type testSocketTagger struct {
	owner Owner
}

func (t *testSocketTagger) SetSocketOwner(_ syscall.Conn, owner Owner) error {
	t.owner = owner
	return nil
}

func (t *testSocketTagger) SetSocketOwnerBeforeConnect(_ syscall.RawConn, owner Owner) error {
	t.owner = owner
	return nil
}

func TestRegisterSocketTaggerCleanupDoesNotRemoveReplacement(t *testing.T) {
	first := new(testSocketTagger)
	clearFirst := RegisterSocketTagger(first)
	second := new(testSocketTagger)
	clearSecond := RegisterSocketTagger(second)
	t.Cleanup(clearSecond)

	clearFirst()
	require.NoError(t, SetSocketOwner(nil, OwnerDaggerland))
	require.Equal(t, Owner(0), first.owner)
	require.Equal(t, OwnerDaggerland, second.owner)
}

func TestSocketOwnerIsRequired(t *testing.T) {
	require.ErrorIs(t, SetSocketOwner(nil, 0), ErrOwnerRequired)
	require.ErrorIs(t, SetSocketOwnerBeforeConnect(nil, 0), ErrOwnerRequired)
	require.False(t, Owner(0).Valid())
	require.True(t, OwnerUserland.Valid())
	require.True(t, OwnerDaggerland.Valid())
}

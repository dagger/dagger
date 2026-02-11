package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestRemoteFromCacheResultAcceptsStringPayload(t *testing.T) {
	payloadRemote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "refs/heads/main", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		Symrefs: map[string]string{
			"HEAD": "refs/heads/main",
		},
	}
	payload, err := json.Marshal(payloadRemote)
	require.NoError(t, err)

	remote, err := remoteFromCacheResult(string(payload))
	require.NoError(t, err)
	require.Len(t, remote.Refs, 1)
	require.Equal(t, payloadRemote.Refs[0].Name, remote.Refs[0].Name)
	require.Equal(t, payloadRemote.Refs[0].SHA, remote.Refs[0].SHA)
	require.Equal(t, payloadRemote.Symrefs["HEAD"], remote.Symrefs["HEAD"])
}

func TestRemoteFromCacheResultRejectsInvalidPayload(t *testing.T) {
	_, err := remoteFromCacheResult("{not-json")
	require.ErrorContains(t, err, "decode cached remote")
}

func TestRemoteMetadataCacheKeyIsolation(t *testing.T) {
	ctx := context.Background()

	cacheIface, err := dagql.NewCache(ctx, "")
	require.NoError(t, err)

	remotePayload := `{"refs":[]}`
	_, err = cacheIface.GetOrInitArbitrary(ctx, "git-remote-test-dedicated-key", dagql.ArbitraryValueFunc(remotePayload))
	require.NoError(t, err)

	gitInitCalls := 0
	gitPayload := `{"refs":[{"name":"refs/heads/main","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`
	res, err := cacheIface.GetOrInitArbitrary(ctx, "git-current-call-key", func(context.Context) (any, error) {
		gitInitCalls++
		return gitPayload, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, gitInitCalls, "unrelated call key should not be aliased to remote metadata payload")
	require.Equal(t, gitPayload, res.Value())
}

package engineutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/buildkit/executor"
)

// The session attachables helper is mounted only into nested clients'
// containers, next to /.init.
func TestInjectInitSessionHelper(t *testing.T) {
	mounts := func(nested *engine.ClientMetadata) []string {
		state := &execState{
			procInfo:             &executor.ProcessInfo{Meta: executor.Meta{Args: []string{"true"}}},
			nestedClientMetadata: nested,
		}
		require.NoError(t, (&Client{}).injectInit(t.Context(), state))
		require.Equal(t, []string{initPath, "true"}, state.procInfo.Meta.Args)
		var dests []string
		for _, m := range state.mounts {
			dests = append(dests, m.Dest)
		}
		return dests
	}
	require.Equal(t, []string{initPath}, mounts(nil))
	require.Equal(t, []string{initPath}, mounts(&engine.ClientMetadata{}))
	require.Equal(t, []string{initPath, distconsts.InitSessionContainerPath}, mounts(&engine.ClientMetadata{ClientID: "nested"}))
}

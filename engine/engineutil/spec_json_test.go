package engineutil

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/stretchr/testify/require"
)

// runc reads the same config.json as before writeSpec, byte for byte.
func TestWriteSpec(t *testing.T) {
	t.Parallel()
	client := &Client{Opts: &Opts{}}
	state := &execState{
		id: "exec-id",
		procInfo: &executor.ProcessInfo{Meta: executor.Meta{
			Args: []string{"sh", "-c", `echo "<a & b>" é`},
			Env:  []string{"PATH=/usr/bin", "HTML=<&>", "UNICODE=é "},
			Cwd:  "/",
		}},
		networkNamespace: &noopNetworkNamespace{},
		cleanups:         &cleanups.Cleanups{},
	}
	t.Cleanup(func() { require.NoError(t, state.cleanups.Run()) })
	require.NoError(t, client.generateBaseSpec(t.Context(), state))

	for range 3 { // the second and third reuse a buffer
		var want, got bytes.Buffer
		require.NoError(t, json.NewEncoder(&want).Encode(state.spec))
		require.NoError(t, writeSpec(&got, state.spec))
		require.Equal(t, want.String(), got.String())
	}
}

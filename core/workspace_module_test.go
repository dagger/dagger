package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceModuleDecodePersistedCompatibility(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payload string
		main    bool
	}{
		{
			name:    "main",
			payload: `{"Name":"demo","Main":true,"Source":"modules/demo"}`,
			main:    true,
		},
		{
			name:    "legacy entrypoint",
			payload: `{"Name":"demo","Entrypoint":true,"Source":"modules/demo"}`,
			main:    true,
		},
		{
			name:    "legacy entrypoint overrides false main",
			payload: `{"Name":"demo","Main":false,"Entrypoint":true,"Source":"modules/demo"}`,
			main:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			decoded, err := (&WorkspaceModule{}).DecodePersistedObject(
				context.Background(),
				nil,
				json.RawMessage(tc.payload),
			)
			require.NoError(t, err)
			require.Equal(t, &WorkspaceModule{
				Name:   "demo",
				Main:   tc.main,
				Source: "modules/demo",
			}, decoded)
		})
	}
}

package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspacePathCoordinates(t *testing.T) {
	// A scalar cannot resolve a relative path before the operation knows its
	// working directory. Keep parent traversal for that boundary check.
	for _, tc := range []struct{ input, want string }{
		{"", "."},
		{"../src/./main.go", "../src/main.go"},
		{"docs/../src", "src"},
		{"/docs/../src", "/src"},
		{`docs\guide.md`, "docs/guide.md"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			decoded, err := WorkspacePath("").DecodeInput(tc.input)
			require.NoError(t, err)
			require.Equal(t, WorkspacePath(tc.want), decoded)
			payload, err := json.Marshal(decoded)
			require.NoError(t, err)
			var restored WorkspacePath
			require.NoError(t, json.Unmarshal(payload, &restored))
			require.Equal(t, decoded, restored)
		})
	}
	_, err := WorkspacePath("").DecodeInput(42)
	require.Error(t, err)
	_, err = WorkspacePath("").DecodeInput("docs\x00/file")
	require.Error(t, err)
}

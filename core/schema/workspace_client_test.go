package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceClientSDKOutputPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		clientPath string
		moduleRef  string
		expected   string
	}{
		{
			name:       "root module client",
			clientPath: "dagger",
			moduleRef:  ".",
			expected:   "dagger",
		},
		{
			name:       "client in nested module root",
			clientPath: "dir/dir",
			moduleRef:  "dir/dir",
			expected:   ".",
		},
		{
			name:       "client below nested module root",
			clientPath: "dir/dir/generated",
			moduleRef:  "dir/dir",
			expected:   "generated",
		},
		{
			name:       "workspace app client targets local module",
			clientPath: "app/lib/dagger-client",
			moduleRef:  ".dagger/modules/api",
			expected:   "../../../app/lib/dagger-client",
		},
		{
			name:       "remote target keeps workspace path",
			clientPath: "app/lib/dagger-client",
			moduleRef:  "github.com/dagger/example",
			expected:   "app/lib/dagger-client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			actual, err := workspaceClientSDKOutputPath(tt.clientPath, tt.moduleRef)
			require.NoError(t, err)
			require.Equal(t, tt.expected, actual)
		})
	}
}

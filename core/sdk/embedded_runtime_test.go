package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedRuntimeFilename(t *testing.T) {
	testcases := []struct {
		source        string
		filename      string
		expectedError string
	}{
		{
			source:   "embed:runtime.dang",
			filename: "runtime.dang",
		},
		{
			source:   "embed:my-runtime.dang",
			filename: "my-runtime.dang",
		},
		{
			source:        "embed:",
			expectedError: "missing filename",
		},
		{
			source:        "embed:.",
			expectedError: "must be a bare file name",
		},
		{
			source:        "embed:..",
			expectedError: "must be a bare file name",
		},
		{
			source:        "embed:../runtime.dang",
			expectedError: "must be a bare file name",
		},
		{
			source:        "embed:sub/runtime.dang",
			expectedError: "must be a bare file name",
		},
		{
			source:        `embed:sub\runtime.dang`,
			expectedError: "must be a bare file name",
		},
		{
			source:        "embed:/etc/passwd.dang",
			expectedError: "must be a bare file name",
		},
		{
			source:        "embed:runtime[1].dang",
			expectedError: "must not contain pattern metacharacters",
		},
		{
			source:        "embed:run*.dang",
			expectedError: "must not contain pattern metacharacters",
		},
		{
			source:        "embed:!runtime.dang",
			expectedError: "must not contain pattern metacharacters",
		},
		{
			source:        "embed:runtime.txt",
			expectedError: "must end in .dang",
		},
		{
			source:        "embed:runtime",
			expectedError: "must end in .dang",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.source, func(t *testing.T) {
			require.True(t, IsEmbeddedRuntimeSource(tc.source))
			filename, err := EmbeddedRuntimeFilename(tc.source)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.filename, filename)
		})
	}
}

func TestIsEmbeddedRuntimeSource(t *testing.T) {
	for _, source := range []string{"go", "dang", "python", "./my-sdk", "github.com/user/repo@main", "embedded", ""} {
		require.False(t, IsEmbeddedRuntimeSource(source), source)
	}
	require.True(t, IsEmbeddedRuntimeSource("embed:runtime.dang"))
}

package control

import (
	"testing"

	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/stretchr/testify/require"
)

func TestDuplicateCacheOptions(t *testing.T) {
	var testCases = []struct {
		name     string
		opts     []*controlapi.CacheOptionsEntry
		expected []*controlapi.CacheOptionsEntry
	}{
		{
			name: "avoids unique opts",
			opts: []*controlapi.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/ref:v1.0.0",
					},
				},
				{
					Type: "local",
					Attrs: map[string]string{
						"dest": "/path/for/export",
					},
				},
			},
			expected: nil,
		},
		{
			name: "finds duplicate opts",
			opts: []*controlapi.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/ref:v1.0.0",
					},
				},
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/ref:v1.0.0",
					},
				},
				{
					Type: "local",
					Attrs: map[string]string{
						"dest": "/path/for/export",
					},
				},
				{
					Type: "local",
					Attrs: map[string]string{
						"dest": "/path/for/export",
					},
				},
			},
			expected: []*controlapi.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/ref:v1.0.0",
					},
				},
				{
					Type: "local",
					Attrs: map[string]string{
						"dest": "/path/for/export",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := findDuplicateCacheOptions(tc.opts)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.expected, result)
		})
	}
}

func TestParseCacheExportIgnoreError(t *testing.T) {
	tests := map[string]struct {
		expectedIgnoreError bool
		expectedSupported   bool
	}{
		"": {
			expectedIgnoreError: false,
			expectedSupported:   false,
		},
		".": {
			expectedIgnoreError: false,
			expectedSupported:   false,
		},
		"fake": {
			expectedIgnoreError: false,
			expectedSupported:   false,
		},
		"true": {
			expectedIgnoreError: true,
			expectedSupported:   true,
		},
		"True": {
			expectedIgnoreError: true,
			expectedSupported:   true,
		},
		"TRUE": {
			expectedIgnoreError: true,
			expectedSupported:   true,
		},
		"truee": {
			expectedIgnoreError: false,
			expectedSupported:   false,
		},
		"false": {
			expectedIgnoreError: false,
			expectedSupported:   true,
		},
		"False": {
			expectedIgnoreError: false,
			expectedSupported:   true,
		},
		"FALSE": {
			expectedIgnoreError: false,
			expectedSupported:   true,
		},
		"ffalse": {
			expectedIgnoreError: false,
			expectedSupported:   false,
		},
	}

	for ignoreErrStr, test := range tests {
		t.Run(ignoreErrStr, func(t *testing.T) {
			ignoreErr, supported := parseCacheExportIgnoreError(ignoreErrStr)
			t.Log("checking expectedIgnoreError")
			require.Equal(t, ignoreErr, test.expectedIgnoreError)
			t.Log("checking expectedSupported")
			require.Equal(t, supported, test.expectedSupported)
		})
	}
}

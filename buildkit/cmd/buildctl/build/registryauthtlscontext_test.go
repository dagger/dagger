package build

import (
	"testing"

	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/stretchr/testify/require"
)

func TestParseRegistryAuthTLSContext(t *testing.T) {
	type testCase struct {
		registryAuthTLSContext []string //--registry-auth-tlscontext
		expected               map[string]*authprovider.AuthTLSConfig
		expectedErr            string
	}
	testCases := []testCase{
		{
			registryAuthTLSContext: []string{
				"host=tcp://myserver:2376,ca=/home/admin/ca-file,cert=/home/admin/cert-file,key=/home/admin/key-file",
			},
			expected: map[string]*authprovider.AuthTLSConfig{
				"tcp://myserver:2376": {
					RootCAs: []string{
						"/home/admin/ca-file",
					},
					KeyPairs: []authprovider.TLSKeyPair{
						{
							Key:         "/home/admin/key-file",
							Certificate: "/home/admin/cert-file",
						},
					},
				},
			},
		},
		{
			registryAuthTLSContext: []string{
				"host=tcp://myserver:2376,cert=/home/admin/cert-file,key=/home/admin/key-file",
			},
			expected: map[string]*authprovider.AuthTLSConfig{
				"tcp://myserver:2376": {
					KeyPairs: []authprovider.TLSKeyPair{
						{
							Key:         "/home/admin/key-file",
							Certificate: "/home/admin/cert-file",
						},
					},
				},
			},
		},
		{
			registryAuthTLSContext: []string{
				"host=tcp://myserver:2376,ca=/home/admin/ca-file",
			},
			expected: map[string]*authprovider.AuthTLSConfig{
				"tcp://myserver:2376": {
					RootCAs: []string{
						"/home/admin/ca-file",
					},
				},
			},
		},
		{
			registryAuthTLSContext: []string{
				"host=tcp://myserver:2376,ca=/home/admin/ca-file,key=/home/admin/key-file",
			},
			expectedErr: "--registry-auth-tlscontext requires cert=<cert>,key=<key>",
		},
		{
			registryAuthTLSContext: []string{
				"host=tcp://myserver:2376,ca=/home/admin/ca-file,cert=/home/admin/cert-file,key=/home/admin/key-file",
				"host=https://myserver:2376,ca=/path/to/my/ca.crt,cert=/path/to/my/cert.crt,key=/path/to/my/key.crt",
			},
			expected: map[string]*authprovider.AuthTLSConfig{
				"tcp://myserver:2376": {
					RootCAs: []string{
						"/home/admin/ca-file",
					},
					KeyPairs: []authprovider.TLSKeyPair{
						{
							Key:         "/home/admin/key-file",
							Certificate: "/home/admin/cert-file",
						},
					},
				},
				"https://myserver:2376": {
					RootCAs: []string{
						"/path/to/my/ca.crt",
					},
					KeyPairs: []authprovider.TLSKeyPair{
						{
							Key:         "/path/to/my/key.crt",
							Certificate: "/path/to/my/cert.crt",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		im, err := ParseRegistryAuthTLSContext(tc.registryAuthTLSContext)
		if tc.expectedErr == "" {
			require.EqualValues(t, tc.expected, im)
		} else {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		}
	}
}

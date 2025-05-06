package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecretParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		input            string
		expectedURI      string
		expectedCacheKey string
	}{
		{
			name:             "env",
			input:            "env://FOO",
			expectedURI:      "env://FOO",
			expectedCacheKey: "",
		},
		{
			name:             "env with cacheKey",
			input:            "env://FOO?cacheKey=bar",
			expectedURI:      "env://FOO",
			expectedCacheKey: "bar",
		},

		{
			name:             "file",
			input:            "file:///home/user/.ssh/id_rsa",
			expectedURI:      "file:///home/user/.ssh/id_rsa",
			expectedCacheKey: "",
		},
		{
			name:             "file with cacheKey",
			input:            "file:///home/user/.ssh/id_rsa?cacheKey=bar",
			expectedURI:      "file:///home/user/.ssh/id_rsa",
			expectedCacheKey: "bar",
		},

		{
			name:             "cmd",
			input:            "cmd://echo foo",
			expectedURI:      "cmd://echo foo",
			expectedCacheKey: "",
		},
		{
			name:             "cmd with cacheKey",
			input:            "cmd://echo foo?cacheKey=bar",
			expectedURI:      "cmd://echo foo",
			expectedCacheKey: "bar",
		},

		{
			name:             "op",
			input:            "op://foo",
			expectedURI:      "op://foo",
			expectedCacheKey: "",
		},
		{
			name:             "op with cacheKey",
			input:            "op://foo?cacheKey=bar",
			expectedURI:      "op://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "op with cacheKey and other query params",
			input:            "op://foo?cacheKey=bar&other=param",
			expectedURI:      "op://foo?other=param",
			expectedCacheKey: "bar",
		},
		{
			name:             "op with cacheKey and other query params different order",
			input:            "op://foo?other=param&cacheKey=bar",
			expectedURI:      "op://foo?other=param",
			expectedCacheKey: "bar",
		},

		{
			name:             "vault",
			input:            "vault://foo",
			expectedURI:      "vault://foo",
			expectedCacheKey: "",
		},
		{
			name:             "vault with cacheKey",
			input:            "vault://foo?cacheKey=bar",
			expectedURI:      "vault://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "vault with cacheKey and other query params",
			input:            "vault://foo?cacheKey=bar&other=param",
			expectedURI:      "vault://foo?other=param",
			expectedCacheKey: "bar",
		},

		{
			name:             "libsecret",
			input:            "libsecret://foo",
			expectedURI:      "libsecret://foo",
			expectedCacheKey: "",
		},
		{
			name:             "libsecret with cacheKey",
			input:            "libsecret://foo?cacheKey=bar",
			expectedURI:      "libsecret://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "libsecret with cacheKey and other query params",
			input:            "libsecret://foo?cacheKey=bar&other=param",
			expectedURI:      "libsecret://foo?other=param",
			expectedCacheKey: "bar",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := secretValue{}
			err := v.Set(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.expectedURI, v.uri)
			require.Equal(t, tc.expectedCacheKey, v.cacheKey)
		})
	}
}

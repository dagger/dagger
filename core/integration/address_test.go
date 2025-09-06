package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type AddressSuite struct{}

func TestAddress(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AddressSuite{})
}

func (AddressSuite) TestSecretParse(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
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
			input:            "env://FOO?cacheKey=bar1",
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
			input:            "file:///home/user/.ssh/id_rsa?cacheKey=bar2",
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
			input:            "cmd://echo foo?cacheKey=bar3",
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
			input:            "op://foo?cacheKey=barr4",
			expectedURI:      "op://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "op with cacheKey and other query params",
			input:            "op://foo?cacheKey=bar5&other=param",
			expectedURI:      "op://foo?other=param",
			expectedCacheKey: "bar",
		},
		{
			name:             "op with cacheKey and other query params different order",
			input:            "op://foo?other=param&cacheKey=bar6",
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
			input:            "vault://foo?cacheKey=bar7",
			expectedURI:      "vault://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "vault with cacheKey and other query params",
			input:            "vault://foo?cacheKey=bar8&other=param",
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
			input:            "libsecret://foo?cacheKey=bar9",
			expectedURI:      "libsecret://foo",
			expectedCacheKey: "bar",
		},
		{
			name:             "libsecret with cacheKey and other query params",
			input:            "libsecret://foo?cacheKey=bar10&other=param",
			expectedURI:      "libsecret://foo?other=param",
			expectedCacheKey: "bar",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			uri, err := c.Address(tc.input).Secret().URI(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedURI, uri)
			// FIXME: can't test expected cache key, it's not exposed in the API
			// test expected cache key digest instead?
		})
	}
}

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type AddressSuite struct{}

func TestAddress(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AddressSuite{})
}

func (AddressSuite) TestContainer(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Address(alpineImage).Container()
	ref, err := ctr.ImageRef(ctx)
	require.NoError(t, err)
	require.Contains(t, ref, "alpine")
	requireFileContains(ctx, t, distconsts.AlpineVersion, ctr.File("/etc/alpine-release"))
}

func (AddressSuite) TestSecret(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		input       string
		expectedURI string
	}{
		{
			input:       "env://FOO",
			expectedURI: "env://FOO",
		},
		{
			input:       "env://FOO?cacheKey=bar1",
			expectedURI: "env://FOO",
		},

		{
			input:       "file:///home/user/.ssh/id_rsa",
			expectedURI: "file:///home/user/.ssh/id_rsa",
		},
		{
			input:       "file:///home/user/.ssh/id_rsa?cacheKey=bar2",
			expectedURI: "file:///home/user/.ssh/id_rsa",
		},

		{
			input:       "cmd://echo foo",
			expectedURI: "cmd://echo foo",
		},
		{
			input:       "cmd://echo foo?cacheKey=bar3",
			expectedURI: "cmd://echo foo",
		},

		{
			input:       "op://foo",
			expectedURI: "op://foo",
		},
		{
			input:       "op://foo?cacheKey=barr4",
			expectedURI: "op://foo",
		},
		{
			input:       "op://foo?cacheKey=bar5&other=param",
			expectedURI: "op://foo?other=param",
		},
		{
			input:       "op://foo?other=param&cacheKey=bar6",
			expectedURI: "op://foo?other=param",
		},

		{
			input:       "vault://foo",
			expectedURI: "vault://foo",
		},
		{
			input:       "vault://foo?cacheKey=bar7",
			expectedURI: "vault://foo",
		},
		{
			input:       "vault://foo?cacheKey=bar8&other=param",
			expectedURI: "vault://foo?other=param",
		},

		{
			input:       "libsecret://foo",
			expectedURI: "libsecret://foo",
		},
		{
			input:       "libsecret://foo?cacheKey=bar9",
			expectedURI: "libsecret://foo",
		},
		{
			input:       "libsecret://foo?cacheKey=bar10&other=param",
			expectedURI: "libsecret://foo?other=param",
		},
	} {
		tc := tc
		t.Run(strings.Replace(tc.input, "://", ": ", 1), func(ctx context.Context, t *testctx.T) {
			uri, err := c.Address(tc.input).Secret().URI(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedURI, uri)
		})
	}
}

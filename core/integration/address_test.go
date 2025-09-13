package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type AddressSuite struct{}

func TestAddress(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AddressSuite{})
}

func (AddressSuite) TestValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, input := range []string{
		"foo",
		"bar",
		"123",
		"env://DEBUG",
		"cmd://echo hello world",
		"https://github.com/dagger/dagger",
		"tcp://localhost:4242",
		"unix:///var/run/docker.sock",
	} {
		value, err := c.Address(input).Value(ctx)
		require.NoError(t, err)
		require.Equal(t, input, value)
	}
}

func (AddressSuite) TestContainer(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Address(alpineImage).Container()
	ref, err := ctr.ImageRef(ctx)
	require.NoError(t, err)
	require.Contains(t, ref, "alpine")
	requireFileContains(ctx, t, distconsts.AlpineVersion, ctr.File("/etc/alpine-release"))
}

func (AddressSuite) TestFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git", func(ctx context.Context, t *testctx.T) {
		requireFileContains(ctx, t,
			"copyright",
			c.Address("https://github.com/dagger/dagger#main:LICENSE").File(),
		)
	})
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		tmp, err := os.MkdirTemp("", "dagger-addr-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmp)
		helloPath := tmp + "/hello.txt"
		err = os.WriteFile(helloPath, []byte("hello there"), 0644)
		require.NoError(t, err)
		requireFileContains(ctx, t, "hello there", c.Address(helloPath).File())
	})
}

func (AddressSuite) TestDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git", func(ctx context.Context, t *testctx.T) {
		requireFileContains(ctx, t,
			"copyright",
			c.Address("https://github.com/dagger/dagger#main").Directory().File("LICENSE"),
		)
	})
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		tmp, err := os.MkdirTemp("", "dagger-addr-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmp)
		err = os.WriteFile(tmp+"/hello.txt", []byte("hello there"), 0644)
		require.NoError(t, err)
		requireFileContains(ctx, t,
			"hello there",
			c.Address(tmp).Directory().File("hello.txt"),
		)
	})
}

func (AddressSuite) TestSecret(ctx context.Context, t *testctx.T) {
	t.Run("env variable", func(ctx context.Context, t *testctx.T) {
		os.Setenv("hello", "kitty")
		c := connect(ctx, t)
		plaintext, err := c.Address("env://hello").Secret().Plaintext(ctx)
		require.NoError(t, err)
		require.Equal(t, "kitty", plaintext)
	})

	t.Run("command", func(ctx context.Context, t *testctx.T) {
		tmp, err := os.MkdirTemp("", "dagger-addr-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmp)
		c := connect(ctx, t, dagger.WithWorkdir(tmp))
		err = os.WriteFile(tmp+"/hello.txt", []byte("hello there"), 0644)
		plaintext, err := c.Address("cmd://cat hello.txt").Secret().Plaintext(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello there", plaintext)
	})
}

func (AddressSuite) TestSecretURI(ctx context.Context, t *testctx.T) {
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

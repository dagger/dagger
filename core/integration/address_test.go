package core

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil"
	daggerio "dagger.io/dagger"
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

func (AddressSuite) TestService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	port := tcpService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello world"))
	})
	t.Run("simple", func(ctx context.Context, t *testctx.T) {
		host := c.Address(fmt.Sprintf("tcp://localhost:%d", port)).Service()
		url := fmt.Sprintf("http://www:%d", port)
		require.Equal(t, "hello world", httpQuery(t, c, host, url))
	})

	t.Run("invalid service", func(ctx context.Context, t *testctx.T) {
		for _, value := range []string{
			"",
			"localhost:80",
			"tcp://",
			"http://localhost",
			"foo://bar",
		} {
			_, err := c.Address(value).Service().ID(ctx)
			require.Error(t, err)
		}
	})
}

func (AddressSuite) TestLocalFile(ctx context.Context, t *testctx.T) {
	tmp := t.TempDir()
	c := connect(ctx, t, daggerio.WithWorkdir(tmp))
	err := os.WriteFile(tmp+"/hello.txt", []byte("hello there"), 0644)
	require.NoError(t, err)
	// Absolute file path
	requireFileContains(ctx, t,
		"hello there",
		c.Address(tmp+"/hello.txt").File(),
	)
	// Relative file path
	requireFileContains(ctx, t,
		"hello there",
		c.Address("./hello.txt").File(),
	)
}

func (AddressSuite) TestLocalDirectory(ctx context.Context, t *testctx.T) {
	tmp := t.TempDir()
	c := connect(ctx, t, daggerio.WithWorkdir(tmp))
	err := os.WriteFile(tmp+"/hello.txt", []byte("hello there"), 0644)
	require.NoError(t, err)
	// Absolute directory path
	requireFileContains(ctx, t,
		"hello there",
		c.Address(tmp).Directory().File("hello.txt"),
	)
	// Relative directory path
	requireFileContains(ctx, t,
		"hello there",
		c.Address(".").Directory().File("hello.txt"),
	)
}

func (AddressSuite) TestGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("remote repo", func(ctx context.Context, t *testctx.T) {
		requireSampleGitRepo(ctx, t, c, c.Address("https://github.com/dagger/dagger").GitRepository())
	})

	t.Run("remote branch", func(ctx context.Context, t *testctx.T) {
		var refs []*dagger.GitRef
		for _, fragment := range []string{
			"",
			"#main",
			"#refs/heads/main",
		} {
			addr := "https://github.com/dagger/dagger" + fragment
			refs = append(refs, c.Address(addr).GitRef())
		}
		requireGitRefCommitsEqual(ctx, t, refs...)
		for _, ref := range refs {
			requireSampleGitBranch(ctx, t, c, ref)
		}
	})

	t.Run("remote tag", func(ctx context.Context, t *testctx.T) {
		requireSampleGitTag(ctx, t, c,
			c.Address("https://github.com/dagger/dagger#v0.9.5").GitRef(),
		)
		requireGitRefIsSampleAnnotatedTag(ctx, t, c,
			c.Address("https://github.com/dagger/dagger#v0.6.1").GitRef(),
		)
	})

	t.Run("remote commit", func(ctx context.Context, t *testctx.T) {
		requireSampleGitCommit(ctx, t, c,
			c.Address("https://github.com/dagger/dagger#c80ac2c13df7d573a069938e01ca13f7a81f0345").GitRef(),
		)
		requireSampleGitHiddenCommit(ctx, t, c,
			c.Address("https://github.com/dagger/dagger#318970484f692d7a76cfa533c5d47458631c9654").GitRef(),
		)
	})

	t.Run("remote directory & file", func(ctx context.Context, t *testctx.T) {
		for _, ref := range []string{
			"",
			"c80ac2c13df7d573a069938e01ca13f7a81f0345",
			"318970484f692d7a76cfa533c5d47458631c9654",
			"main",
			"refs/heads/main",
			"v0.9.5",
			"v0.6.1",
		} {
			var subdir string
			switch ref {
			default:
				subdir = "cmd/dagger"
			case "c80ac2c13df7d573a069938e01ca13f7a81f0345":
				subdir = "cmd/cloak"
			}
			requireSampleGitRootDir(ctx, t, c,
				c.Address(fmt.Sprintf("https://github.com/dagger/dagger#%s", ref)).Directory(),
			)
			requireSampleGitSubDir(ctx, t, c,
				c.Address(fmt.Sprintf("https://github.com/dagger/dagger#%s:%s", ref, subdir)).Directory(),
			)
			requireSampleGitFile(ctx, t, c,
				c.Address(fmt.Sprintf("https://github.com/dagger/dagger#%s:%s/main.go", ref, subdir)).File(),
			)
		}
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
		c := connect(ctx, t)
		plaintext, err := c.Address("cmd://echo hello there").Secret().Plaintext(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello there\n", plaintext)
	})

	t.Run("uri", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		for _, tc := range []struct {
			input       string
			expectedURI string
		}{
			{"env://FOO", "env://FOO"},
			{"env://FOO?cacheKey=bar1", "env://FOO"},
			{"file:///home/user/.ssh/id_rsa", "file:///home/user/.ssh/id_rsa"},
			{"file:///home/user/.ssh/id_rsa?cacheKey=bar2", "file:///home/user/.ssh/id_rsa"},
			{"cmd://echo foo", "cmd://echo foo"},
			{"cmd://echo foo?cacheKey=bar3", "cmd://echo foo"},
			{"op://foo", "op://foo"},
			{"op://foo?cacheKey=barr4", "op://foo"},
			{"op://foo?cacheKey=bar5&other=param", "op://foo?other=param"},
			{"op://foo?other=param&cacheKey=bar6", "op://foo?other=param"},
			{"vault://foo", "vault://foo"},
			{"vault://foo?cacheKey=bar7", "vault://foo"},
			{"vault://foo?cacheKey=bar8&other=param", "vault://foo?other=param"},
			{"libsecret://foo", "libsecret://foo"},
			{"libsecret://foo?cacheKey=bar9", "libsecret://foo"},
			{"libsecret://foo?cacheKey=bar10&other=param", "libsecret://foo?other=param"},
		} {
			tc := tc
			t.Run(strings.Replace(tc.input, "://", ": ", 1), func(ctx context.Context, t *testctx.T) {
				uri, err := c.Address(tc.input).Secret().URI(ctx)
				require.NoError(t, err)
				require.Equal(t, tc.expectedURI, uri)
			})
		}
	})
}

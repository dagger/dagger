package core

import (
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/internal/testutil"
)

func init() {
	if err := testutil.SetupBuildkitd(); err != nil {
		panic(err)
	}
}

func newCache(t *testing.T) core.CacheID {
	var res struct {
		CacheFromTokens struct {
			ID core.CacheID
		}
	}

	err := testutil.Query(`
		query CreateCache($token: String!) {
			cacheFromTokens(tokens: [$token]) {
				id
			}
		}
	`, &res, &testutil.QueryOptions{Variables: map[string]any{
		"token": identity.NewID(),
	}})
	require.NoError(t, err)

	return res.CacheFromTokens.ID
}

func dirWithFileID(t *testing.T, path string, contents string) core.DirectoryID {
	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}{}

	err := testutil.Query(
		`query Test($path: String!, $contents: String!) {
			directory {
				withNewFile(path: $path, contents: $contents) {
					id
				}
			}
		}`, &dirRes, &testutil.QueryOptions{Variables: map[string]any{
			"path":     path,
			"contents": contents,
		}})
	require.NoError(t, err)

	return dirRes.Directory.WithNewFile.ID
}

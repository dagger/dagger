package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/internal/testutil"
)

func TestHTTP(t *testing.T) {
	t.Parallel()

	var res struct {
		HTTP struct {
			Contents string
		}
	}

	err := testutil.Query(
		`{
			http(url: "https://raw.githubusercontent.com/dagger/dagger/main/README.md") {
				contents
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.HTTP.Contents)
	require.Contains(t, res.HTTP.Contents, "Dagger")
}

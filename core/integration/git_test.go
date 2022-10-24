package core

import (
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestGit(t *testing.T) {
	t.Parallel()

	res := struct {
		Git struct {
			Branch struct {
				Tree struct {
					File struct {
						Contents string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			git(url: "github.com/dagger/dagger") {
				branch(name: "main") {
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Git.Branch.Tree.File.Contents, "Dagger")
}

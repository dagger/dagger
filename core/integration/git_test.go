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
			Commit struct {
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
				commit(id: "c80ac2c13df7d573a069938e01ca13f7a81f0345") {
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
	require.Contains(t, res.Git.Commit.Tree.File.Contents, "Dagger")
}

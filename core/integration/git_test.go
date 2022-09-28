package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/internal/testutil"
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

// Test backwards compatibility with old git API
func TestGitOld(t *testing.T) {
	t.Parallel()

	res := struct {
		Core struct {
			Git struct {
				File string
			}
		}
	}{}

	err := testutil.Query(
		`{
			core {
				git(remote: "github.com/dagger/dagger") {
					file(path: "README.md", lines: 1)
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Core.Git.File, "Dagger")
}

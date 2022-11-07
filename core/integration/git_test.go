package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
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
			git(url: "github.com/dagger/dagger", keepGitDir: true) {
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

func TestGitKeepGitDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := dagger.Connect(ctx)
	defer client.Close()

	t.Run("git dir is present", func(t *testing.T) {
		dir := client.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.Contains(t, ent, ".git")
	})

	t.Run("git dir is not present", func(t *testing.T) {
		dir := client.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.NotContains(t, ent, ".git")
	})
}

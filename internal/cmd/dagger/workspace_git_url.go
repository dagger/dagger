package daggercmd

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/spf13/cobra"
)

func newWorkspaceGitURLCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "url",
		Short: "Print the resolved Git URL, including the selected subdirectory",
		Long: `Print the resolved Git URL, including the selected subdirectory.

Use the repository and ref loaded for the selected workspace.
The output has the form protocol://repository#ref:subdirectory.
Omit the subdirectory when the workspace is at the repository root.

For local workspaces, use the origin remote and the loaded ref.
A local workspace must have an origin remote.

Use the output with dagger -W to select the same remote location.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withEngine(cmd.Context(), client.Params{
				SkipWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				resolved, err := workspaceGitURL(ctx, engineClient.Dagger())
				if err != nil {
					return fmt.Errorf("read workspace git url: %w", err)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return err
			})
		},
	}
}

func workspaceGitURL(ctx context.Context, dag *dagger.Client) (string, error) {
	var result struct {
		CurrentWorkspace struct {
			Address string
			Cwd     string
			Git     struct {
				Repository struct {
					URL string
				}
				Head struct {
					Name      string
					CommitSHA string
				}
			}
		}
	}
	// The repository URL belongs to the loaded ref. Parsing the workspace
	// selector again could choose a different transport, ref, or vanity path.
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query WorkspaceGitURL {
			currentWorkspace {
				address cwd
				git { repository: __repository { url } head { name commitSHA } }
			}
		}`,
	}, &dagger.Response{Data: &result}); err != nil {
		return "", err
	}
	ws := result.CurrentWorkspace
	cloneURL := ws.Git.Repository.URL
	if cloneURL == "" {
		// Local repository objects have no remote URL. Read only origin on
		// the client; the ref and selected path still come from the API.
		var err error
		cloneURL, err = localWorkspaceGitOrigin(ctx, ws.Address)
		if err != nil {
			return "", err
		}
	}
	return formatWorkspaceGitURL(cloneURL, cmp.Or(ws.Git.Head.Name, ws.Git.Head.CommitSHA), ws.Cwd)
}

func localWorkspaceGitOrigin(ctx context.Context, address string) (string, error) {
	location, err := url.Parse(address)
	if err != nil || location.Scheme != "file" {
		return "", fmt.Errorf("selected workspace has no remote Git URL")
	}
	dir := filepath.FromSlash(location.Path)
	origin, err := gitOutput(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("read selected workspace Git origin: %w", err)
	}
	return origin, nil
}

func formatWorkspaceGitURL(cloneURL, ref, cwd string) (string, error) {
	repo, err := gitutil.ParseURL(cloneURL)
	if err != nil {
		return "", fmt.Errorf("invalid remote Git URL %q: %w", cloneURL, err)
	}
	if repo.Host == "" {
		return "", fmt.Errorf("remote Git URL %q has no host", cloneURL)
	}
	if ref == "" {
		return "", fmt.Errorf("selected workspace has no resolved Git ref")
	}
	subdir, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	subdir = filepath.ToSlash(subdir)
	if strings.ContainsAny(ref, "#%") || strings.ContainsAny(subdir, "#%") {
		return "", fmt.Errorf("git URL cannot represent a ref or subdirectory containing # or %%")
	}
	// Construct an explicit URL even when origin uses SCP syntax. Keep the
	// fragment literal: Dagger's #ref:subdirectory parser does not URL-decode it.
	base := (&url.URL{Scheme: repo.Scheme, User: repo.User, Host: repo.Host, Path: repo.Path}).String()
	resolved := base + "#" + ref
	if subdir != "" {
		resolved += ":" + subdir
	}
	return resolved, nil
}

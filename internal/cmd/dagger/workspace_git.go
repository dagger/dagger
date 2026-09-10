package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var workspaceGitCmd = newWorkspaceGitCmd()

func newWorkspaceGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Inspect Git metadata for the selected workspace",
		Long: `Inspect Git metadata for the selected local or remote workspace.

These commands use the workspace's selected Git ref and repository.
They do not load installed modules.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newWorkspaceGitValueCmd("ref", "Print the resolved ref name, or commit hash if unnamed", func(ctx context.Context, git *dagger.WorkspaceGit) (string, error) {
			return git.Head().Name(ctx)
		}),
		newWorkspaceGitValueCmd("sha", "Print the full commit hash", func(ctx context.Context, git *dagger.WorkspaceGit) (string, error) {
			return git.Head().CommitSHA(ctx)
		}),
	)
	dirtyCmd := newWorkspaceGitValueCmd("dirty", "Print whether the workspace has uncommitted changes", func(ctx context.Context, git *dagger.WorkspaceGit) (string, error) {
		empty, err := git.Uncommitted().IsEmpty(ctx)
		return strconv.FormatBool(!empty), err
	})
	dirtyCmd.Long = `Print true if the workspace has uncommitted changes, or false if it is clean.

Changes include staged files, unstaged files, and untracked files.
Git ignore rules apply to untracked files.
Both true and false return exit status 0.`
	cmd.AddCommand(dirtyCmd, newWorkspaceGitLogCmd(), newWorkspaceGitURLCmd())
	return cmd
}

func newWorkspaceGitValueCmd(name, description string, value func(context.Context, *dagger.WorkspaceGit) (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withEngine(cmd.Context(), client.Params{
				SkipWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				result, err := value(ctx, engineClient.Dagger().CurrentWorkspace().Git())
				if err != nil {
					return fmt.Errorf("read workspace git %s: %w", name, err)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), result)
				return err
			})
		},
	}
}

func newWorkspaceGitLogCmd() *cobra.Command {
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Print the workspace commit history",
		Long: `Print commit history from the workspace's selected Git ref.

Each line contains a short commit hash and the first line of its message.
The default limit is 10 commits. The history covers the whole repository.

Use --json to print an array with full commit hashes, short hashes, author
and committer names, email addresses, dates, messages, and parent hashes.
Dates use RFC3339 format.`,
		Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be greater than zero")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withEngine(cmd.Context(), client.Params{
				SkipWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				commits, err := workspaceGitLog(ctx, engineClient.Dagger(), limit, jsonOutput)
				if err != nil {
					return fmt.Errorf("read workspace git log: %w", err)
				}
				return printWorkspaceGitLog(cmd.OutOrStdout(), commits, jsonOutput)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of commits (must be greater than zero)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print commit metadata as JSON")
	return cmd
}

type workspaceGitCommit struct {
	SHA             string   `json:"sha"`
	ShortSHA        string   `json:"shortSha"`
	AuthorName      string   `json:"authorName"`
	AuthorEmail     string   `json:"authorEmail"`
	AuthoredDate    string   `json:"authoredDate"`
	CommitterName   string   `json:"committerName"`
	CommitterEmail  string   `json:"committerEmail"`
	CommittedDate   string   `json:"committedDate"`
	Message         string   `json:"message"`
	MessageHeadline string   `json:"messageHeadline"`
	MessageBody     string   `json:"messageBody"`
	ParentSHAs      []string `json:"parentShas"`
}

func workspaceGitLog(ctx context.Context, dag *dagger.Client, limit int, fullMetadata bool) ([]workspaceGitCommit, error) {
	fields := "shortSha messageHeadline"
	if fullMetadata {
		fields += ` sha authorName authorEmail authoredDate committerName committerEmail
			committedDate message messageBody parentShas`
	}
	var result struct {
		CurrentWorkspace struct {
			Git struct {
				Head struct {
					Log []workspaceGitCommit
				}
			}
		}
	}
	// Fetch all requested metadata in one query, without a request per field
	// and commit through the generated SDK accessors.
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query WorkspaceGitLog($limit: Int!) {
			currentWorkspace { git { head { log(limit: $limit) { ` + fields + ` } } } }
		}`,
		Variables: map[string]any{"limit": limit},
	}, &dagger.Response{Data: &result}); err != nil {
		return nil, err
	}
	return result.CurrentWorkspace.Git.Head.Log, nil
}

func printWorkspaceGitLog(out io.Writer, commits []workspaceGitCommit, jsonOutput bool) error {
	if jsonOutput {
		if commits == nil {
			commits = []workspaceGitCommit{}
		}
		return json.NewEncoder(out).Encode(commits)
	}
	for _, commit := range commits {
		if _, err := fmt.Fprintf(out, "%s %s\n", commit.ShortSHA, commit.MessageHeadline); err != nil {
			return err
		}
	}
	return nil
}

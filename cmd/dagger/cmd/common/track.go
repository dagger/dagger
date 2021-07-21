package common

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/spf13/cobra"
	"go.dagger.io/dagger/state"
	"go.dagger.io/dagger/telemetry"
)

// TrackCommand sends telemetry about a command execution
func TrackCommand(ctx context.Context, cmd *cobra.Command, props ...*telemetry.Property) chan struct{} {
	props = append([]*telemetry.Property{
		{
			Name:  "command",
			Value: commandName(cmd),
		},
	}, props...)

	return telemetry.TrackAsync(ctx, "Command Executed", props...)
}

func commandName(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c.Parent() != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}

// TrackWorkspaceCommand is like TrackCommand but includes workspace and
// optionally environment metadata.
func TrackWorkspaceCommand(ctx context.Context, cmd *cobra.Command, w *state.Workspace, env *state.State, props ...*telemetry.Property) chan struct{} {
	props = append([]*telemetry.Property{
		{
			// Hash the repository URL for privacy
			Name:  "git_repository_hash",
			Value: hash(gitRepoURL(w.Path)),
		},
		{
			// The workspace path might contain the username (e.g. /home/user/workspace), so we hash itfor privacy.
			Name:  "workspace_path_hash",
			Value: hash(w.Path),
		},
	}, props...)

	if env != nil {
		props = append([]*telemetry.Property{
			{
				Name:  "environment_name",
				Value: env.Name,
			},
		}, props...)
	}

	return TrackCommand(ctx, cmd, props...)
}

// hash returns the sha256 digest of the string
func hash(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
}

// gitRepoURL returns the git repository remote, if any.
func gitRepoURL(path string) string {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return ""
	}

	origin, err := repo.Remote("origin")
	if err != nil {
		return ""
	}

	if urls := origin.Config().URLs; len(urls) > 0 {
		return urls[0]
	}

	return ""
}

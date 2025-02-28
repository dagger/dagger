package main

import (
	"context"
	"dagger/agents/internal/dagger"
)

type Agents struct{}

func (m *Agents) Migrate(
	ctx context.Context,
	// Source repository to migrate
	// +optional
	// +default="https://github.com/dagger/agents"
	sourceRepo string,
	// Repository to push the migrated repo to
	// +optional
	// +default="ssh://git@github.com/shykes/dagger"
	targetRepo string,
	// Name of the branch to push the migrated repo to
	// +optional
	// +default="agents-migrated"
	targetBranch string,
	// Git user name
	gitName string,
	// Git user email
	gitEmail string,
	// SSH key for git authentication
	sshKey *dagger.Secret,
) error {
	git := dag.Supergit(dagger.SupergitOpts{SSHKey: sshKey})
	_, err := git.
		Clone(sourceRepo).
		FilterToSubdirectory("agents").
		Command([]string{"push", "-f", targetRepo, "main:stage-mgiration"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	_, err = git.
		Clone("https://github.com/dagger/dagger").
		WithRemote("old", sourceRepo).
		WithCommand([]string{"fetch", "old", "--tags"}).
		WithConfig("user.email", gitEmail).
		WithConfig("user.name", gitName).
		WithCommand([]string{"merge", "--allow-unrelated-histories", "old/to-subdir"}).
		Command([]string{"push", targetRepo, "main:" + targetBranch}).
		Stdout(ctx)
	return err
}

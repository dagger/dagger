package main

import "dagger/gh/internal/dagger"

// Work with GitHub repositories.
func (m *Gh) Repo() *Repo {
	return &Repo{Gh: m}
}

type Repo struct {
	// +private
	Gh *Gh
}

// Clone a GitHub repository locally.
func (m *Repo) Clone(
	repository string,

	// Additional arguments to pass to the "git clone" command.
	//
	// +optional
	args []string,

	// GitHub token.
	//
	// +optional
	token *dagger.Secret,
) *dagger.Directory {
	cmdArgs := []string{"gh", "repo", "clone", repository, "/tmp/repo"}

	if len(args) > 0 {
		cmdArgs = append(cmdArgs, "--")
		cmdArgs = append(cmdArgs, args...)
	}

	return m.Gh.container(token, repository).WithExec(cmdArgs).Directory("/tmp/repo")
}

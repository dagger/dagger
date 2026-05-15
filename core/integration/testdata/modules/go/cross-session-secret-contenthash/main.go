package main

import "dagger/secreter/internal/dagger"

type Secreter struct{}

func (*Secreter) Fn(cacheBust string, tokenPlaintext string) *dagger.Container {
	authSecret := dag.SetSecret("GIT_AUTH", tokenPlaintext)
	gitRepo := dag.Git("https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private", dagger.GitOpts{HTTPAuthToken: authSecret}).
		Branch("main").
		Tree()

	return dag.Container().From("alpine:3.20").
		WithEnvVariable("CACHEBUST", cacheBust).
		WithMountedDirectory("/src", gitRepo).
		WithExec([]string{"true"})
}

package main

import "typescript-sdk/internal/dagger"

const (
	gitCredentialSockPath = "/tmp/dagger-git-credential.sock"
	// where the engine bind-mounts its credential helper into execs that
	// mount a git-credential socket
	gitCredentialHelperPath = "/.git-credential"
)

// ensureGitCmd installs git if missing: package managers shell out to the
// git CLI for git dependencies, but neither node's alpine image nor bun's
// debian image ships it.
const ensureGitCmd = "command -v git >/dev/null || apk add --no-cache git || { apt-get update -qq && apt-get install -qqy --no-install-recommends git; }"

// Mount the engine-provided git-credential socket and point git at its
// helper, so package managers shelling out to git can authenticate against
// private hosts. Must be paired with withoutGitCredentials before user code
// runs. Known limitation: any GIT_CONFIG_* env a base image sets is masked
// during the install and dropped by the scrub (file-based git config is
// untouched).
func withGitCredentials(ctr *dagger.Container, sock *dagger.Socket) *dagger.Container {
	if sock == nil {
		return ctr
	}
	return ctr.
		WithExec([]string{"sh", "-c", ensureGitCmd}).
		WithUnixSocket(gitCredentialSockPath, sock).
		WithEnvVariable("GIT_CONFIG_COUNT", "1").
		WithEnvVariable("GIT_CONFIG_KEY_0", "credential.helper").
		WithEnvVariable("GIT_CONFIG_VALUE_0", gitCredentialHelperPath+" "+gitCredentialSockPath)
}

func withoutGitCredentials(ctr *dagger.Container, sock *dagger.Socket) *dagger.Container {
	if sock == nil {
		return ctr
	}
	return ctr.
		WithoutUnixSocket(gitCredentialSockPath).
		WithoutEnvVariable("GIT_CONFIG_COUNT").
		WithoutEnvVariable("GIT_CONFIG_KEY_0").
		WithoutEnvVariable("GIT_CONFIG_VALUE_0")
}

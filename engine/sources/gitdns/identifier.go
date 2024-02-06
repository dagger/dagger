package gitdns

import (
	bkgit "github.com/moby/buildkit/source/git"
)

const AttrGitClientIDs = "dagger.git.clientids"

type GitIdentifier struct {
	bkgit.GitIdentifier

	ClientIDs []string
}

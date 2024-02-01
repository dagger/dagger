package gitdns

import (
	bkgit "github.com/moby/buildkit/source/git"
)

type GitIdentifier struct {
	bkgit.GitIdentifier

	ClientIDs []string
}

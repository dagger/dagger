package gitdns

import (
	bkgit "github.com/moby/buildkit/source/git"
)

const AttrDNSNamespace = "dagger.dns.namespace"

type GitIdentifier struct {
	bkgit.GitIdentifier

	Namespace string
}

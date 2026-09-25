package schema

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/utils/ioutil"
)

// Use the same transport as go-git's ListContext, but retain the advertisement
// before AllReferences guesses symbolic HEAD on servers without that capability.
// Git's ls-remote --symref reports only symbolic refs actually advertised.
func publicRemoteAdvertisement(ctx context.Context, remote *gitutil.GitURL) (_ *gitutil.Remote, rerr error) {
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "git.publicAdvertisement", wcprof.OpOpts{})
	defer func() { op.EndErr(rerr) }()
	endpoint, err := transport.NewEndpoint(remote.Remote())
	if err != nil {
		return nil, err
	}
	transportClient, err := client.NewClient(endpoint)
	if err != nil {
		return nil, err
	}
	session, err := transportClient.NewUploadPackSession(endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer ioutil.CheckClose(session, &rerr)
	advertised, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return nil, err
	}
	// Preserve ListContext's validation and visibility error classification.
	if _, err := advertised.AllReferences(); err != nil {
		return nil, err
	}
	return remoteFromAdvertisement(advertised), nil
}

func remoteFromAdvertisement(advertised *packp.AdvRefs) *gitutil.Remote {
	remote := &gitutil.Remote{
		Refs:    make([]*gitutil.Ref, 0, 1+len(advertised.References)+len(advertised.Peeled)),
		Symrefs: make(map[string]string),
	}
	if advertised.Head != nil {
		remote.Refs = append(remote.Refs, &gitutil.Ref{Name: "HEAD", SHA: advertised.Head.String()})
	}
	for name, hash := range advertised.References {
		remote.Refs = append(remote.Refs, &gitutil.Ref{Name: name, SHA: hash.String()})
	}
	for name, hash := range advertised.Peeled {
		remote.Refs = append(remote.Refs, &gitutil.Ref{Name: name + "^{}", SHA: hash.String()})
	}
	for _, symref := range advertised.Capabilities.Get(capability.SymRef) {
		if name, target, ok := strings.Cut(symref, ":"); ok {
			remote.Symrefs[name] = target
		}
	}
	// Git advertises refs in name order; map iteration must not change the
	// ordered refs (or their digest) between calls.
	slices.SortFunc(remote.Refs, func(a, b *gitutil.Ref) int { return cmp.Compare(a.Name, b.Name) })
	return remote
}

package core

import (
	"context"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type GitRef struct {
	URL string `json:"url"`
	Ref string `json:"ref"`

	KeepGitDir bool `json:"keepGitDir"`

	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Pipeline pipeline.Path   `json:"pipeline"`
	Platform specs.Platform  `json:"platform,omitempty"`
}

func (ref *GitRef) clone() *GitRef {
	r := *ref
	r.Services = cloneSlice(r.Services)
	r.Pipeline = cloneSlice(r.Pipeline)
	return &r
}

func (ref *GitRef) WithRef(name string) *GitRef {
	ref = ref.clone()
	ref.Ref = name
	return ref
}

func (ref *GitRef) Tree(ctx context.Context, bk *buildkit.Client) (*Directory, error) {
	st := ref.getState(ctx, bk)
	return NewDirectorySt(ctx, *st, "", ref.Pipeline, ref.Platform, ref.Services)
}

func (ref *GitRef) Commit(ctx context.Context, bk *buildkit.Client) (string, error) {
	st := ref.getState(ctx, bk)
	p, err := resolveProvenance(ctx, bk, *st)
	if err != nil {
		return "", err
	}
	if len(p.Sources.Git) == 0 {
		return "", errors.Errorf("no git commit was resolved")
	}
	return p.Sources.Git[0].Commit, nil
}

func (ref *GitRef) getState(ctx context.Context, bk *buildkit.Client) *llb.State {
	opts := []llb.GitOption{}

	if ref.KeepGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if ref.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(ref.SSHKnownHosts))
	}
	if ref.SSHAuthSocket != "" {
		opts = append(opts, llb.MountSSHSock(string(ref.SSHAuthSocket)))
	}

	useDNS := len(ref.Services) > 0

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err == nil && !useDNS {
		useDNS = len(clientMetadata.ParentClientIDs) > 0
	}

	var st llb.State
	if useDNS {
		// NB: only configure search domains if we're directly using a service, or
		// if we're nested beneath another search domain.
		//
		// we have to be a bit selective here to avoid breaking Dockerfile builds
		// that use a Buildkit frontend (# syntax = ...) that doesn't have the
		// networks API cap.
		//
		// TODO: add API cap
		st = gitdns.State(ref.URL, ref.Ref, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.Git(ref.URL, ref.Ref, opts...)
	}
	return &st
}

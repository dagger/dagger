package core

import (
	"context"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type GitRepository struct {
	URL string `json:"url"`

	KeepGitDir bool `json:"keepGitDir"`

	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Pipeline pipeline.Path   `json:"pipeline"`
	Platform specs.Platform  `json:"platform,omitempty"`
}

func (repo *GitRepository) ID() (GitRepositoryID, error) {
	return resourceid.Encode(repo)
}

type GitRef struct {
	Ref  string         `json:"ref"`
	Repo *GitRepository `json:"repository"`
}

func (ref *GitRef) ID() (GitRefID, error) {
	return resourceid.Encode(ref)
}

func (ref *GitRef) Tree(ctx context.Context, bk *buildkit.Client) (*Directory, error) {
	st := ref.getState(ctx, bk)
	return NewDirectorySt(ctx, *st, "", ref.Repo.Pipeline, ref.Repo.Platform, ref.Repo.Services)
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

	if ref.Repo.KeepGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if ref.Repo.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(ref.Repo.SSHKnownHosts))
	}
	if ref.Repo.SSHAuthSocket != "" {
		opts = append(opts, llb.MountSSHSock(string(ref.Repo.SSHAuthSocket)))
	}

	useDNS := len(ref.Repo.Services) > 0

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
		st = gitdns.State(ref.Repo.URL, ref.Ref, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.Git(ref.Repo.URL, ref.Ref, opts...)
	}
	return &st
}

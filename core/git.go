package core

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/gitdns"
)

type GitRepository struct {
	Query *Query

	URL string `json:"url"`

	KeepGitDir bool `json:"keepGitDir"`

	SSHKnownHosts string  `json:"sshKnownHosts"`
	SSHAuthSocket *Socket `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Platform Platform        `json:"platform,omitempty"`
}

func (*GitRepository) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRepository",
		NonNull:   true,
	}
}

func (*GitRepository) TypeDescription() string {
	return "A git repository."
}

type GitRef struct {
	Query *Query

	Ref  string         `json:"ref"`
	Repo *GitRepository `json:"repository"`
}

func (*GitRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRef",
		NonNull:   true,
	}
}

func (*GitRef) TypeDescription() string {
	return "A git ref (tag, branch, or commit)."
}

func (ref *GitRef) Tree(ctx context.Context) (*Directory, error) {
	bk := ref.Query.Buildkit
	st := ref.getState(ctx, bk)
	return NewDirectorySt(ctx, ref.Query, *st, "", ref.Repo.Platform, ref.Repo.Services)
}

func (ref *GitRef) Commit(ctx context.Context) (string, error) {
	bk := ref.Query.Buildkit
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
	if ref.Repo.SSHAuthSocket != nil {
		opts = append(opts, llb.MountSSHSock(ref.Repo.SSHAuthSocket.SSHID()))
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
		st = gitdns.Git(ref.Repo.URL, ref.Ref, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.Git(ref.Repo.URL, ref.Ref, opts...)
	}
	return &st
}

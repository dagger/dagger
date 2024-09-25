package core

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/gitdns"
)

type GitRepository struct {
	Query *Query

	URL string `json:"url"`

	DiscardGitDir bool `json:"discardGitDir"`

	SSHKnownHosts string  `json:"sshKnownHosts"`
	SSHAuthSocket *Socket `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Platform Platform        `json:"platform,omitempty"`

	AuthToken  *Secret `json:"authToken"`
	AuthHeader *Secret `json:"authHeader"`
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

func (ref *GitRef) Tree(ctx context.Context, discardGitDir bool) (*Directory, error) {
	st, err := ref.getState(ctx, ref.Repo.DiscardGitDir || discardGitDir)
	if err != nil {
		return nil, err
	}
	return NewDirectorySt(ctx, ref.Query, st, "", ref.Repo.Platform, ref.Repo.Services)
}

func (ref *GitRef) Commit(ctx context.Context) (string, error) {
	bk, err := ref.Query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}
	st, err := ref.getState(ctx, true)
	if err != nil {
		return "", err
	}
	p, err := resolveProvenance(ctx, bk, st)
	if err != nil {
		return "", err
	}
	if len(p.Sources.Git) == 0 {
		return "", errors.Errorf("no git commit was resolved")
	}
	return p.Sources.Git[0].Commit, nil
}

func (ref *GitRef) getState(ctx context.Context, discardGitDir bool) (llb.State, error) {
	opts := []llb.GitOption{}

	if !discardGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if ref.Repo.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(ref.Repo.SSHKnownHosts))
	}
	if ref.Repo.SSHAuthSocket != nil {
		opts = append(opts, llb.MountSSHSock(ref.Repo.SSHAuthSocket.LLBID()))
	}
	if ref.Repo.AuthToken != nil {
		opts = append(opts, llb.AuthTokenSecret(ref.Repo.AuthToken.LLBID()))
	}
	if ref.Repo.AuthHeader != nil {
		opts = append(opts, llb.AuthHeaderSecret(ref.Repo.AuthHeader.LLBID()))
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return llb.State{}, err
	}

	return gitdns.Git(ref.Repo.URL, ref.Ref, clientMetadata.SessionID, opts...), nil
}

package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
)

var _ router.ExecutableSchema = &gitSchema{}

type gitSchema struct {
	*baseSchema
}

func (s *gitSchema) Name() string {
	return "git"
}

func (s *gitSchema) Schema() string {
	return Git
}

func (s *gitSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"git": router.ToResolver(s.git),
		},
		"GitRepository": router.ObjectResolver{
			"branches": router.ToResolver(s.branches),
			"branch":   router.ToResolver(s.branch),
			"tags":     router.ToResolver(s.tags),
			"tag":      router.ToResolver(s.tag),
		},
		"GitRef": router.ObjectResolver{
			"digest": router.ToResolver(s.digest),
			"tree":   router.ToResolver(s.tree),
		},
	}
}

func (s *gitSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type gitRepository struct {
	URL string `json:"url"`
}

type gitRef struct {
	Repository gitRepository
	Name       string
}

type gitArgs struct {
	URL string `json:"url"`
}

func (s *gitSchema) git(ctx *router.Context, parent any, args gitArgs) (gitRepository, error) {
	return gitRepository(args), nil
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx *router.Context, parent gitRepository, args branchArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.Name,
	}, nil
}

func (s *gitSchema) branches(ctx *router.Context, parent any, args any) (any, error) {
	return nil, ErrNotImplementedYet
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx *router.Context, parent gitRepository, args tagArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.Name,
	}, nil
}

func (s *gitSchema) tags(ctx *router.Context, parent any, args any) (any, error) {
	return nil, ErrNotImplementedYet
}

func (s *gitSchema) digest(ctx *router.Context, parent any, args any) (any, error) {
	return nil, ErrNotImplementedYet
}

func (s *gitSchema) tree(ctx *router.Context, parent gitRef, args any) (*core.Directory, error) {
	var opts []llb.GitOption
	if s.sshAuthSockID != "" {
		opts = append(opts, llb.MountSSHSock(s.sshAuthSockID))
	}
	st := llb.Git(parent.Repository.URL, parent.Name, opts...)
	return core.NewDirectory(ctx, st, "", s.platform)
}

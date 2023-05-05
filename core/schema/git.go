package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
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
			"commit":   router.ToResolver(s.commit),
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
	URL         string            `json:"url"`
	KeepGitDir  bool              `json:"keepGitDir"`
	Pipeline    pipeline.Path     `json:"pipeline"`
	ServiceHost *core.ContainerID `json:"serviceHost,omitempty"`
}

type gitRef struct {
	Repository gitRepository
	Name       string
}

type gitArgs struct {
	URL                     string            `json:"url"`
	KeepGitDir              bool              `json:"keepGitDir"`
	ExperimentalServiceHost *core.ContainerID `json:"experimentalServiceHost"`
}

func (s *gitSchema) git(ctx *router.Context, parent *core.Query, args gitArgs) (gitRepository, error) {
	return gitRepository{
		URL:         args.URL,
		KeepGitDir:  args.KeepGitDir,
		ServiceHost: args.ExperimentalServiceHost,
		Pipeline:    parent.PipelinePath(),
	}, nil
}

type branchArgs struct {
	Name string
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx *router.Context, parent gitRepository, args commitArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.ID,
	}, nil
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

type gitTreeArgs struct {
	SSHKnownHosts string        `json:"sshKnownHosts"`
	SSHAuthSocket core.SocketID `json:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx *router.Context, parent gitRef, args gitTreeArgs) (*core.Directory, error) {
	opts := []llb.GitOption{
		parent.Repository.Pipeline.LLBOpt(),
	}

	if parent.Repository.KeepGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if args.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(args.SSHKnownHosts))
	}
	if args.SSHAuthSocket != "" {
		opts = append(opts, llb.MountSSHSock(args.SSHAuthSocket.LLBID()))
	}
	var svcs core.ServiceBindings
	if parent.Repository.ServiceHost != nil {
		svcs = core.ServiceBindings{*parent.Repository.ServiceHost: nil}
	}
	st := llb.Git(parent.Repository.URL, parent.Name, opts...)
	return core.NewDirectory(ctx, st, "", parent.Repository.Pipeline, s.platform, svcs)
}

package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/socket"
)

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct {
	*APIServer

	svcs *core.Services
}

func (s *gitSchema) Name() string {
	return "git"
}

func (s *gitSchema) Schema() string {
	return Git
}

func (s *gitSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"git": ToResolver(s.git),
		},
	}

	ResolveIDable[core.GitRepository](rs, "GitRepository", ObjectResolver{
		"branch": ToResolver(s.branch),
		"tag":    ToResolver(s.tag),
		"commit": ToResolver(s.commit),
	})
	ResolveIDable[core.GitRef](rs, "GitRef", ObjectResolver{
		"tree":   ToResolver(s.tree),
		"commit": ToResolver(s.fetchCommit),
	})

	return rs
}

type gitArgs struct {
	URL                     string          `json:"url"`
	KeepGitDir              bool            `json:"keepGitDir"`
	ExperimentalServiceHost *core.ServiceID `json:"experimentalServiceHost"`

	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`
}

func (s *gitSchema) git(ctx context.Context, parent *core.Query, args gitArgs) (*core.GitRepository, error) {
	var svcs core.ServiceBindings
	if args.ExperimentalServiceHost != nil {
		svc, err := args.ExperimentalServiceHost.Decode()
		if err != nil {
			return nil, nil
		}

		host, err := svc.Hostname(ctx, s.svcs)
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		})
	}

	repo := &core.GitRepository{
		URL:           args.URL,
		KeepGitDir:    args.KeepGitDir,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: args.SSHAuthSocket,
		Services:      svcs,
		Pipeline:      parent.PipelinePath(),
		Platform:      s.APIServer.platform,
	}
	return repo, nil
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx context.Context, parent *core.GitRepository, args commitArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Ref:  args.ID,
		Repo: parent,
	}, nil
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx context.Context, parent *core.GitRepository, args branchArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Ref:  args.Name,
		Repo: parent,
	}, nil
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx context.Context, parent *core.GitRepository, args tagArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Ref:  args.Name,
		Repo: parent,
	}, nil
}

type treeArgs struct {
	// SSHKnownHosts is deprecated
	SSHKnownHosts string `json:"sshKnownHosts"`
	// SSHAuthSocket is deprecated
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx context.Context, parent *core.GitRef, treeArgs treeArgs) (*core.Directory, error) {
	res := *parent
	repo := *res.Repo
	res.Repo = &repo
	if treeArgs.SSHKnownHosts != "" || treeArgs.SSHAuthSocket != "" {
		// no need for a full clone() here, we're only modifying string fields
		res.Repo.SSHKnownHosts = treeArgs.SSHKnownHosts
		res.Repo.SSHAuthSocket = treeArgs.SSHAuthSocket
	}
	return res.Tree(ctx, s.bk)
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent *core.GitRef, _ any) (string, error) {
	return parent.Commit(ctx, s.bk)
}

package schema

import (
	"context"

	"github.com/dagger/dagger/core"
)

var _ ExecutableSchema = &gitSchema{}

type gitSchema struct {
	*MergedSchemas

	svcs *core.Services
}

func (s *gitSchema) Name() string {
	return "git"
}

func (s *gitSchema) SourceModuleName() string {
	return coreModuleName
}

func (s *gitSchema) Schema() string {
	return Git
}

func (s *gitSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"git": ToCachedResolver(s.queryCache, s.git),
		},
		"GitRepository": ObjectResolver{
			"branch": ToCachedResolver(s.queryCache, s.branch),
			"tag":    ToCachedResolver(s.queryCache, s.tag),
			"commit": ToCachedResolver(s.queryCache, s.commit),
		},
		"GitRef": ObjectResolver{
			"tree":   ToCachedResolver(s.queryCache, s.tree),
			"commit": ToCachedResolver(s.queryCache, s.fetchCommit),
		},
	}
}

type gitArgs struct {
	URL                     string         `json:"url"`
	KeepGitDir              bool           `json:"keepGitDir"`
	ExperimentalServiceHost core.ServiceID `json:"experimentalServiceHost"`

	SSHKnownHosts string        `json:"sshKnownHosts"`
	SSHAuthSocket core.SocketID `json:"sshAuthSocket"`
}

func (s *gitSchema) git(ctx context.Context, parent *core.Query, args gitArgs) (*core.GitRef, error) {
	var svcs core.ServiceBindings
	if args.ExperimentalServiceHost != nil {
		svc, err := load(ctx, args.ExperimentalServiceHost, s.MergedSchemas)
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

	socket, err := load(ctx, args.SSHAuthSocket, s.MergedSchemas)
	if err != nil {
		return nil, err
	}

	repo := &core.GitRef{
		URL:           args.URL,
		KeepGitDir:    args.KeepGitDir,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: socket.SocketID(),
		Services:      svcs,
		Pipeline:      parent.PipelinePath(),
		Platform:      s.MergedSchemas.platform,
	}
	return repo, nil
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx context.Context, parent *core.GitRef, args commitArgs) (*core.GitRef, error) {
	return parent.WithRef(args.ID), nil
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx context.Context, parent *core.GitRef, args branchArgs) (*core.GitRef, error) {
	return parent.WithRef(args.Name), nil
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx context.Context, parent *core.GitRef, args tagArgs) (*core.GitRef, error) {
	return parent.WithRef(args.Name), nil
}

type treeArgs struct {
	// SSHKnownHosts is deprecated
	SSHKnownHosts string `json:"sshKnownHosts"`
	// SSHAuthSocket is deprecated
	SSHAuthSocket core.SocketID `json:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx context.Context, parent *core.GitRef, treeArgs treeArgs) (*core.Directory, error) {
	res := *parent
	if treeArgs.SSHKnownHosts != "" || treeArgs.SSHAuthSocket != nil {
		// no need for a full clone() here, we're only modifying string fields
		res.SSHKnownHosts = treeArgs.SSHKnownHosts
		socket, err := load(ctx, treeArgs.SSHAuthSocket, s.MergedSchemas)
		if err != nil {
			return nil, err
		}
		res.SSHAuthSocket = socket.SocketID()
	}
	return res.Tree(ctx, s.bk)
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent *core.GitRef, _ any) (string, error) {
	return parent.Commit(ctx, s.bk)
}

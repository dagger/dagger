package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/socket"
)

var _ ExecutableSchema = &gitSchema{}

type gitSchema struct {
	*MergedSchemas

	svcs *core.Services
}

func (s *gitSchema) Name() string {
	return "git"
}

func (s *gitSchema) Schema() string {
	return Git
}

func (s *gitSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"git": ToResolver(s.git),
		},
		"GitRepository": ObjectResolver{
			"branch": ToResolver(s.branch),
			"tag":    ToResolver(s.tag),
			"commit": ToResolver(s.commit),
		},
		"GitRef": ObjectResolver{
			"tree":   ToResolver(s.tree),
			"commit": ToResolver(s.fetchCommit),
		},
	}
}

func (s *gitSchema) Dependencies() []ExecutableSchema {
	return nil
}

type gitArgs struct {
	URL                     string          `json:"url"`
	KeepGitDir              bool            `json:"keepGitDir"`
	ExperimentalServiceHost *core.ServiceID `json:"experimentalServiceHost"`

	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`
}

func (s *gitSchema) git(ctx context.Context, parent *core.Query, args gitArgs) (*core.GitRef, error) {
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

	repo := &core.GitRef{
		URL:           args.URL,
		KeepGitDir:    args.KeepGitDir,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: args.SSHAuthSocket,
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
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx context.Context, parent *core.GitRef, treeArgs treeArgs) (*core.Directory, error) {
	res := *parent
	if treeArgs.SSHKnownHosts != "" || treeArgs.SSHAuthSocket != "" {
		// no need for a full clone() here, we're only modifying string fields
		res.SSHKnownHosts = treeArgs.SSHKnownHosts
		res.SSHAuthSocket = treeArgs.SSHAuthSocket
	}
	return res.Tree(ctx, s.bk)
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent *core.GitRef, _ any) (string, error) {
	return parent.Commit(ctx, s.bk)
}

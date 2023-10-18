package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	"github.com/pkg/errors"
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
			"commit": ToResolver(s.findCommit),
		},
	}
}

func (s *gitSchema) Dependencies() []ExecutableSchema {
	return nil
}

type gitRepository struct {
	URL             string         `json:"url"`
	KeepGitDir      bool           `json:"keepGitDir"`
	AuthTokenSecret *core.SecretID `json:"authTokenSecret,omitempty"`
	Pipeline        pipeline.Path  `json:"pipeline"`
	ServiceHost     *core.Service  `json:"serviceHost,omitempty"`
}

type gitRef struct {
	Repository gitRepository
	Name       string
}

type gitArgs struct {
	URL                     string          `json:"url"`
	KeepGitDir              bool            `json:"keepGitDir"`
	ExperimentalServiceHost *core.ServiceID `json:"experimentalServiceHost"`
}

func (s *gitSchema) git(ctx *core.Context, parent *core.Query, args gitArgs) (gitRepository, error) {
	repo := gitRepository{
		URL:        args.URL,
		KeepGitDir: args.KeepGitDir,
		Pipeline:   parent.PipelinePath(),
	}
	if args.ExperimentalServiceHost != nil {
		svc, err := args.ExperimentalServiceHost.Decode()
		if err != nil {
			return gitRepository{}, nil
		}
		repo.ServiceHost = svc
	}
	return repo, nil
}

type branchArgs struct {
	Name string
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx *core.Context, parent gitRepository, args commitArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.ID,
	}, nil
}

func (s *gitSchema) branch(ctx *core.Context, parent gitRepository, args branchArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.Name,
	}, nil
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx *core.Context, parent gitRepository, args tagArgs) (gitRef, error) {
	return gitRef{
		Repository: parent,
		Name:       args.Name,
	}, nil
}

type gitTreeArgs struct {
	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx *core.Context, parent gitRef, args gitTreeArgs) (*core.Directory, error) {
	var svcs core.ServiceBindings
	if parent.Repository.ServiceHost != nil {
		host, err := parent.Repository.ServiceHost.Hostname(ctx, s.svcs)
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, core.ServiceBinding{
			Service:  parent.Repository.ServiceHost,
			Hostname: host,
		})
	}

	st, err := s.getState(ctx, parent, args, svcs)
	if err != nil {
		return nil, err
	}
	return core.NewDirectorySt(ctx, *st, "", parent.Repository.Pipeline, s.platform, svcs)
}

func (s *gitSchema) findCommit(ctx *core.Context, parent gitRef, args gitTreeArgs) (string, error) {
	var svcs core.ServiceBindings
	if parent.Repository.ServiceHost != nil {
		host, err := parent.Repository.ServiceHost.Hostname(ctx, s.svcs)
		if err != nil {
			return "", err
		}
		svcs = append(svcs, core.ServiceBinding{
			Service:  parent.Repository.ServiceHost,
			Hostname: host,
		})
	}

	st, err := s.getState(ctx, parent, args, svcs)
	if err != nil {
		return "", err
	}
	p, err := s.getProvenance(ctx, *st)
	if err != nil {
		return "", err
	}
	if len(p.Sources.Git) == 0 {
		return "", errors.Errorf("no git commit was resolved")
	}
	return p.Sources.Git[0].Commit, nil
}

func (s *gitSchema) getProvenance(ctx *core.Context, st llb.State) (*provenance.Capture, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}
	pr := res.Ref.Provenance()
	if pr == nil {
		return nil, errors.Errorf("no provenance was resolved")
	}
	p, ok := pr.(*provenance.Capture)
	if !ok {
		return nil, errors.Errorf("invalid provenance type %T", pr)
	}
	return p, nil
}

func (s *gitSchema) getState(ctx *core.Context, parent gitRef, args gitTreeArgs, svcs core.ServiceBindings) (*llb.State, error) {
	opts := []llb.GitOption{}

	if parent.Repository.KeepGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if args.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(args.SSHKnownHosts))
	}
	if args.SSHAuthSocket != "" {
		opts = append(opts, llb.MountSSHSock(string(args.SSHAuthSocket)))
	}

	useDNS := len(svcs) > 0

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
		st = gitdns.State(parent.Repository.URL, parent.Name, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.Git(parent.Repository.URL, parent.Name, opts...)
	}
	return &st, nil
}

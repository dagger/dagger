package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type gitPushArgs struct {
	To                dagql.Optional[dagql.ID[*core.GitRepository]]
	Branch            string `default:""`
	ExpectedRemoteSHA string `name:"expectedRemoteSHA" default:""`
}

func (s *gitSchema) push(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitPushArgs) (dagql.ObjectResult[*core.GitPushResult], error) {
	var inst dagql.ObjectResult[*core.GitPushResult]
	ctx, cancel := context.WithTimeout(ctx, core.GitPushTimeout)
	defer cancel()
	opts := core.GitPushOpts{Branch: args.Branch, ExpectedRemoteSHA: args.ExpectedRemoteSHA}
	if _, err := opts.Ref(parent.Self().Ref); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	repo := parent.Self().Repo
	if args.To.Valid {
		repo, err = args.To.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
	} else if _, remote := repo.Self().Backend.(*core.RemoteGitRepository); !remote {
		if !repo.Self().URL.Valid || repo.Self().URL.Value.String() == "" {
			return inst, fmt.Errorf("push requires an explicit destination repository: source has no remote URL")
		}
		if err := srv.Select(ctx, srv.Root(), &repo, dagql.Selector{Field: "git", Args: []dagql.NamedInput{{Name: "url", Value: repo.Self().URL.Value}}}); err != nil {
			return inst, err
		}
	}
	remote, ok := repo.Self().Backend.(*core.RemoteGitRepository)
	if !ok {
		return inst, fmt.Errorf("push destination must be a remote Git repository, not a Directory; use Workspace.export for a local checkout")
	}
	result, err := parent.Self().Push(ctx, remote, opts)
	if err != nil {
		return inst, err
	}
	// Return a pure receipt composition, never the effectful push call. A
	// receipt ID can be loaded in another session without touching the remote.
	err = srv.Select(ctx, srv.Root(), &inst, dagql.Selector{Field: "__gitPushResult", Args: []dagql.NamedInput{
		{Name: "ref", Value: dagql.NewString(result.Ref)}, {Name: "previousSHA", Value: dagql.NewString(result.PreviousSHA)},
		{Name: "sha", Value: dagql.NewString(result.SHA)}, {Name: "disposition", Value: result.Disposition},
	}})
	return inst, err
}

func (s *gitSchema) pushResult(_ context.Context, _ *core.Query, args struct {
	Ref         string
	PreviousSHA string `name:"previousSHA"`
	SHA         string
	Disposition core.GitPushDisposition
}) (*core.GitPushResult, error) {
	return &core.GitPushResult{Ref: args.Ref, PreviousSHA: args.PreviousSHA, SHA: args.SHA, Disposition: args.Disposition}, nil
}

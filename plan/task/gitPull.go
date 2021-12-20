package task

import (
	"context"
	"net/url"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("GitPull", func() Task { return &gitPullTask{} })
}

type gitPullTask struct {
}

func (c gitPullTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	remote, err := v.Lookup("remote").String()
	if err != nil {
		return nil, err
	}
	ref, err := v.Lookup("ref").String()
	if err != nil {
		return nil, err
	}

	remoteRedacted := remote
	if u, err := url.Parse(remote); err == nil {
		remoteRedacted = u.Redacted()
	}

	gitOpts := []llb.GitOption{}
	var opts struct {
		KeepGitDir bool
	}

	if err := v.Decode(&opts); err != nil {
		return nil, err
	}

	if opts.KeepGitDir {
		gitOpts = append(gitOpts, llb.KeepGitDir())
	}

	// Secret
	if authToken := v.Lookup("authToken"); authToken.Exists() {
		authTokenSecret, err := pctx.Secrets.FromValue(authToken)
		if err != nil {
			return nil, err
		}
		gitOpts = append(gitOpts, llb.AuthTokenSecret(authTokenSecret.ID()))
	}

	if authHeader := v.Lookup("authHeader"); authHeader.Exists() {
		authHeaderSecret, err := pctx.Secrets.FromValue(authHeader)
		if err != nil {
			return nil, err
		}
		gitOpts = append(gitOpts, llb.AuthHeaderSecret(authHeaderSecret.ID()))
	}

	gitOpts = append(gitOpts, withCustomName(v, "FetchGit %s@%s", remoteRedacted, ref))

	st := llb.Git(remote, ref, gitOpts...)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}

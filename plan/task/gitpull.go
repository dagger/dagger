package task

import (
	"context"
	"net/url"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("GitPull", func() Task { return &gitPullTask{} })
}

type gitPullTask struct {
}

func (c *gitPullTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var gitPull struct {
		Remote     string
		Ref        string
		KeepGitDir bool
		Auth       struct {
			Username string
		}
	}

	if err := v.Decode(&gitPull); err != nil {
		return nil, err
	}

	gitOpts := []llb.GitOption{}

	lg := log.Ctx(ctx)

	if gitPull.KeepGitDir {
		lg.Debug().Str("keepGitDir", "true").Msg("adding git option")
		gitOpts = append(gitOpts, llb.KeepGitDir())
	}

	if gitPull.Auth.Username != "" {
		pwd := v.Lookup("auth.password")

		pwdSecret, err := pctx.Secrets.FromValue(pwd)
		if err != nil {
			return nil, err
		}

		remote, err := url.Parse(gitPull.Remote)
		if err != nil {
			return nil, err
		}

		lg.Debug().Str("username", gitPull.Auth.Username).Str("password", "***").Msg("using username:password auth")
		remote.User = url.UserPassword(gitPull.Auth.Username, strings.TrimSpace(pwdSecret.PlainText()))
		gitPull.Remote = remote.String()
	} else if authToken := v.Lookup("auth.authToken"); plancontext.IsSecretValue(authToken) {
		authTokenSecret, err := pctx.Secrets.FromValue(authToken)
		if err != nil {
			return nil, err
		}
		lg.Debug().Str("authToken", "***").Msg("adding git option")
		gitOpts = append(gitOpts, llb.AuthTokenSecret(authTokenSecret.ID()))
	} else if authHeader := v.Lookup("auth.authHeader"); plancontext.IsSecretValue(authHeader) {
		authHeaderSecret, err := pctx.Secrets.FromValue(authHeader)
		if err != nil {
			return nil, err
		}
		lg.Debug().Str("authHeader", "***").Msg("adding git option")
		gitOpts = append(gitOpts, llb.AuthHeaderSecret(authHeaderSecret.ID()))
	} else if authSocket := v.Lookup("auth.sshAgent"); plancontext.IsSocketValue(authSocket) {
		s, err := pctx.Sockets.FromValue(authSocket)
		if err != nil {
			return nil, err
		}

		lg.Debug().Str("sshAgent", "***").Msg("adding git option")
		gitOpts = append(gitOpts, llb.MountSSHSock(s.ID()))
	}

	remoteRedacted := gitPull.Remote
	if u, err := url.Parse(gitPull.Remote); err == nil {
		remoteRedacted = u.Redacted()
	}

	gitOpts = append(gitOpts, withCustomName(v, "GitPull %s@%s", remoteRedacted, gitPull.Ref))

	st := llb.Git(gitPull.Remote, gitPull.Ref, gitOpts...)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}

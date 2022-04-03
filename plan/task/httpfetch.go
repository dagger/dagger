package task

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("HTTPFetch", func() Task { return &httpFetchTask{} })
}

type httpFetchTask struct{}

func (c *httpFetchTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var httpFetch struct {
		Source      string
		Checksum    string
		Dest        string
		Permissions int
		UID         int
		GID         int
	}

	if err := v.Decode(&httpFetch); err != nil {
		return nil, err
	}

	linkRedacted := httpFetch.Source
	if u, err := url.Parse(httpFetch.Source); err == nil {
		linkRedacted = u.Redacted()
	}

	httpOpts := []llb.HTTPOption{}
	lg := log.Ctx(ctx)
	if httpFetch.Checksum != "" {
		lg.Debug().Str("checksum", httpFetch.Checksum).Msg("adding http option")
		dgst, err := digest.Parse(httpFetch.Checksum)
		if err != nil {
			return nil, err
		}
		httpOpts = append(httpOpts, llb.Checksum(dgst))
	}
	if httpFetch.Dest != "" {
		lg.Debug().Str("dest", httpFetch.Dest).Msg("adding http option")
		httpOpts = append(httpOpts, llb.Filename(httpFetch.Dest))
	}
	if httpFetch.Permissions != 0 {
		lg.Debug().Str("permissions", fmt.Sprint(httpFetch.Permissions)).Msg("adding http option")
		httpOpts = append(httpOpts, llb.Chmod(fs.FileMode(httpFetch.Permissions)))
	}
	if httpFetch.UID != 0 && httpFetch.GID != 0 {
		lg.Debug().Str("uid", fmt.Sprint(httpFetch.UID)).Str("gid", fmt.Sprint(httpFetch.GID)).Msg("adding http option")
		httpOpts = append(httpOpts, llb.Chown(httpFetch.UID, httpFetch.GID))
	}

	httpOpts = append(httpOpts, withCustomName(v, "FetchHTTP %s", linkRedacted))

	st := llb.HTTP(httpFetch.Source, httpOpts...)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}

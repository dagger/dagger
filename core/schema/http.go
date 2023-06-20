package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
)

var _ router.ExecutableSchema = &httpSchema{}

type httpSchema struct {
	*baseSchema
}

func (s *httpSchema) Name() string {
	return "http"
}

func (s *httpSchema) Schema() string {
	return HTTP
}

func (s *httpSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"http": router.ToResolver(s.http),
		},
	}
}

func (s *httpSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type httpArgs struct {
	URL                     string          `json:"url"`
	ExperimentalServiceHost *core.ServiceID `json:"experimentalServiceHost"`
}

func (s *httpSchema) http(ctx *router.Context, parent *core.Query, args httpArgs) (*core.File, error) {
	pipeline := parent.PipelinePath()

	// Use a filename that is set to the URL. Buildkit internally stores some cache metadata of etags
	// and http checksums using an id based on this name, so setting it to the URL maximizes our chances
	// of following more optimized cache codepaths.
	// Do a hash encode to prevent conflicts with use of `/` in the URL while also not hitting max filename limits
	filename := digest.FromString(args.URL).Encoded()

	svcs := core.ServiceBindings{}
	if args.ExperimentalServiceHost != nil {
		svcs[*args.ExperimentalServiceHost] = nil
	}

	opts := []llb.HTTPOption{
		llb.Filename(filename),
	}

	if len(svcs) > 0 || len(s.extraSearchDomains) > 0 {
		// NB: only configure search domains if we're directly using a service, or
		// if we're nested beneath another search domain.
		//
		// we have to be a bit selective here to avoid breaking Dockerfile builds
		// that use a Buildkit frontend (# syntax = ...) that doesn't have the
		// networks API cap yet.
		opts = append(opts, llb.WithNetwork(core.DaggerNetwork))
	}

	st := llb.HTTP(args.URL, opts...)
	return core.NewFileSt(ctx, st, filename, pipeline, s.platform, svcs)
}

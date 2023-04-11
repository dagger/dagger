package schema

import (
	"encoding/base64"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
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
	URL                     string            `json:"url"`
	ExperimentalServiceHost *core.ContainerID `json:"experimentalServiceHost"`
}

func (s *httpSchema) http(ctx *router.Context, parent *core.Query, args httpArgs) (*core.File, error) {
	pipeline := parent.PipelinePath()

	// Use a filename that is set to the URL. Buildkit internally stores some cache metadata of etags
	// and http checksums using an id based on this name, so setting it to the URL maximizes our chances
	// of following more optimized cache codepaths.
	// Do a base64 encode to prevent conflicts with use of `/` in the URL.
	filename := base64.URLEncoding.EncodeToString([]byte(args.URL))
	st := llb.HTTP(args.URL, llb.Filename(filename), pipeline.LLBOpt())

	svcs := core.ServiceBindings{}
	if args.ExperimentalServiceHost != nil {
		svcs[*args.ExperimentalServiceHost] = nil
	}

	return core.NewFile(ctx, st, filename, pipeline, s.platform, svcs)
}

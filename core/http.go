package core

import (
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/router"
)

var _ router.ExecutableSchema = &httpSchema{}

type httpSchema struct {
	*baseSchema
}

func (s *httpSchema) Name() string {
	return "http"
}

func (s *httpSchema) Schema() string {
	return schema.HTTP
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
	URL string `json:"url"`
}

func (s *httpSchema) http(ctx *router.Context, _ any, args httpArgs) (*File, error) {
	st := llb.HTTP(args.URL, llb.Filename("contents"))
	f, err := NewFile(ctx, st, "contents", s.platform)
	if err != nil {
		return nil, err
	}
	return f, nil
}

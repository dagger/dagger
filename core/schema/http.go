package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/httpdns"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
)

var _ ExecutableSchema = &httpSchema{}

type httpSchema struct {
	*MergedSchemas

	svcs *core.Services
}

func (s *httpSchema) Name() string {
	return "http"
}

func (s *httpSchema) Schema() string {
	return HTTP
}

func (s *httpSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"http": ToResolver(s.http),
		},
	}
}

func (s *httpSchema) Dependencies() []ExecutableSchema {
	return nil
}

type httpArgs struct {
	URL                     string          `json:"url"`
	ExperimentalServiceHost *core.ServiceID `json:"experimentalServiceHost"`
}

func (s *httpSchema) http(ctx *core.Context, parent *core.Query, args httpArgs) (*core.File, error) {
	// Use a filename that is set to the URL. Buildkit internally stores some cache metadata of etags
	// and http checksums using an id based on this name, so setting it to the URL maximizes our chances
	// of following more optimized cache codepaths.
	// Do a hash encode to prevent conflicts with use of `/` in the URL while also not hitting max filename limits
	filename := digest.FromString(args.URL).Encoded()

	svcs := core.ServiceBindings{}
	if args.ExperimentalServiceHost != nil {
		svc, err := args.ExperimentalServiceHost.Decode()
		if err != nil {
			return nil, err
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

	opts := []llb.HTTPOption{
		llb.Filename(filename),
	}

	useDNS := len(svcs) > 0

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err == nil && !useDNS {
		useDNS = len(clientMetadata.ParentClientIDs) > 0
	}

	var st llb.State
	if useDNS {
		// NB: only configure search domains if we're directly using a service, or
		// if we're nested.
		//
		// we have to be a bit selective here to avoid breaking Dockerfile builds
		// that use a Buildkit frontend (# syntax = ...).
		//
		// TODO: add API cap
		st = httpdns.State(args.URL, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.HTTP(args.URL, opts...)
	}

	return core.NewFileSt(ctx, st, filename, parent.PipelinePath(), s.platform, svcs)
}

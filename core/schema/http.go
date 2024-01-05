package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/httpdns"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"github.com/dagger/dagger/dagql"
)

var _ SchemaResolvers = &httpSchema{}

type httpSchema struct {
	srv *dagql.Server
}

func (s *httpSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("http", s.http).
			Doc(`Returns a file containing an http remote url content.`).
			ArgDoc("url", `HTTP url to get the content from (e.g., "https://docs.dagger.io").`).
			ArgDoc("experimentalServiceHost", `A service which must be started before the URL is fetched.`),
	}.Install(s.srv)
}

type httpArgs struct {
	URL                     string
	ExperimentalServiceHost dagql.Optional[core.ServiceID]
}

func (s *httpSchema) http(ctx context.Context, parent *core.Query, args httpArgs) (*core.File, error) {
	// Use a filename that is set to the URL. Buildkit internally stores some cache metadata of etags
	// and http checksums using an id based on this name, so setting it to the URL maximizes our chances
	// of following more optimized cache codepaths.
	// Do a hash encode to prevent conflicts with use of `/` in the URL while also not hitting max filename limits
	filename := digest.FromString(args.URL).Encoded()

	svcs := core.ServiceBindings{}
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, core.ServiceBinding{
			ID:       svc.ID(),
			Service:  svc.Self,
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

	return core.NewFileSt(ctx, parent, st, filename, parent.Platform, svcs)
}

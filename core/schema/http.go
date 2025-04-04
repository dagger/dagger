package schema

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/moby/buildkit/util/tracing"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
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

// steps:
// - CacheKey called - makes If-None-Match request
//     - we lookup previously stored header metadata for the url, and send all the
//       ETags we've previously received. if we get a hit, return a cache key
//       that collides with the previously stored header.
//       - there's some weird logic to handle servers that don't handle
//         If-None-Match, this adds an extra HEAD request - we can probably
//         safely cut this (the server is fucked in this case)
//     - as part of making an ETag request, we might get a full response back -
//       so we can save that for later with our header metadata and full results
//     - return a cachekey hash(url + checksum)
// - Solver will magically determine if we've seen this before.
// - Exec called
//     - checks to see if during CacheKey call we found a match, we can
//       shortcut here and return early
//     - otherwise, just make the full request, and return.

// in dagql world:
// - We don't want to do this fancy dance at all.
// - This should *all* be one function - do an ETag lookup, and return the full
//   result later - we're still able to share snapshots based on ETag + URL
//   internally to the implementation.

// in inbetween world, you cannot modify a cache key after computation. so we
// still need to have separate functions, which makes it harder to move towards
// the same place.
// - We need to be able to compute a buildkit cache key.
// - One option is to use the dagql cache key (and expose a dagql.Server method
//   to be able to do that).
//     - This requires that we put the ETag logic into a dagql cache key though :(
//       1. this is kinda miserable, and 2. we don't have a good way of caching
//          cache-keys. we actually just want to return the content-hashed response.
// - Another option is to write a separate function and attach it to the field,
//   that dagop can use to compute the cache key.
// - Another option is to just... yeet the previous behaviour. Don't do cache
//   key computation in the first place. Within the context of a single
//   buildkit graph, all the same requests will collide anyways - it's only an
//   issue for cache persistence. The problem is, this would result in saved
//   cache data that is actually out-of-date potentially.

func (s *httpSchema) http(ctx context.Context, query *core.Query, args httpArgs) (*core.File, error) {
	// Use a filename that is set to the URL. Buildkit internally stores some cache metadata of etags
	// and http checksums using an id based on this name, so setting it to the URL maximizes our chances
	// of following more optimized cache codepaths.
	// Do a hash encode to prevent conflicts with use of `/` in the URL while also not hitting max filename limits
	filename := digest.FromString(args.URL).Encoded()

	bindings := core.ServiceBindings{}
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, core.ServiceBinding{
			ID:       svc.ID(),
			Service:  svc.Self,
			Hostname: host,
		})
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, bindings)
	if err != nil {
		return nil, err
	}
	defer detach()

	req, err := http.NewRequest("GET", args.URL, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	transport := netconfhttp.NewTransport(tracing.DefaultTransport, query.DNS())
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	dt, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return core.NewFileWithContents(ctx, query, filename, dt, 0644, nil, query.Platform())

	// opts := []llb.HTTPOption{
	// 	llb.Filename(filename),
	// }
	//
	// clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	// if err != nil {
	// 	return nil, err
	// }

	// st := httpdns.HTTP(args.URL, clientMetadata.SessionID, opts...)
	// return core.NewFileSt(ctx, parent, st, filename, parent.Platform(), svcs)
}

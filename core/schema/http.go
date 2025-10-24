package schema

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
)

var _ SchemaResolvers = &httpSchema{}

type httpSchema struct{}

func (s *httpSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("http", s.http, dagql.CachePerClient).
			Doc(`Returns a file containing an http remote url content.`).
			Args(
				dagql.Arg("url").Doc(`HTTP url to get the content from (e.g., "https://docs.dagger.io").`),
				dagql.Arg("name").Doc(`File name to use for the file. Defaults to the last part of the URL.`),
				dagql.Arg("permissions").Doc(`Permissions to set on the file.`),
				dagql.Arg("authHeader").Doc(`Secret used to populate the Authorization HTTP header`),
				dagql.Arg("experimentalServiceHost").Doc(`A service which must be started before the URL is fetched.`),
			),
	}.Install(srv)
}

type httpArgs struct {
	URL                     string
	Name                    *string
	Permissions             *int
	AuthHeader              dagql.Optional[core.SecretID]
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	FSDagOpInternalArgs
	RefID string `internal:"true" default:"" name:"refID"`
}

func (s *httpSchema) httpPath(ctx context.Context, parent *core.Query, args httpArgs) (string, error) {
	if args.Name != nil {
		return *args.Name, nil
	}

	parsed, err := url.Parse(args.URL)
	if err != nil {
		return "", err
	}
	filename := filepath.Base(parsed.Path)
	if filename == "" || filename == "." || filename == "/" {
		filename = "index"
	}
	return filename, nil
}

func (s *httpSchema) http(ctx context.Context, parent dagql.ObjectResult[*core.Query], args httpArgs) (inst dagql.ObjectResult[*core.File], rerr error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}

	if args.InDagOp() {
		cache := parent.Self().BuildkitCache()
		snap, err := cache.Get(ctx, args.RefID, nil)
		if err != nil {
			return inst, err
		}
		snap = snap.Clone()

		f := core.NewFile(nil, args.DagOpPath, parent.Self().Platform(), nil)
		f.Result = snap
		return dagql.NewObjectResultForCurrentID(ctx, srv, f)
	}

	filename, err := s.httpPath(ctx, parent.Self(), args)
	if err != nil {
		return inst, err
	}
	permissions := 0600
	if args.Permissions != nil {
		permissions = *args.Permissions
	}

	var authHeader string
	if args.AuthHeader.Valid {
		secret, err := args.AuthHeader.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		secretStore, err := parent.Self().Secrets(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get secret store: %w", err)
		}
		authHeaderRaw, err := secretStore.GetSecretPlaintext(ctx, secret.ID().Digest())
		if err != nil {
			return inst, err
		}
		authHeader = string(authHeaderRaw)
	}

	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		host, err := svc.Self().Hostname(ctx, svc.ID())
		if err != nil {
			return inst, err
		}
		binding := core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		}

		svcs, err := parent.Self().Services(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get services: %w", err)
		}
		detach, _, err := svcs.StartBindings(ctx, []core.ServiceBinding{binding})
		if err != nil {
			return inst, err
		}
		defer detach()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return inst, err
	}
	if authHeader != "" {
		req.Header.Add("Authorization", authHeader)
	}
	snap, dgst, resp, err := core.DoHTTPRequest(ctx, parent.Self(), req, filename, permissions)
	if err != nil {
		return inst, err
	}
	defer resp.Body.Close()
	defer snap.Release(context.WithoutCancel(ctx))

	// also mixin the checksum
	newID := dagql.CurrentID(ctx).
		WithArgument(call.NewArgument(
			"refID",
			call.NewLiteralString(snap.ID()),
			false,
		)).
		WithDigest(hashutil.HashStrings(
			filename,
			fmt.Sprint(permissions),
			dgst.String(),
			resp.Header.Get("Last-Modified"),
		))
	ctxDagOp := dagql.ContextWithID(ctx, newID)

	file, err := DagOpFile(ctxDagOp, srv, parent.Self(), args, s.http, WithPathFn(s.httpPath))
	if err != nil {
		return inst, err
	}

	// evaluate now! so that the snapshot definitely lives long enough
	if _, err := file.Evaluate(ctx); err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForID(file, srv, newID)
}

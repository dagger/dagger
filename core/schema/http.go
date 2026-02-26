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
		dagql.NodeFunc("http", s.http).
			WithInput(dagql.PerClientInput).
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
		authHeaderRaw, err := secretStore.GetSecretPlaintext(ctx, core.SecretIDDigest(secret.ID()))
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

	// Keep recipe digest as the authoritative call identity.
	// Attach HTTP output identity as content digest so equivalent outputs can
	// still converge in cache/egraph flows.
	outputDigest := hashutil.HashStrings(
		filename,
		fmt.Sprint(permissions),
		dgst.String(),
		resp.Header.Get("Last-Modified"),
	)
	resultID := dagql.CurrentID(ctx).With(call.WithContentDigest(outputDigest))
	file := &core.File{
		File:      filename,
		Platform:  parent.Self().Platform(),
		LazyState: core.NewLazyState(),
		Snapshot:  snap.Clone(),
	}
	file.LazyInitComplete = true

	inst, err = dagql.NewObjectResultForID(file, srv, resultID)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

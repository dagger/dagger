package schema

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

var _ SchemaResolvers = &httpSchema{}

type httpSchema struct{}

func (s *httpSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("http", s.http).
			WithInput(dagql.PerSessionInput).
			Doc(`Returns a file containing an http remote url content.`).
			Args(
				dagql.Arg("url").Doc(`HTTP url to get the content from (e.g., "https://docs.dagger.io").`),
				dagql.Arg("name").Doc(`File name to use for the file. Defaults to the last part of the URL.`),
				dagql.Arg("permissions").Doc(`Permissions to set on the file.`),
				dagql.Arg("checksum").Doc(`Expected digest of the downloaded content (e.g., "sha256:...").`),
				dagql.Arg("authHeader").Doc(`Secret used to populate the Authorization HTTP header`),
				dagql.Arg("experimentalServiceHost").Doc(`A service which must be started before the URL is fetched.`),
			),
		dagql.NodeFunc("_httpState", s.httpState).
			IsPersistable().
			Doc(`(Internal-only) Returns a persistent HTTP state object.`).
			Args(
				dagql.Arg("url").Doc(`HTTP url to get the content from.`),
			),
	}.Install(srv)

	dagql.Fields[*core.HTTPState]{
		dagql.NodeFunc("_resolve", s.httpStateResolve).
			IsPersistable().
			WithInput(dagql.PerSessionInput).
			Doc(`(Internal-only) Resolve the HTTP state once per session and return the resulting file.`).
			Args(
				dagql.Arg("checksum").Doc(`Expected digest of the downloaded content.`),
				dagql.Arg("permissions").Doc(`Permissions to set on the file.`),
				dagql.Arg("name").Doc(`Resolved file name to use for the file.`),
			),
	}.Install(srv)
}

type httpArgs struct {
	URL                     string
	Name                    dagql.Optional[dagql.String]
	Permissions             dagql.Optional[dagql.Int]
	Checksum                dagql.Optional[dagql.String]
	AuthHeader              dagql.Optional[core.SecretID]
	ExperimentalServiceHost dagql.Optional[core.ServiceID]
}

type httpStateArgs struct {
	URL string
}

type httpStateResolveArgs struct {
	Checksum    dagql.Optional[dagql.String]
	Permissions int
	Name        string
}

func (s *httpSchema) httpPath(ctx context.Context, parent *core.Query, args httpArgs) (string, error) {
	if args.Name.Valid {
		return string(args.Name.Value), nil
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

func (s *httpSchema) http(ctx context.Context, parent dagql.ObjectResult[*core.Query], args httpArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}

	filename, err := s.httpPath(ctx, parent.Self(), args)
	if err != nil {
		return inst, err
	}
	permissions := int(args.Permissions.GetOr(dagql.Int(0600)))

	if args.AuthHeader.Valid || args.ExperimentalServiceHost.Valid {
		authHeader, detach, err := s.resolveHTTPSessionContext(ctx, parent.Self(), srv, args)
		if err != nil {
			return inst, err
		}
		if detach != nil {
			defer detach()
		}

		fetched, err := core.FetchHTTPFile(ctx, parent.Self(), core.FetchHTTPRequestOpts{
			URL:                 args.URL,
			Filename:            filename,
			Permissions:         permissions,
			Checksum:            args.Checksum,
			AuthorizationHeader: authHeader,
		})
		if err != nil {
			return inst, err
		}
		return s.newHTTPFileResult(ctx, srv, fetched, permissions, args.Checksum)
	}

	if err := srv.Select(ctx, parent, &inst, dagql.Selector{
		Field: "_httpState",
		Args: []dagql.NamedInput{
			{Name: "url", Value: dagql.String(args.URL)},
		},
	}, dagql.Selector{
		Field: "_resolve",
		Args: []dagql.NamedInput{
			{Name: "checksum", Value: args.Checksum},
			{Name: "permissions", Value: dagql.Int(permissions)},
			{Name: "name", Value: dagql.String(filename)},
		},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *httpSchema) httpState(ctx context.Context, parent dagql.ObjectResult[*core.Query], args httpStateArgs) (*core.HTTPState, error) {
	return &core.HTTPState{URL: args.URL}, nil
}

func (s *httpSchema) httpStateResolve(ctx context.Context, parent dagql.ObjectResult[*core.HTTPState], args httpStateResolveArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("current query: %w", err)
	}
	fetched, err := parent.Self().Resolve(ctx, query, args.Checksum, args.Permissions, args.Name)
	if err != nil {
		return inst, err
	}
	return s.newHTTPFileResult(ctx, srv, fetched, args.Permissions, args.Checksum)
}

func (s *httpSchema) resolveHTTPSessionContext(
	ctx context.Context,
	query *core.Query,
	srv *dagql.Server,
	args httpArgs,
) (authHeader string, detach func(), err error) {
	if args.AuthHeader.Valid {
		secret, err := args.AuthHeader.Value.Load(ctx, srv)
		if err != nil {
			return "", nil, err
		}
		authHeaderRaw, err := secret.Self().Plaintext(ctx)
		if err != nil {
			return "", nil, err
		}
		authHeader = string(authHeaderRaw)
	}

	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return "", nil, err
		}
		svcDig, err := svc.ContentPreferredDigest(ctx)
		if err != nil {
			return "", nil, err
		}
		host, err := svc.Self().Hostname(ctx, svcDig)
		if err != nil {
			return "", nil, err
		}
		binding := core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		}
		svcs, err := query.Services(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("failed to get services: %w", err)
		}
		detach, _, err = svcs.StartBindings(ctx, []core.ServiceBinding{binding})
		if err != nil {
			return "", nil, err
		}
	}

	return authHeader, detach, nil
}

func (s *httpSchema) newHTTPFileResult(
	ctx context.Context,
	srv *dagql.Server,
	fetched *core.HTTPFetchResult,
	permissions int,
	checksum dagql.Optional[dagql.String],
) (inst dagql.ObjectResult[*core.File], err error) {
	filePath, _ := fetched.File.File.Peek()
	outputDigest := hashutil.HashStrings(
		filePath,
		fmt.Sprint(permissions),
		fetched.ContentDigest.String(),
		fetched.LastModified,
		string(checksum.GetOr(dagql.String(""))),
	)
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, fetched.File)
	if err != nil {
		_ = fetched.File.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	inst, err = inst.WithContentDigest(ctx, outputDigest)
	if err != nil {
		_ = fetched.File.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	return inst, nil
}

func parseChecksumArg(checksum *string) (digest.Digest, error) {
	if checksum == nil || *checksum == "" {
		return "", nil
	}
	parsed, err := digest.Parse(*checksum)
	if err != nil {
		return "", fmt.Errorf("invalid checksum %q: %w", *checksum, err)
	}
	return parsed, nil
}

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
		dagql.NodeFunc("_httpResolved", s.httpResolved).
			IsPersistable().
			Doc(`(Internal-only) Returns a file containing an http remote url content.`).
			Args(
				dagql.Arg("url").Doc(`HTTP url to get the content from.`),
				dagql.Arg("name").Doc(`Resolved file name to use for the file.`),
				dagql.Arg("permissions").Doc(`Permissions to set on the file.`),
				dagql.Arg("checksum").Doc(`Expected digest of the downloaded content.`),
				dagql.Arg("resolvedETag").Doc(`(Internal-only) resolved ETag version.`),
				dagql.Arg("resolvedLastModified").Doc(`(Internal-only) resolved Last-Modified version.`),
				dagql.Arg("resolvedDigest").Doc(`(Internal-only) resolved digest version.`),
			),
	}.Install(srv)
}

type httpArgs struct {
	URL                     string
	Name                    *string
	Permissions             *int
	Checksum                *string
	AuthHeader              dagql.Optional[core.SecretID]
	ExperimentalServiceHost dagql.Optional[core.ServiceID]
}

type httpResolvedArgs struct {
	URL         string
	Name        *string
	Permissions *int
	Checksum    *string

	ResolvedETag         *string `name:"resolvedETag" internal:"true"`
	ResolvedLastModified *string `internal:"true"`
	ResolvedDigest       *string `internal:"true"`
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

func (s *httpSchema) http(ctx context.Context, parent dagql.ObjectResult[*core.Query], args httpArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}

	filename, err := s.httpPath(ctx, parent.Self(), args)
	if err != nil {
		return inst, err
	}
	permissions := valueOrDefault(args.Permissions, 0600)

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

	resolved, err := core.ResolveHTTPVersion(ctx, parent.Self(), core.ResolveHTTPRequestVersionOpts{
		URL:      args.URL,
		Checksum: args.Checksum,
	})
	if err != nil {
		return inst, err
	}

	selectArgs := []dagql.NamedInput{
		{Name: "url", Value: dagql.String(args.URL)},
	}
	if args.Name != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "name", Value: dagql.String(*args.Name)})
	}
	if args.Permissions != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "permissions", Value: dagql.Int(*args.Permissions)})
	}
	if args.Checksum != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "checksum", Value: dagql.String(*args.Checksum)})
	}
	if resolved.ETag != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "resolvedETag", Value: dagql.String(*resolved.ETag)})
	}
	if resolved.LastModified != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "resolvedLastModified", Value: dagql.String(*resolved.LastModified)})
	}
	if resolved.Digest != nil {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "resolvedDigest", Value: dagql.String(*resolved.Digest)})
	}

	if err := srv.Select(ctx, parent, &inst, dagql.Selector{
		Field: "_httpResolved",
		Args:  selectArgs,
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *httpSchema) httpResolved(ctx context.Context, parent dagql.ObjectResult[*core.Query], args httpResolvedArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql server: %w", err)
	}

	filename := valueOrDefault(args.Name, "index")
	permissions := valueOrDefault(args.Permissions, 0600)
	fetched, err := core.FetchHTTPFile(ctx, parent.Self(), core.FetchHTTPRequestOpts{
		URL:                  args.URL,
		Filename:             filename,
		Permissions:          permissions,
		Checksum:             args.Checksum,
		ResolvedETag:         args.ResolvedETag,
		ResolvedLastModified: args.ResolvedLastModified,
		ResolvedDigest:       args.ResolvedDigest,
	})
	if err != nil {
		return inst, err
	}
	return s.newHTTPFileResult(ctx, srv, fetched, permissions, args.Checksum)
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
	checksum *string,
) (inst dagql.ObjectResult[*core.File], err error) {
	outputDigest := hashutil.HashStrings(
		fetched.File.File,
		fmt.Sprint(permissions),
		fetched.ContentDigest.String(),
		fetched.LastModified,
		valueOrDefault(checksum, ""),
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

func valueOrDefault[T any](value *T, defaultValue T) T {
	if value == nil {
		return defaultValue
	}
	return *value
}

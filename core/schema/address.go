package schema

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

type addressSchema struct{}

var _ SchemaResolvers = &addressSchema{}

func (s *addressSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("address", s.address).
			Doc(`initialize an address to load directories, containers, secrets or other object types.`),
	}.Install(srv)

	dagql.Fields[*core.Address]{
		dagql.Func("value", s.value).
			Doc(`The address value`),
		dagql.NodeFuncWithCacheKey("container", s.container, dagql.CachePerCall).
			Doc(`Load a container from the address.`),
		dagql.NodeFuncWithCacheKey("directory", s.directory, dagql.CacheAsRequested).
			Doc(`Load a directory from the address.`),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CacheAsRequested).
			Doc(`Load a file from the address.`),
		dagql.NodeFuncWithCacheKey("gitRef", s.gitRef, dagql.CachePerClient).
			Doc(`Load a git ref (branch, tag or commit) from the address.`),
		dagql.NodeFuncWithCacheKey("gitRepository", s.gitRepository, dagql.CachePerClient).
			Doc(`Load a git repository from the address.`),
		dagql.NodeFuncWithCacheKey("secret", s.secret, dagql.CachePerCall).
			Doc(`Load a secret from the address.`),
		dagql.NodeFuncWithCacheKey("service", s.service, dagql.CachePerClient).
			Doc(`Load a service from the address.`),
	}.Install(srv)
}

func (s *addressSchema) value(ctx context.Context, parent *core.Address, args struct{}) (string, error) {
	return parent.Value, nil
}

func (s *addressSchema) address(ctx context.Context, root *core.Query, args struct {
	Value dagql.String
}) (*core.Address, error) {
	addr := args.Value.String()
	if addr == "" {
		return nil, fmt.Errorf("resource cannot have empty address")
	}
	return &core.Address{
		Value: addr,
	}, nil
}

type loadFileArgs struct {
	core.CopyFilter
	HostDirCacheConfig
}

func (s *addressSchema) file(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args loadFileArgs,
) (
	inst dagql.ObjectResult[*core.File],
	err error,
) {
	var (
		q    []dagql.Selector
		addr = r.Self().Value
	)
	gitURL, err := gitutil.ParseURL(addr)
	if err == nil {
		// Remote file
		q = queryRemoteGitRoot(gitURL)
		if gitURL.Fragment == nil || gitURL.Fragment.Subdir == "" {
			return inst, fmt.Errorf("no file path specified within git repository")
		}
		q = append(q, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(gitURL.Fragment.Subdir),
				},
			},
		})
	} else {
		// Local file
		q = []dagql.Selector{
			{
				Field: "host",
			},
			{
				Field: "file",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.NewString(getLocalPath(addr)),
					},
				},
			},
		}
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, srv.Root(), &inst, q...); err != nil {
		return inst, err
	}
	return inst, nil
}

type loadDirectoryArgs struct {
	core.CopyFilter
	HostDirCacheConfig
}

func (s *addressSchema) directory(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args loadDirectoryArgs,
) (
	inst dagql.ObjectResult[*core.Directory],
	err error,
) {
	var (
		q    []dagql.Selector
		addr = r.Self().Value
	)
	gitURL, err := gitutil.ParseURL(addr)
	if err == nil {
		// Remote directory (using git remote)
		q = queryRemoteGitRoot(gitURL)
		if gitURL.Fragment != nil && gitURL.Fragment.Subdir != "" {
			q = append(q, dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.NewString(gitURL.Fragment.Subdir),
					},
				},
			})
		}
	} else {
		// Local directory
		q = []dagql.Selector{
			{
				Field: "host",
			},
			{
				Field: "directory",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.NewString(getLocalPath(addr)),
					},
				},
			},
		}
		if len(args.Exclude) > 0 {
			q[1].Args = append(q[1].Args, dagql.NamedInput{
				Name:  "exclude",
				Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args.Exclude...)),
			})
		}
		if len(args.Include) > 0 {
			q[1].Args = append(q[1].Args, dagql.NamedInput{
				Name:  "include",
				Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args.Include...)),
			})
		}
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, srv.Root(), &inst, q...); err != nil {
		return inst, err
	}
	return inst, nil
}

func queryRemoteGitRef(gitURL *gitutil.GitURL) []dagql.Selector {
	q := queryRemoteGitRepository(gitURL)
	// .head() or .ref()
	if gitURL.Fragment == nil || gitURL.Fragment.Ref == "" {
		q = append(q, dagql.Selector{
			Field: "head",
		})
	} else {
		q = append(q, dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString(gitURL.Fragment.Ref),
				},
			},
		})
	}
	return q
}

// Build a query for selecting the root of a repo from a git url
// The subdir path is left to the caller to process (might be a file or directory)
func queryRemoteGitRoot(gitURL *gitutil.GitURL) []dagql.Selector {
	q := queryRemoteGitRef(gitURL)
	// .tree()
	q = append(q, dagql.Selector{
		Field: "tree",
	})
	return q
}

// Convert an address to an absolute local path:
// - file:// is stripped if needed
func getLocalPath(path string) string {
	// allow `file://` scheme or no scheme
	path = strings.TrimPrefix(path, "file://")
	// make windows paths usable in the Linux engine container
	// FIXME: still needed engine-side?
	path = filepath.ToSlash(path)

	return path
}

func (s *addressSchema) container(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Container],
	err error,
) {
	var (
		addr = r.Self().Value
	)
	q := []dagql.Selector{
		{
			Field: "container",
		},
		{
			Field: "from",
			Args: []dagql.NamedInput{
				{
					Name:  "address",
					Value: dagql.NewString(addr),
				},
			},
		},
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, srv.Root(), &inst, q...)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *addressSchema) gitRepository(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.GitRepository],
	err error,
) {
	var (
		q    []dagql.Selector
		addr = r.Self().Value
	)
	gitURL, err := gitutil.ParseURL(addr)
	if err == nil {
		// Remote repository
		if gitURL.Fragment != nil {
			if gitURL.Fragment.Ref != "" {
				return inst, fmt.Errorf("git repository address cannot contain ref")
			}
			if gitURL.Fragment.Subdir != "" {
				return inst, fmt.Errorf("git repository address cannot contain subdir")
			}
		}
		q = queryRemoteGitRepository(gitURL)
	} else {
		// Local repository
		q = queryLocalGitRepository(getLocalPath(addr))
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, srv.Root(), &inst, q...); err != nil {
		return inst, err
	}
	return inst, nil
}

func queryLocalGitRepository(path string) []dagql.Selector {
	return []dagql.Selector{
		{
			Field: "host",
		},
		{
			Field: "directory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(path),
				},
			},
		},
		{
			Field: "asGit",
		},
	}
}

func queryLocalGitRef(path, ref string) []dagql.Selector {
	q := queryLocalGitRepository(path)
	if ref == "" {
		q = append(q, dagql.Selector{
			Field: "head",
		})
	} else {
		q = append(q, dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString(ref),
				},
			},
		})
	}
	return q
}

func queryRemoteGitRepository(gitURL *gitutil.GitURL) []dagql.Selector {
	return []dagql.Selector{
		{
			Field: "git",
			Args: []dagql.NamedInput{
				{
					Name:  "url",
					Value: dagql.NewString(gitURL.Remote()),
				},
			},
		},
	}
}

func (s *addressSchema) gitRef(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.GitRef],
	err error,
) {
	var (
		addr = r.Self().Value
		q    []dagql.Selector
	)
	gitURL, err := gitutil.ParseURL(addr)
	if err == nil {
		// Remote ref
		if gitURL.Fragment != nil && gitURL.Fragment.Subdir != "" {
			return inst, fmt.Errorf("git ref address cannot contain subdir")
		}
		q = queryRemoteGitRef(gitURL)
	} else {
		// Local ref
		path, ref, _ := strings.Cut(addr, "#")
		q = queryLocalGitRef(getLocalPath(path), ref)
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, srv.Root(), &inst, q...)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *addressSchema) secret(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Secret],
	err error,
) {
	var (
		cacheKey string
		addr     = r.Self().Value
	)
	if !strings.Contains(addr, ":") {
		// case of e.g. `MY_ENV_SECRET`, which is shorthand for `env://MY_ENV_SECRET`
		addr = "env://" + addr
	}
	// legacy secrets in the form of `--token env:MY_ENV_SECRET` instead of `env://MY_ENV_SECRET`
	secretSource, val, _ := strings.Cut(addr, ":")
	if !strings.HasPrefix(val, "//") {
		addr = secretSource + "://" + val
	}

	// Parse the address to separate the base address from query parameters
	addrWithoutQuery, queryValsStr, ok := strings.Cut(addr, "?")
	if ok && len(queryValsStr) > 0 {
		// Parse the query parameters from the address
		queryVals, err := url.ParseQuery(queryValsStr)
		if err != nil {
			return inst, err
		}
		// Extract the cacheKey parameter if present and remove it from the query
		if ckey := queryVals.Get("cacheKey"); ckey != "" {
			cacheKey = ckey
			queryVals.Del("cacheKey")
			queryValsStr = queryVals.Encode()
			// Reconstruct the address without the cacheKey parameter
			if len(queryValsStr) > 0 {
				addr = fmt.Sprintf("%s?%s", addrWithoutQuery, queryValsStr)
			} else {
				addr = addrWithoutQuery
			}
		}
	}
	q := []dagql.Selector{
		{
			Field: "secret",
			Args: []dagql.NamedInput{
				{
					Name:  "uri",
					Value: dagql.NewString(addr),
				},
				{
					Name:  "cacheKey",
					Value: dagql.Opt(dagql.String(cacheKey)),
				},
			},
		},
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, srv.Root(), &inst, q...)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *addressSchema) service(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Service],
	err error,
) {
	var (
		addr  = r.Self().Value
		host  string
		ports dagql.ArrayInput[dagql.InputObject[core.PortForward]]
	)
	u, err := url.Parse(addr)
	if err != nil {
		return inst, err
	}
	switch u.Scheme {
	case "tcp":
		h, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return inst, err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return inst, err
		}
		host = h
		ports = append(ports, dagql.InputObject[core.PortForward]{
			Value: core.PortForward{
				Backend:  nPort,
				Frontend: &nPort,
				Protocol: core.NetworkProtocolTCP,
			},
		})
	case "udp":
		h, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return inst, err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return inst, err
		}
		host = h
		ports = append(ports, dagql.InputObject[core.PortForward]{
			Value: core.PortForward{
				Backend:  nPort,
				Frontend: &nPort,
				Protocol: core.NetworkProtocolUDP,
			},
		})
	default:
		return inst, fmt.Errorf("unsupported service address. Must be a valid tcp:// or udp:// URL")
	}
	q := []dagql.Selector{
		{
			Field: "host",
		},
		{
			Field: "service",
			Args: []dagql.NamedInput{
				{
					Name:  "host",
					Value: dagql.NewString(host),
				},
				{
					Name:  "ports",
					Value: ports,
				},
			},
		},
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, srv.Root(), &inst, q...)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

package schema

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

// moduleRefCycleKey is the context key carrying the chain of in-flight
// module-reference strings, used to detect reference cycles.
type moduleRefCycleKey struct{}

// resolveModuleRef detects and resolves a module function reference of the
// bare form "<module>:<function>" (e.g. "docusaurus:serve"), wiring one
// module's function output into another object-typed value.
//
// Detection & precedence (commit-on-match, no silent fallback):
//   - The candidate must be a string containing EXACTLY one ":" with non-empty
//     parts on both sides. Strings containing "://" (URL-ish, e.g. "tcp://...")
//     are never module refs.
//   - The first segment is normalized to a gql field name and looked up on the
//     (non-canonical) current Query root's object type. Only if a field of that
//     name EXISTS — AND carries module provenance (FieldSpec.Module != nil),
//     which distinguishes a module entrypoint from a reserved core
//     field like "git" or "secret" that shares the root namespace — is the
//     string committed as a module ref.
//   - Once committed, any subsequent failure (unknown function, type mismatch,
//     cycle) is a HARD error and does NOT fall through to image/URL handling.
//
// Return values:
//   - (true, err): the string was committed as a module ref; err reports the
//     outcome of resolving it (nil on success).
//   - (false, nil): the string is not a module ref; the caller's existing
//     decoding logic should run unchanged.
//
// dest must be a typed dagql destination (e.g. *dagql.ObjectResult[*core.Service])
// so dagql's own typed Select produces the type-mismatch error.
func resolveModuleRef(ctx context.Context, addr string, dest any) (matched bool, err error) {
	// URL-ish strings are never module refs.
	if strings.Contains(addr, "://") {
		return false, nil
	}
	// A module ref candidate has exactly one ":" with non-empty parts.
	module, rest, ok := strings.Cut(addr, ":")
	if !ok || module == "" || rest == "" {
		return false, nil
	}

	// Use the non-canonical current server: module fields live on the
	// outer Query root, not the canonical core schema.
	srv := dagql.CurrentDagqlServer(ctx)
	if srv == nil {
		return false, nil
	}
	root := srv.Root()
	moduleField := strcase.ToLowerCamel(module)
	// Detect whether the module is actually installed by checking the Query
	// root's object type for a field of that name, rather than probing via a
	// Select. If it is not installed, this is not a module ref.
	spec, exists := root.ObjectType().FieldSpec(moduleField, srv.View)
	if !exists {
		// The name may be an installed workspace module that was not loaded
		// for this command: selector verbs (`dagger check <mod>:<item>`)
		// narrow module loading to the modules their patterns name, so a
		// module referenced only through another module's wiring is never
		// loaded. When the workspace config installs a module by this name,
		// demand-load it and retry against the refreshed client schema.
		// Loading is gated on config membership, so image refs (postgres:16)
		// never trigger module loads.
		refreshed, installed, loadErr := demandLoadInstalledModule(ctx, module)
		if !installed {
			return false, nil
		}
		if loadErr != nil {
			// Committed: the workspace installs this module, so the string is
			// a module ref and the load failure is the real error.
			return true, fmt.Errorf("resolve module reference %q: load module %q: %w", addr, module, loadErr)
		}
		srv = refreshed
		root = srv.Root()
		spec, exists = root.ObjectType().FieldSpec(moduleField, srv.View)
		if !exists {
			return false, nil
		}
	}
	// Core Query fields (host, git, secret, engine, container, http, module, ...)
	// share the Query root's namespace with module entrypoints, but only
	// module entrypoints carry module provenance (spec.Module). A field with no
	// Module is a core field — a reserved word — so leave it to the caller's normal
	// address decoding (e.g. "git:2.40" or "secret:foo" as an image/URL) rather
	// than committing it as a module ref.
	if spec.Module == nil {
		return false, nil
	}

	// Committed: from here on, any error is a hard module-ref error.

	// Only "<module>:<function>" (a single function segment) is supported today.
	// A matching module prefix followed by extra colons (e.g.
	// "backend:payment:server") is reported explicitly rather than silently
	// treated as an image ref.
	if strings.Contains(rest, ":") {
		return true, fmt.Errorf("invalid module reference %q: only %s:<function> is supported today (a single function segment); got extra segments in %q", addr, module, rest)
	}
	functionField := strcase.ToLowerCamel(rest)

	// Cycle guard: track the chain of in-flight module refs on the context
	// and refuse to descend into one already present. Context values propagate
	// through dagql Select into nested module construction, so re-entry of an
	// in-flight ref is detectable here. Without this, reference cycles hang the
	// engine with unbounded goroutine growth.
	//
	// The chain stores the NORMALIZED "<moduleField>:<functionField>" (both
	// lower-camel), not the raw addr, so equivalently-spelled refs (e.g. case
	// variants like "Foo:Bar" vs "foo:bar") still collide and produce the clean
	// cycle error instead of wedging on a cache wait. The raw addr is kept in the
	// user-facing message for readability.
	normalized := moduleField + ":" + functionField
	chain, _ := ctx.Value(moduleRefCycleKey{}).([]string)
	for _, seen := range chain {
		if seen == normalized {
			return true, fmt.Errorf("module reference cycle detected: %s -> %s",
				strings.Join(chain, " -> "), normalized)
		}
	}
	newChain := make([]string, len(chain)+1)
	copy(newChain, chain)
	newChain[len(chain)] = normalized
	ctx = context.WithValue(ctx, moduleRefCycleKey{}, newChain)

	// Resolve by selecting from the Query root into the typed destination: first
	// the module field, then the function field. dagql's typed Select enforces
	// that the function's return type matches dest, producing a clear
	// type-mismatch error.
	selectors := []dagql.Selector{
		{Field: moduleField},
		{Field: functionField},
	}
	if err := srv.Select(ctx, root, dest, selectors...); err != nil {
		return true, fmt.Errorf("resolve module reference %q (module %q): %w", addr, module, err)
	}
	return true, nil
}

// demandLoadInstalledModule loads and serves the named workspace module when
// the workspace config installs it but the current command has not loaded it
// (selector verbs narrow module loading to the modules their patterns name).
//
// Returns installed=false when the name is not an installed workspace module —
// the caller's normal address decoding should run unchanged. When installed,
// err reports the load outcome and srv is the refreshed schema served to the
// current client (which now carries the module as a root field).
func demandLoadInstalledModule(ctx context.Context, name string) (srv *dagql.Server, installed bool, err error) {
	// The lookups below deliberately discard their errors: any failure to see
	// the workspace from here means the string cannot be a demand-loadable
	// module ref, and the caller's normal address decoding should run.
	q, _ := core.CurrentQuery(ctx)
	if q == nil {
		return nil, false, nil
	}
	ws, _ := q.Server.CurrentWorkspace(ctx)
	if ws == nil {
		return nil, false, nil
	}
	// Only the workspace-owning client may trigger module loads from address
	// resolution. A module client must never demand-load workspace siblings
	// into its own session — modules only see their declared dependencies,
	// and this gate keeps a bare string from becoming a capability grant.
	md, _ := engine.ClientMetadataFromContext(ctx)
	if md == nil || md.ClientID != ws.ClientID {
		return nil, false, nil
	}
	cfg, _ := workspaceConfigWithCompatFallback(ctx, ws)
	if cfg == nil {
		return nil, false, nil
	}
	want := strcase.ToKebab(name)
	isEntrypoint := false
	for installedName, entry := range cfg.Modules {
		if strcase.ToKebab(installedName) == want {
			installed = true
			isEntrypoint = entry.Entrypoint
			break
		}
	}
	if !installed {
		return nil, false, nil
	}
	// An entrypoint module's functions are hoisted onto the Query root and no
	// module field is served for it, so a <module>:<function> reference can
	// never resolve. Committed: report that directly instead of loading the
	// module only to miss the retry and fall back to the generic address error.
	if isEntrypoint {
		return nil, true, fmt.Errorf("module %q is the workspace entrypoint; its functions are hoisted to the root and cannot be referenced as %q", name, name+":<function>")
	}
	// Strict (non-best-effort) load: the consumer's constructor requires this
	// module, so a load failure is that resolution's real error. This does not
	// undo best-effort operations like `dagger generate`: their own initial
	// best-effort pass records a failed module, EnsureWorkspaceModules returns
	// the recorded error here without reloading, and ModTree runs nodes
	// without fail-fast — so only the node that genuinely needs the broken
	// module fails, and repair generators keep running.
	if _, err := q.Server.EnsureWorkspaceModules(ctx, []string{name}, false); err != nil {
		return nil, true, err
	}
	deps, err := q.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, true, err
	}
	srv, err = deps.Schema(ctx)
	if err != nil {
		return nil, true, err
	}
	return srv, true, nil
}

// isBareRefShaped reports whether addr looks like it was intended as a bare
// module reference "<module>:<function>" — exactly one ":", no "://", and no
// "/". Such strings that fail normal address decoding almost always mean the
// user mistyped an installed module name, so callers wrap the fallback error
// with moduleRefHint to point at dagger.toml.
func isBareRefShaped(addr string) bool {
	if strings.Contains(addr, "://") || strings.Contains(addr, "/") {
		return false
	}
	return strings.Count(addr, ":") == 1
}

// moduleRefHint builds the near-miss hint appended to fallback errors for
// bare-ref-shaped addresses that matched no installed module. Kept identical
// between the .service() and .container() decoders.
func moduleRefHint(addr string) string {
	return fmt.Sprintf("if you meant to wire in another module's output, no installed module matches %q — check the [modules.X] keys in dagger.toml", addr)
}

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
		dagql.NodeFunc("container", s.container).
			WithInput(dagql.PerCallInput).
			Doc(`Load a container from the address.`),
		dagql.NodeFunc("directory", s.directory).
			WithInput(dagql.RequestedCacheInput("noCache")).
			Doc(`Load a directory from the address.`),
		dagql.NodeFunc("file", s.file).
			WithInput(dagql.RequestedCacheInput("noCache")).
			Doc(`Load a file from the address.`),
		dagql.NodeFunc("gitRef", s.gitRef).
			WithInput(dagql.PerClientInput).
			Doc(`Load a git ref (branch, tag or commit) from the address.`),
		dagql.NodeFunc("gitRepository", s.gitRepository).
			WithInput(dagql.PerClientInput).
			Doc(`Load a git repository from the address.`),
		dagql.NodeFunc("secret", s.secret).
			WithInput(dagql.PerCallInput).
			Doc(`Load a secret from the address.`),
		dagql.NodeFunc("service", s.service).
			WithInput(dagql.PerClientInput).
			Doc(`Load a service from the address.`),
		dagql.NodeFunc("socket", s.socket).
			WithInput(dagql.PerCallInput).
			Doc(`Load a local socket from the address.`),
	}.Install(srv)
}

func (s *addressSchema) value(ctx context.Context, parent *core.Address, args struct{}) (string, error) {
	return parent.Value, nil
}

func (s *addressSchema) address(ctx context.Context, root *core.Query, args struct {
	Value dagql.String
},
) (*core.Address, error) {
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
	var q []dagql.Selector
	addr := r.Self().Value
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

func queryLocalDirectory(path string, filter core.CopyFilter) []dagql.Selector {
	args := []dagql.NamedInput{
		{
			Name:  "path",
			Value: dagql.NewString(getLocalPath(path)),
		},
	}
	if len(filter.Exclude) > 0 {
		args = append(args, dagql.NamedInput{
			Name:  "exclude",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Exclude...)),
		})
	}
	if len(filter.Include) > 0 {
		args = append(args, dagql.NamedInput{
			Name:  "include",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Include...)),
		})
	}
	if filter.Gitignore {
		args = append(args, dagql.NamedInput{
			Name:  "gitignore",
			Value: dagql.Boolean(true),
		})
	}
	return []dagql.Selector{
		{Field: "host"},
		{Field: "directory", Args: args},
	}
}

func (s *addressSchema) directory(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args loadDirectoryArgs,
) (
	inst dagql.ObjectResult[*core.Directory],
	err error,
) {
	var q []dagql.Selector
	addr := r.Self().Value
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
		q = queryLocalDirectory(addr, args.CopyFilter)
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
	// Default to repo head
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
	q = append(q, dagql.Selector{
		Field: "tree",
	})
	return q
}

// Convert an address to an absolute local path:
// - file:// is stripped if needed
func getLocalPath(path string) string {
	// file://PATH -> PATH
	return strings.TrimPrefix(path, "file://")
}

func (s *addressSchema) container(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Container],
	err error,
) {
	addr := r.Self().Value
	if matched, err := resolveModuleRef(ctx, addr, &inst); matched {
		// The address named an installed module: it is committed as a
		// module reference. Any failure here is hard and must not fall
		// through to image interpretation. An image ref shadowed by a module
		// name can be forced with a fully-qualified registry path, which
		// never matches an installed module name.
		return inst, err
	}
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
	// Desugar through the canonical server so entrypoint proxies on the
	// outer Query root cannot shadow the core container constructor.
	coreSrv := srv.Canonical()
	err = coreSrv.Select(ctx, coreSrv.Root(), &inst, q...)
	if err != nil {
		// A bare-ref-shaped address that fell through to image resolution and
		// failed is most often a mistyped module ref; add a hint pointing at
		// dagger.toml. Keep wording consistent with the .service() decoder.
		if isBareRefShaped(addr) {
			return inst, fmt.Errorf("%w (%s)", err, moduleRefHint(addr))
		}
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
	var q []dagql.Selector
	addr := r.Self().Value
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
	var q []dagql.Selector
	addr := r.Self().Value
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
	var cacheKey string
	addr := r.Self().Value
	// MY_SECRET -> env://MY_SECRET
	if !strings.Contains(addr, ":") {
		addr = "env://" + addr
	}
	// legacy format:
	// env:MY_SECRET -> env://MY_SECRET
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
	q := selectSecret(addr, cacheKey)
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

func selectSecret(addr, cacheKey string) []dagql.Selector {
	sel := dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{Name: "uri", Value: dagql.NewString(addr)},
		},
	}
	if cacheKey != "" {
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  "cacheKey",
			Value: dagql.Opt(dagql.String(cacheKey)),
		})
	}
	return []dagql.Selector{sel}
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
		host     string
		ports    dagql.ArrayInput[dagql.InputObject[core.PortForward]]
		protocol core.NetworkProtocol
	)
	addr := r.Self().Value
	// A bare "<module>:<function>" naming an installed module is
	// committed as a module reference; any failure here is hard and does not
	// fall through to tcp:///udp:// interpretation.
	if matched, err := resolveModuleRef(ctx, addr, &inst); matched {
		return inst, err
	}
	// wrapFallback annotates fallback URL/host-port parse failures for
	// bare-ref-shaped addresses (e.g. a mistyped "docusarus:serve") with a hint
	// pointing at dagger.toml. Kept consistent with the .container() decoder.
	wrapFallback := func(err error) error {
		if isBareRefShaped(addr) {
			return fmt.Errorf("%w (%s)", err, moduleRefHint(addr))
		}
		return err
	}
	u, err := url.Parse(addr)
	if err != nil {
		return inst, wrapFallback(err)
	}
	h, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return inst, wrapFallback(err)
	}
	nPort, err := strconv.Atoi(port)
	if err != nil {
		return inst, wrapFallback(err)
	}
	host = h
	switch u.Scheme {
	case "tcp":
		protocol = core.NetworkProtocolTCP
	case "udp":
		protocol = core.NetworkProtocolUDP
	default:
		return inst, wrapFallback(fmt.Errorf("unsupported service address: %q. Must be a valid tcp:// or udp:// URL", u.Scheme))
	}
	portInputAny, err := (dagql.InputObject[core.PortForward]{}).Decoder().DecodeInput(map[string]any{
		"frontend": nPort,
		"backend":  nPort,
		"protocol": string(protocol),
	})
	if err != nil {
		return inst, fmt.Errorf("decode service address port forward input: %w", err)
	}
	portInput, ok := portInputAny.(dagql.InputObject[core.PortForward])
	if !ok {
		return inst, fmt.Errorf("decode service address port forward input: unexpected input %T", portInputAny)
	}
	ports = append(ports, portInput)
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

func (s *addressSchema) socket(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Socket],
	err error,
) {
	addr := r.Self().Value
	path := strings.TrimPrefix(addr, "unix://")
	q := []dagql.Selector{
		{
			Field: "host",
		},
		{
			Field: "unixSocket",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(path),
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

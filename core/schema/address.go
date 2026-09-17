package schema

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

// moduleRefCycleKey is the context key carrying the chain of in-flight
// module-reference strings, used to detect reference cycles.
type moduleRefCycleKey struct{}

// resolveModuleRef uses the address's workspace, or the legacy client schema.
// Only an absent reference permits external fallback.
func resolveModuleRef(ctx context.Context, address *core.Address, dest any) (bool, error) {
	addr := address.Value
	if strings.Contains(addr, "://") {
		return false, nil
	}
	ws := address.BoundWorkspace
	if ws.Self() == nil {
		if address.ExternalOnly {
			return false, nil
		}
		return resolveLegacyModuleRef(ctx, addr, dest)
	}
	ctx, err := withWorkspaceClientContext(ctx, ws.Self())
	if err != nil {
		return true, err
	}
	ctx = core.WorkspaceToContext(ctx, ws)
	module, rest, qualified := strings.Cut(addr, ":")
	var srv *dagql.Server
	if qualified {
		srv, err = workspaceModuleSchema(ctx, ws, module)
		if err != nil {
			return true, fmt.Errorf("resolve module reference %q: %w", addr, err)
		}
	}
	if srv == nil {
		if !workspace.IsShortFormModuleRef(module) {
			return false, nil
		}
		module, srv, err = workspaceShorthandModule(ctx, ws, module)
		if err != nil {
			return true, fmt.Errorf("resolve module reference %q: %w", addr, err)
		}
		if srv == nil {
			return false, nil
		}
		rest = addr
	}
	return true, selectModuleRef(ctx, srv, addr, module, rest, dest)
}

// resolveLegacyModuleRef retains the current-client lookup and single-field
// syntax used before Workspace.resolve.
func resolveLegacyModuleRef(ctx context.Context, addr string, dest any) (bool, error) {
	module, rest, qualified := strings.Cut(addr, ":")
	if !qualified {
		if !workspace.IsShortFormModuleRef(addr) {
			return false, nil
		}
		entrypoint, found := workspaceEntrypointModuleName(ctx)
		if !found {
			return false, nil
		}
		module, rest = entrypoint, addr
	} else if module == "" || rest == "" {
		return false, nil
	}
	srv := dagql.CurrentDagqlServer(ctx)
	if srv == nil {
		return false, nil
	}
	srv = srv.Canonical()
	moduleField := strcase.ToLowerCamel(module)
	spec, exists := srv.Root().ObjectType().FieldSpec(moduleField, srv.View)
	if !exists {
		// Selector commands may have loaded only the consumer of this module.
		refreshed, installed, err := demandLoadInstalledModule(ctx, module)
		if !installed {
			return false, nil
		}
		if err != nil {
			return true, fmt.Errorf("resolve module reference %q: load module %q: %w", addr, module, err)
		}
		srv = refreshed.Canonical()
		spec, exists = srv.Root().ObjectType().FieldSpec(moduleField, srv.View)
		if !exists {
			return false, nil
		}
	}
	// Core fields such as git and container are not installed modules.
	if spec.Module == nil {
		return false, nil
	}
	if strings.Contains(rest, ":") {
		return true, fmt.Errorf("invalid module reference %q: only %s:<function> is supported today (a single function segment); got extra segments in %q", addr, module, rest)
	}
	if !qualified {
		obj, exists := srv.ObjectType(spec.Type.Type().Name())
		if exists {
			if _, exists := obj.FieldSpec(strcase.ToLowerCamel(rest), srv.View); !exists {
				return false, nil
			}
		}
	}
	return true, selectModuleRef(ctx, srv, addr, module, rest, dest)
}

func selectModuleRef(ctx context.Context, srv *dagql.Server, addr, module, rest string, dest any) error {
	srv = srv.Canonical()
	root := srv.Root()
	moduleField := strcase.ToLowerCamel(module)
	spec, exists := root.ObjectType().FieldSpec(moduleField, srv.View)
	if !exists {
		return fmt.Errorf("workspace module %q has no constructor", module)
	}
	fields := core.NewModTreePath(rest).APICase()
	selectors := []dagql.Selector{{
		Field: moduleField,
		Args:  core.WithBoundWorkspaceArgs(ctx, srv, spec.Args.Inputs(srv.View), nil),
	}}
	parentType := spec.Type.Type()
	for _, field := range fields {
		objType, exists := srv.ObjectType(parentType.Name())
		if !exists || parentType.Elem != nil {
			return fmt.Errorf("resolve module reference %q: cannot traverse %s", addr, parentType)
		}
		fieldSpec, exists := objType.FieldSpec(field, srv.View)
		if !exists {
			return fmt.Errorf("resolve module reference %q: %s has no field %q", addr, parentType.Name(), field)
		}
		selectors = append(selectors, dagql.Selector{
			Field: field,
			Args:  core.WithBoundWorkspaceArgs(ctx, srv, fieldSpec.Args.Inputs(srv.View), nil),
		})
		parentType = fieldSpec.Type.Type()
	}

	// Normalize names so case variants cannot evade cycle detection during
	// nested module construction.
	normalized := moduleField + ":" + strings.Join(fields, ":")
	chain, _ := ctx.Value(moduleRefCycleKey{}).([]string)
	for _, seen := range chain {
		if seen == normalized {
			return fmt.Errorf("module reference cycle detected: %s -> %s",
				strings.Join(chain, " -> "), normalized)
		}
	}
	newChain := make([]string, len(chain)+1)
	copy(newChain, chain)
	newChain[len(chain)] = normalized
	ctx = context.WithValue(ctx, moduleRefCycleKey{}, newChain)

	if err := srv.Select(ctx, root, dest, selectors...); err != nil {
		return fmt.Errorf("resolve module reference %q (module %q): %w", addr, module, err)
	}
	return nil
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
	q, ws, cfg, ok := currentWorkspaceConfig(ctx)
	if !ok {
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
	want := strcase.ToKebab(name)
	for installedName := range cfg.Modules {
		if strcase.ToKebab(installedName) == want {
			installed = true
			break
		}
	}
	if !installed {
		return nil, false, nil
	}
	// Strict (non-best-effort) load: the consumer's constructor requires this
	// module, so a load failure is that resolution's real error. This does not
	// undo best-effort operations like `dagger generate`: their own initial
	// best-effort pass records a failed module, EnsureWorkspaceModules returns
	// the recorded error here without reloading, and ModTree runs nodes
	// without fail-fast — so only the node that genuinely needs the broken
	// module fails, and repair generators keep running.
	if _, err := q.Server.EnsureWorkspaceModules(ctx, []string{name}, core.ModuleLoadStrict); err != nil {
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

// currentWorkspaceConfig returns the current query with its workspace and
// config, or ok=false when there is none (errors are deliberately discarded).
func currentWorkspaceConfig(ctx context.Context) (q *core.Query, ws *core.Workspace, cfg *workspace.Config, ok bool) {
	q, _ = core.CurrentQuery(ctx)
	if q == nil {
		return nil, nil, nil, false
	}
	ws, _ = q.Server.CurrentWorkspace(ctx)
	if ws == nil {
		return nil, nil, nil, false
	}
	cfg, _ = workspaceConfigWithCompatFallback(ctx, ws)
	if cfg == nil {
		return nil, nil, nil, false
	}
	return q, ws, cfg, true
}

// workspaceEntrypointModuleName returns the install name of the entrypoint module.
func workspaceEntrypointModuleName(ctx context.Context) (string, bool) {
	_, _, cfg, ok := currentWorkspaceConfig(ctx)
	if !ok {
		return "", false
	}
	for name, entry := range cfg.Modules {
		if entry.Entrypoint {
			return name, true
		}
	}
	return "", false
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
		dagql.Func("address", s.legacyAddress).
			View(BeforeVersion("v1.0.0-0")).
			Doc(`initialize an address to load directories, containers, secrets or other object types.`),
		dagql.Func("address", s.address).
			View(AfterVersion("v1.0.0-0")).
			Doc("Resolve external references only."),
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
		dagql.NodeFunc("volume", s.volume).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerCallInput).
			Doc(`Load a volume from the address.`),
		dagql.NodeFunc("workspace", s.workspace).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerCallInput).
			Doc(`Load a workspace from a module reference.`),
	}.Install(srv)
}

func (s *addressSchema) value(ctx context.Context, parent *core.Address, args struct{}) (string, error) {
	return parent.Value, nil
}

func (s *addressSchema) address(ctx context.Context, root *core.Query, args struct {
	Value dagql.String
},
) (*core.Address, error) {
	return newAddress(args.Value.String())
}

func newAddress(addr string) (*core.Address, error) {
	if addr == "" {
		return nil, fmt.Errorf("resource cannot have empty address")
	}
	return &core.Address{Value: addr, ExternalOnly: true}, nil
}

func (s *addressSchema) legacyAddress(ctx context.Context, root *core.Query, args struct{ Value dagql.String }) (*core.Address, error) {
	addr, err := s.address(ctx, root, args)
	if err == nil {
		addr.ExternalOnly = false
	}
	return addr, err
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
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
		return inst, err
	}
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
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
		return inst, err
	}
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
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
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
		if isBareRefShaped(addr) && (!r.Self().ExternalOnly || r.Self().BoundWorkspace.Self() != nil) {
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
	if r.Self().BoundWorkspace.Self() != nil {
		if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
			return inst, err
		}
	}
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
	if r.Self().BoundWorkspace.Self() != nil {
		if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
			return inst, err
		}
	}
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
	if r.Self().BoundWorkspace.Self() != nil {
		if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
			return inst, err
		}
	}
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
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
		return inst, err
	}
	// wrapFallback annotates fallback URL/host-port parse failures for
	// bare-ref-shaped addresses (e.g. a mistyped "docusarus:serve") with a hint
	// pointing at dagger.toml. Kept consistent with the .container() decoder.
	wrapFallback := func(err error) error {
		if isBareRefShaped(addr) && (!r.Self().ExternalOnly || r.Self().BoundWorkspace.Self() != nil) {
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

func (s *addressSchema) workspace(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Workspace],
	err error,
) {
	addr := r.Self().Value
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
		return inst, err
	}
	return inst, fmt.Errorf("workspace address %q must reference an installed module as <module>:<function>", addr)
}

func (s *addressSchema) volume(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Volume],
	err error,
) {
	query, srv, err := currentRootQuery(ctx)
	if err != nil {
		return inst, err
	}
	if err := query.RequireMainClient(ctx); err != nil {
		return inst, err
	}

	addr := r.Self().Value
	if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
		return inst, err
	}

	u, err := url.Parse(addr)
	if err != nil {
		return inst, fmt.Errorf("parse volume address: %w", err)
	}
	if u.Scheme == "engine-volume" {
		parsed, err := parseEngineVolumeAddress(addr)
		if err != nil {
			return inst, err
		}
		argsList := []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(parsed.Name)},
		}
		if parsed.HasSubdir {
			argsList = append(argsList, dagql.NamedInput{
				Name:  "subdir",
				Value: dagql.Opt(dagql.NewString(parsed.Subdir)),
			})
		}
		err = srv.Select(ctx, srv.Root(), &inst, dagql.Selector{
			Field: "engineVolume",
			Args:  argsList,
		})
		return inst, err
	}
	if u.Scheme != "sshfs" {
		return inst, fmt.Errorf("unsupported volume address %q: must use sshfs:// or engine-volume://", addr)
	}

	parsed, err := parseSSHFSVolumeAddress(addr)
	if err != nil {
		return inst, err
	}

	privateKey, err := loadAddressSecret(ctx, srv, parsed.PrivateKeyAddr)
	if err != nil {
		return inst, fmt.Errorf("load volume privateKey secret: %w", err)
	}
	privateKeyID, err := privateKey.ID()
	if err != nil {
		return inst, fmt.Errorf("get volume privateKey ID: %w", err)
	}

	argsList := []dagql.NamedInput{
		{Name: "endpoint", Value: dagql.NewString(parsed.Endpoint)},
		{Name: "privateKey", Value: dagql.NewID[*core.Secret](privateKeyID)},
	}
	if parsed.KnownHostsAddr != "" {
		knownHosts, err := loadAddressSecret(ctx, srv, parsed.KnownHostsAddr)
		if err != nil {
			return inst, fmt.Errorf("load volume knownHosts secret: %w", err)
		}
		knownHostsID, err := knownHosts.ID()
		if err != nil {
			return inst, fmt.Errorf("get volume knownHosts ID: %w", err)
		}
		argsList = append(argsList, dagql.NamedInput{
			Name:  "knownHosts",
			Value: dagql.Opt(dagql.NewID[*core.Secret](knownHostsID)),
		})
	}
	if parsed.CacheKey != "" {
		argsList = append(argsList, dagql.NamedInput{
			Name:  "cacheKey",
			Value: dagql.Opt(dagql.NewString(parsed.CacheKey)),
		})
	}
	if parsed.InsecureSkipHostKeyCheck {
		argsList = append(argsList, dagql.NamedInput{
			Name:  "insecureSkipHostKeyCheck",
			Value: dagql.Boolean(true),
		})
	}

	err = srv.Select(ctx, srv.Root(), &inst, dagql.Selector{
		Field: "sshfsVolume",
		Args:  argsList,
	})
	if err != nil {
		return inst, err
	}
	return inst, nil
}

type engineVolumeAddress struct {
	Name      string
	Subdir    string
	HasSubdir bool
}

func parseEngineVolumeAddress(addr string) (engineVolumeAddress, error) {
	var parsed engineVolumeAddress
	u, err := url.Parse(addr)
	if err != nil {
		return parsed, fmt.Errorf("parse volume address: %w", err)
	}
	if u.Scheme != "engine-volume" {
		return parsed, fmt.Errorf("unsupported volume address %q: must use engine-volume://", addr)
	}
	if u.Opaque != "" || u.Host == "" {
		return parsed, fmt.Errorf("engine volume address must put the name after engine-volume://")
	}
	if u.User != nil {
		return parsed, fmt.Errorf("engine volume address must not include user information")
	}
	if u.Fragment != "" {
		return parsed, fmt.Errorf("volume address must not include a fragment")
	}
	if u.RawPath != "" {
		return parsed, fmt.Errorf("engine volume address name must not use percent encoding")
	}
	if u.ForceQuery {
		return parsed, fmt.Errorf("engine volume address must not include an empty query")
	}

	queryVals, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return parsed, fmt.Errorf("parse engine volume address query: %w", err)
	}
	if values, ok := queryVals["subdir"]; ok {
		if len(values) != 1 {
			return parsed, fmt.Errorf("engine volume address query parameter %q must be specified once", "subdir")
		}
		parsed.HasSubdir = true
		parsed.Subdir = values[0]
		delete(queryVals, "subdir")
	}
	if len(queryVals) > 0 {
		return parsed, fmt.Errorf("unsupported volume address query parameter %q", firstQueryKey(queryVals))
	}

	parsed.Name = u.Host + u.Path
	if err := core.ValidateEngineVolumeName(parsed.Name); err != nil {
		return engineVolumeAddress{}, err
	}
	if parsed.HasSubdir {
		if err := core.ValidateEngineVolumeSubdir(parsed.Subdir); err != nil {
			return engineVolumeAddress{}, err
		}
	}
	return parsed, nil
}

func currentRootQuery(ctx context.Context) (*core.Query, *dagql.Server, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, err
	}
	query, ok := dagql.UnwrapAs[*core.Query](srv.Root())
	if !ok {
		return nil, nil, fmt.Errorf("dagql root is not Query")
	}
	return query, srv, nil
}

type sshfsVolumeAddress struct {
	Endpoint                 string
	PrivateKeyAddr           string
	KnownHostsAddr           string
	CacheKey                 string
	InsecureSkipHostKeyCheck bool
}

func parseSSHFSVolumeAddress(addr string) (sshfsVolumeAddress, error) {
	var parsed sshfsVolumeAddress
	u, err := url.Parse(addr)
	if err != nil {
		return parsed, fmt.Errorf("parse volume address: %w", err)
	}
	if u.Scheme != "sshfs" {
		return parsed, fmt.Errorf("unsupported volume address %q: must use sshfs://", addr)
	}
	if u.Fragment != "" {
		return parsed, fmt.Errorf("volume address must not include a fragment")
	}
	queryVals := u.Query()

	parsed.PrivateKeyAddr = queryVals.Get("privateKey")
	if parsed.PrivateKeyAddr == "" {
		return parsed, fmt.Errorf("volume address missing privateKey query parameter")
	}
	queryVals.Del("privateKey")

	parsed.KnownHostsAddr = queryVals.Get("knownHosts")
	queryVals.Del("knownHosts")

	parsed.CacheKey = queryVals.Get("cacheKey")
	queryVals.Del("cacheKey")

	if raw := queryVals.Get("insecureSkipHostKeyCheck"); raw != "" {
		parsed.InsecureSkipHostKeyCheck, err = strconv.ParseBool(raw)
		if err != nil {
			return parsed, fmt.Errorf("parse insecureSkipHostKeyCheck: %w", err)
		}
	}
	queryVals.Del("insecureSkipHostKeyCheck")

	if len(queryVals) > 0 {
		return parsed, fmt.Errorf("unsupported volume address query parameter %q", firstQueryKey(queryVals))
	}

	// Query.sshfsVolume validates the SSHFS endpoint structure after the
	// address-only query parameters have been stripped.
	u.RawQuery = ""
	parsed.Endpoint = u.String()
	return parsed, nil
}

func (s *addressSchema) socket(
	ctx context.Context,
	r dagql.ObjectResult[*core.Address],
	args struct{},
) (
	inst dagql.ObjectResult[*core.Socket],
	err error,
) {
	if r.Self().BoundWorkspace.Self() != nil {
		if matched, err := resolveModuleRef(ctx, r.Self(), &inst); matched {
			return inst, err
		}
	}
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

func loadAddressSecret(ctx context.Context, srv *dagql.Server, addr string) (dagql.ObjectResult[*core.Secret], error) {
	var inst dagql.ObjectResult[*core.Secret]
	if err := srv.Select(ctx, srv.Root(), &inst,
		dagql.Selector{
			Field: "address",
			Args: []dagql.NamedInput{
				{Name: "value", Value: dagql.NewString(addr)},
			},
		},
		dagql.Selector{
			Field: "secret",
		},
	); err != nil {
		return inst, err
	}
	return inst, nil
}

func firstQueryKey(vals url.Values) string {
	keys := make([]string, 0, len(vals))
	for key := range vals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// workspaceModuleSchema resolves one module from the selected workspace,
// including sibling modules. It never borrows ambient modules from the caller.
func workspaceModuleSchema(ctx context.Context, ws dagql.ObjectResult[*core.Workspace], name string) (*dagql.Server, error) {
	cfg, err := workspaceEffectiveConfig(ctx, ws.Self())
	if err != nil {
		return nil, err
	}
	installedName := ""
	for candidate := range cfg.Modules {
		if strcase.ToLowerCamel(candidate) == strcase.ToLowerCamel(name) {
			installedName = candidate
			break
		}
	}
	var selected dagql.ObjectResult[*core.Module]
	if installedName != "" {
		mods, err := (&workspaceSchema{}).workspaceTargetModules(ctx, ws, []string{installedName})
		if err != nil {
			return nil, err
		}
		for _, mod := range mods {
			if mod.Self().Name() == installedName {
				selected = mod
				break
			}
		}
		if selected.Self() == nil {
			return nil, fmt.Errorf("workspace module %q was not loaded", installedName)
		}
	} else if !ws.Self().IsValueWorkspace() {
		// Explicit -m modules need not be in config. Inspect only modules already
		// served to this live workspace; frozen values must not borrow them.
		mods, err := currentWorkspacePrimaryModules(ctx)
		if err != nil {
			return nil, err
		}
		for _, mod := range mods {
			if strcase.ToLowerCamel(mod.Self().Name()) == strcase.ToLowerCamel(name) {
				selected = mod
				break
			}
		}
		if selected.Self() != nil {
			removed, err := workspaceRemovedModuleNames(ctx, ws.Self())
			if err != nil {
				return nil, err
			}
			if removed[canonicalOverlayModuleName(name)] {
				return nil, nil
			}
		}
	}
	if selected.Self() == nil {
		return nil, nil
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := query.DefaultDeps(ctx)
	if err != nil {
		return nil, err
	}
	return deps.Append(core.NewUserMod(selected)).Schema(ctx)
}

// workspaceShorthandModule matches entrypoint fields without running constructors.
func workspaceShorthandModule(ctx context.Context, ws dagql.ObjectResult[*core.Workspace], field string) (string, *dagql.Server, error) {
	// Read configuration even when an entrypoint is already served: invalid
	// receiver configuration is an error, not proof that a reference is absent.
	cfg, err := workspaceEffectiveConfig(ctx, ws.Self())
	if err != nil {
		return "", nil, err
	}
	entrypoints, err := workspaceEntrypointNames(ctx, ws.Self())
	if err != nil {
		return "", nil, err
	}
	servedEntrypoint := false
	for _, enabled := range entrypoints {
		servedEntrypoint = servedEntrypoint || enabled
	}
	if !servedEntrypoint {
		// A resolve-only query may not have loaded its configured entrypoint.
		// The existing module loader performs demand loading and arbitration.
		for name, entry := range cfg.Modules {
			entrypoints[name] = entry.Entrypoint
		}
	}
	names := make([]string, 0, len(entrypoints))
	for name, enabled := range entrypoints {
		if enabled {
			names = append(names, name)
		}
	}
	if !ws.Self().IsValueWorkspace() {
		// A served entrypoint outside config is an explicit -m extra. It keeps
		// precedence over ambient nominations, including after config edits.
		mods, err := currentWorkspacePrimaryModules(ctx)
		if err != nil {
			return "", nil, err
		}
		removed, err := workspaceRemovedModuleNames(ctx, ws.Self())
		if err != nil {
			return "", nil, err
		}
		var extras []string
		for _, mod := range mods {
			name := mod.Self().Name()
			if !entrypoints[name] || removed[canonicalOverlayModuleName(name)] {
				continue
			}
			if _, configured := cfg.Modules[name]; !configured {
				extras = append(extras, name)
			}
		}
		if len(extras) > 0 {
			names = extras
		}
	}
	sort.Strings(names)
	var matchedName string
	var matchedServer *dagql.Server
	for _, name := range names {
		srv, err := workspaceModuleSchema(ctx, ws, name)
		if err != nil {
			return "", nil, err
		}
		if srv == nil {
			continue
		}
		root := srv.Canonical().Root()
		constructor, exists := root.ObjectType().FieldSpec(strcase.ToLowerCamel(name), srv.View)
		if !exists {
			continue
		}
		obj, exists := srv.ObjectType(constructor.Type.Type().Name())
		if !exists {
			continue
		}
		if _, exists := obj.FieldSpec(strcase.ToLowerCamel(field), srv.View); !exists {
			continue
		}
		if matchedServer != nil {
			return "", nil, fmt.Errorf("ambiguous entrypoint field %q in %q and %q", field, matchedName, name)
		}
		matchedName, matchedServer = name, srv
	}
	return matchedName, matchedServer, nil
}

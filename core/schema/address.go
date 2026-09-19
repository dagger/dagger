package schema

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

// moduleRefCycleKey is the context key carrying the chain of in-flight
// artifact addresses, used to detect reference cycles.
type moduleRefCycleKey struct{}

// resolveModuleRef resolves a DAG address in the address's workspace. A value
// without the dag:// scheme keeps its external meaning: it is never looked up
// in the workspace. See hack/designs/collections-issue.md, section 4, "Scheme".
//
// typeName is the GraphQL type the caller loads; a different artifact type is
// an error. The legacy client schema keeps its <module>:<function> lookup.
func resolveModuleRef(ctx context.Context, address *core.Address, typeName string, dest any) (bool, error) {
	addr := address.Value
	ws := address.BoundWorkspace
	if !dagaddress.IsAddress(addr) {
		if strings.Contains(addr, "://") || ws.Self() != nil || address.ExternalOnly {
			return false, nil
		}
		return resolveLegacyModuleRef(ctx, addr, dest)
	}
	if ws.Self() == nil {
		return true, fmt.Errorf("resolve %q: a DAG address needs a workspace; use Workspace.resolve", addr)
	}
	parsed, err := dagaddress.Parse(addr)
	if err != nil {
		return true, err
	}
	ctx, err = withWorkspaceClientContext(ctx, ws.Self())
	if err != nil {
		return true, err
	}
	ctx = core.WorkspaceToContext(ctx, ws)
	artifact, err := resolveWorkspaceArtifact(ctx, ws, parsed, addr)
	if err != nil {
		return true, err
	}
	if err := artifact.AssertType(parsed.Types); err != nil {
		return true, fmt.Errorf("resolve %q: %w", addr, err)
	}
	if artifact.TypeName != typeName {
		return true, fmt.Errorf("resolve %q: artifact is a %s, not a %s", addr, artifact.TypeName, typeName)
	}

	// Detect cycles on the canonical address, so spellings of one artifact
	// cannot evade the guard during nested module construction.
	normalized, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return true, err
	}
	chain, _ := ctx.Value(moduleRefCycleKey{}).([]string)
	if slices.Contains(chain, normalized) {
		return true, fmt.Errorf("module reference cycle detected: %s -> %s", strings.Join(chain, " -> "), normalized)
	}
	newChain := make([]string, len(chain)+1)
	copy(newChain, chain)
	newChain[len(chain)] = normalized
	ctx = context.WithValue(ctx, moduleRefCycleKey{}, newChain)

	if err := artifact.Evaluate(ctx, dest); err != nil {
		return true, fmt.Errorf("resolve %q: %w", addr, err)
	}
	return true, nil
}

// resolveWorkspaceArtifact is Workspace.artifacts(include: [path]).filterUri(uri).one().
// The include pattern narrows module loading to the modules the path names.
func resolveWorkspaceArtifact(ctx context.Context, ws dagql.ObjectResult[*core.Workspace], parsed *dagaddress.Address, uri string) (*core.Artifact, error) {
	var include []string
	if parsed.Path != "" {
		include = []string{parsed.Path}
	}
	artifacts, err := (&workspaceSchema{}).collectArtifacts(ctx, ws, include)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", uri, err)
	}
	if err := checkArtifactAddressWorkspace(artifacts.Entries, parsed, uri); err != nil {
		return nil, err
	}
	// Apply the type assertion as a filter only to choose among several
	// matches, so a single artifact of another type reports the assertion.
	untyped := *parsed
	untyped.Types = nil
	selected, err := artifacts.FilterURI(&untyped)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", uri, err)
	}
	if len(selected.Entries) > 1 && len(parsed.Types) > 0 {
		selected = selected.FilterTypeNames(parsed.Types)
	}
	if len(selected.Entries) == 0 {
		return nil, fmt.Errorf("resolve %q: no artifact matches", uri)
	}
	artifact, err := selected.One()
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", uri, err)
	}
	return artifact, nil
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
	functionField := strcase.ToLowerCamel(rest)
	var functionSpec dagql.FieldSpec
	functionExists := false
	if objType, exists := srv.ObjectType(spec.Type.Type().Name()); exists {
		functionSpec, functionExists = objType.FieldSpec(functionField, srv.View)
	}
	if !qualified && !functionExists {
		return false, nil
	}
	var functionArgs []dagql.NamedInput
	if functionExists {
		functionArgs = core.WithBoundWorkspaceArgs(ctx, srv, functionSpec.Args.Inputs(srv.View), nil)
	}
	selectors := []dagql.Selector{
		{Field: moduleField, Args: core.WithBoundWorkspaceArgs(ctx, srv, spec.Args.Inputs(srv.View), nil)},
		{Field: functionField, Args: functionArgs},
	}

	// Normalize names so case variants cannot evade cycle detection during
	// nested module construction.
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

	if err := srv.Select(ctx, srv.Root(), dest, selectors...); err != nil {
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

// isBareRefShaped reports whether addr looks like a module reference in the
// old "<module>:<function>" form: exactly one ":", no "://", and no "/". A
// value without the dag:// scheme keeps its external meaning in v1, so a string
// that fails external decoding most often meant a workspace artifact; callers
// wrap the fallback error with moduleRefHint.
func isBareRefShaped(addr string) bool {
	if strings.Contains(addr, "://") || strings.Contains(addr, "/") {
		return false
	}
	return strings.Count(addr, ":") == 1
}

// moduleRefHint builds the near-miss hint appended to fallback errors for
// bare-ref-shaped addresses. Kept identical between the .service() and
// .container() decoders.
func moduleRefHint(address *core.Address) string {
	module, function, _ := strings.Cut(address.Value, ":")
	if address.BoundWorkspace.Self() == nil {
		return fmt.Sprintf("no installed module matches %q; check the [modules.X] keys in dagger.toml", module)
	}
	return fmt.Sprintf("if you meant to wire in another module's output, write it as a DAG address: dag://%s/%s", module, function)
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
	if matched, err := resolveModuleRef(ctx, r.Self(), "File", &inst); matched {
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
	if matched, err := resolveModuleRef(ctx, r.Self(), "Directory", &inst); matched {
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
	if matched, err := resolveModuleRef(ctx, r.Self(), "Container", &inst); matched {
		// A DAG address, or a matched legacy module reference, must not
		// fall through to image interpretation when evaluation fails.
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
		// failed may be a mistyped module reference. Use the same view-aware
		// hint as the .service() decoder.
		if isBareRefShaped(addr) && (!r.Self().ExternalOnly || r.Self().BoundWorkspace.Self() != nil) {
			return inst, fmt.Errorf("%w (%s)", err, moduleRefHint(r.Self()))
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
		if matched, err := resolveModuleRef(ctx, r.Self(), "GitRepository", &inst); matched {
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
		if matched, err := resolveModuleRef(ctx, r.Self(), "GitRef", &inst); matched {
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
		if matched, err := resolveModuleRef(ctx, r.Self(), "Secret", &inst); matched {
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
	// A DAG address, or a matched legacy module reference, must not
	// fall through to tcp:///udp:// interpretation when evaluation fails.
	if matched, err := resolveModuleRef(ctx, r.Self(), "Service", &inst); matched {
		return inst, err
	}
	// wrapFallback annotates fallback URL/host-port parse failures for
	// bare-ref-shaped addresses with the same view-aware hint as .container().
	wrapFallback := func(err error) error {
		if isBareRefShaped(addr) && (!r.Self().ExternalOnly || r.Self().BoundWorkspace.Self() != nil) {
			return fmt.Errorf("%w (%s)", err, moduleRefHint(r.Self()))
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
	if matched, err := resolveModuleRef(ctx, r.Self(), "Workspace", &inst); matched {
		return inst, err
	}
	return inst, fmt.Errorf("workspace address %q must be a DAG address such as dag://<module>/<function>", addr)
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
	if matched, err := resolveModuleRef(ctx, r.Self(), "Volume", &inst); matched {
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
		if matched, err := resolveModuleRef(ctx, r.Self(), "Socket", &inst); matched {
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

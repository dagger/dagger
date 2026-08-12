package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/iancoleman/strcase"
)

// WorkspaceRemoteRef is a git ref that denotes a workspace: a clone ref, an
// optional version, and the subdirectory the workspace root sits in.
type WorkspaceRemoteRef struct {
	CloneRef        string
	Version         string
	WorkspaceSubdir string
}

// ParseWorkspaceRemoteRef parses a remote workspace ref, in either the
// fragment form (`host/repo#ref:subdir`) or the legacy `@ref` form.
func ParseWorkspaceRemoteRef(ctx context.Context, remoteRef string) (WorkspaceRemoteRef, error) {
	// Fragment refs are parsed via the same git URL parser used by Address.*.
	if strings.Contains(remoteRef, "#") {
		gitURL, err := gitutil.ParseURL(remoteRef)
		if err != nil {
			return WorkspaceRemoteRef{}, err
		}
		version := ""
		subdir := "."
		if gitURL.Fragment != nil {
			version = gitURL.Fragment.Ref
			subdir = gitURL.Fragment.Subdir
		}
		workspaceSubdir, err := NormalizeWorkspaceRemoteSubdir(subdir)
		if err != nil {
			return WorkspaceRemoteRef{}, fmt.Errorf("invalid git subdir in workspace ref %q: %w", remoteRef, err)
		}
		return WorkspaceRemoteRef{
			CloneRef:        gitURL.Remote(),
			Version:         version,
			WorkspaceSubdir: workspaceSubdir,
		}, nil
	}

	// Preserve legacy @ref parsing semantics for existing workspace refs.
	parsedRef, err := ParseGitRefString(ctx, remoteRef)
	if err != nil {
		return WorkspaceRemoteRef{}, err
	}
	workspaceSubdir := "."
	if parsedRef.RepoRootSubdir != "/" && parsedRef.RepoRootSubdir != "." {
		workspaceSubdir = parsedRef.RepoRootSubdir
	}
	return WorkspaceRemoteRef{
		CloneRef:        parsedRef.SourceCloneRef,
		Version:         parsedRef.ModVersion,
		WorkspaceSubdir: workspaceSubdir,
	}, nil
}

// NormalizeWorkspaceRemoteSubdir cleans a workspace subdirectory and refuses
// one that escapes the repository.
func NormalizeWorkspaceRemoteSubdir(subdir string) (string, error) {
	if subdir == "" {
		return ".", nil
	}
	subdir = filepath.Clean(subdir)
	subdir = strings.TrimPrefix(subdir, string(filepath.Separator))
	if subdir == "" || subdir == "." {
		return ".", nil
	}
	if !filepath.IsLocal(subdir) {
		return "", fmt.Errorf("path points outside repository: %q", subdir)
	}
	return subdir, nil
}

// CloneWorkspaceGitTree resolves a clone ref at an optional version and
// returns its tree. Going through the ordinary git(url).head / .ref(name)
// selectors is what records the resolved commit in the workspace lockfile.
func CloneWorkspaceGitTree(
	ctx context.Context,
	dag *dagql.Server,
	cloneRef string,
	version string,
) (dagql.ObjectResult[*Directory], dagql.ObjectResult[*GitRef], error) {
	// Build the ref selector — use "head" if no version specified.
	refSelector := dagql.Selector{Field: "head"}
	if version != "" {
		refSelector = dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(version)}},
		}
	}

	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(cloneRef)},
			},
		},
		refSelector,
	)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, gitRef, fmt.Errorf("resolving repo ref: %w", err)
	}

	var tree dagql.ObjectResult[*Directory]
	err = dag.Select(ctx, gitRef, &tree,
		dagql.Selector{
			Field: "tree",
			Args: []dagql.NamedInput{
				{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			},
		},
	)
	if err != nil {
		return tree, gitRef, fmt.Errorf("cloning repo: %w", err)
	}
	return tree, gitRef, nil
}

// IncludeSource is what an [[include]] entry needs to be resolved: where the
// including config sits, how to read files next to it, and how to resolve a git
// ref. ReadRelative and StatRelative address paths relative to the including
// config's directory; both may be nil for a caller that can only resolve git
// includes.
type IncludeSource struct {
	Dag           *dagql.Server
	ReadRelative  func(ctx context.Context, relPath string) ([]byte, error)
	StatRelative  func(ctx context.Context, relPath string) (*Stat, error)
	WorkspaceRoot string
}

// DefaultIncludeConfigFile is appended to an include source that names a
// directory rather than a config file.
const DefaultIncludeConfigFile = workspace.ConfigFileName

// LoadIncludedConfig resolves one [[include]] source and returns the config it
// names, with everything that cannot cross the include boundary removed.
//
// The source addresses a config *file*, not a workspace: either a path relative
// to the including config's directory, or a git ref whose fragment names the
// file inside the repository. A source that names a directory gets
// dagger.toml appended.
//
// The caller merges the result underneath its own config. A git include
// resolves through the normal git selectors, so its commit lands in the
// consuming workspace's dagger.lock like any other git lookup.
func LoadIncludedConfig(
	ctx context.Context,
	source IncludeSource,
	include string,
) (_ *workspace.Config, rerr error) {
	if include == "" {
		return nil, nil
	}

	// The warning below is written to the caller's context, not the span's:
	// progress renderers surface top-level logs, and a notice about modules
	// that silently went missing has to be seen.
	callerCtx := ctx
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("including workspace config: %s", include))
	defer telemetry.EndWithCause(span, &rerr)

	var (
		cfg       *workspace.Config
		statFS    StatFS
		configDir string
		err       error
	)
	if IncludeSourceIsGit(include) {
		cfg, statFS, configDir, err = source.loadGitInclude(ctx, include)
	} else {
		cfg, statFS, configDir, err = source.loadLocalInclude(ctx, include)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace include %q: %w", include, err)
	}

	warnDroppedIncludedModules(callerCtx, include, dropLocalIncludedModules(ctx, statFS, configDir, cfg))

	return cfg, nil
}

// IncludeSourceIsGit reports whether an include source addresses a git
// repository rather than a path next to the including config.
//
// A git include needs a scheme (`https://…`) or a fragment (`host/repo#ref:path`)
// — without one there is no way to tell where the repository ends and the path
// to the config file begins. Everything else is a path, which makes a bare
// `common/dagger.toml` mean what it looks like.
func IncludeSourceIsGit(source string) bool {
	return strings.Contains(source, "#") || gitref.FastKindCheck(source, "") == gitref.KindGit
}

func (s IncludeSource) loadGitInclude(
	ctx context.Context,
	include string,
) (*workspace.Config, StatFS, string, error) {
	if s.Dag == nil {
		return nil, nil, "", fmt.Errorf("git includes cannot be resolved here")
	}

	parsedRef, err := ParseWorkspaceRemoteRef(ctx, include)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parsing git ref: %w", err)
	}

	tree, _, err := CloneWorkspaceGitTree(ctx, s.Dag, parsedRef.CloneRef, parsedRef.Version)
	if err != nil {
		return nil, nil, "", err
	}
	statFS := &DirectoryStatFS{Dir: tree}

	configPath := includeConfigPath(parsedRef.WorkspaceSubdir)
	data, err := DirectoryReadFile(ctx, tree, configPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, nil, "", err
	}
	return cfg, statFS, path.Dir(configPath), nil
}

func (s IncludeSource) loadLocalInclude(
	ctx context.Context,
	include string,
) (*workspace.Config, StatFS, string, error) {
	if s.ReadRelative == nil {
		return nil, nil, "", fmt.Errorf("local includes cannot be resolved here")
	}
	if filepath.IsAbs(include) {
		return nil, nil, "", fmt.Errorf("must be relative to the including config, not an absolute path")
	}

	configPath := includeConfigPath(include)
	data, err := s.ReadRelative(ctx, configPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, nil, "", err
	}

	var statFS StatFS
	if s.StatRelative != nil {
		statFS = includeRelativeStatFS{stat: s.StatRelative}
	}
	return cfg, statFS, path.Dir(configPath), nil
}

// includeConfigPath appends the default config filename to a source that names
// a directory, so `#:dagger/common` and `../common` reach the config inside.
func includeConfigPath(p string) string {
	p = path.Clean(filepath.ToSlash(p))
	if p == "." || p == "/" {
		return DefaultIncludeConfigFile
	}
	if path.Ext(p) == ".toml" {
		return p
	}
	return path.Join(p, DefaultIncludeConfigFile)
}

// includeRelativeStatFS adapts a caller's relative stat into the StatFS the
// module classifier wants.
type includeRelativeStatFS struct {
	stat func(ctx context.Context, relPath string) (*Stat, error)
}

func (fs includeRelativeStatFS) Stat(ctx context.Context, path string) (string, *Stat, error) {
	stat, err := fs.stat(ctx, path)
	if err != nil {
		return "", nil, err
	}
	return path, stat, nil
}

// dropLocalIncludedModules removes the module entries a workspace must not load
// through an include: the included config's own local modules. It returns the
// dropped entries, labelled the way the config spells them — including dotted
// `env.<name>.modules.<mod>` paths for env overlays.
//
// A local source in an included config means a directory next to *that* config,
// and resolving it as written would resolve it against the consuming workspace
// instead — a different module, or none. Addressing them properly is possible
// (an included git config's modules are reachable as refs into its repository)
// but is deliberately left for later; for now an include shares configuration,
// not code.
func dropLocalIncludedModules(
	ctx context.Context,
	statFS StatFS,
	configDir string,
	cfg *workspace.Config,
) []string {
	// The labels name config paths, so they cannot be used to match ports: the
	// module names the dropped entries denote are tracked separately.
	var dropped, droppedModules []string
	for name, entry := range cfg.Modules {
		if !includedSourceIsLocal(ctx, statFS, configDir, entry.Source) {
			continue
		}
		delete(cfg.Modules, name)
		dropped = append(dropped, name)
		droppedModules = append(droppedModules, name)
	}

	for envName, env := range cfg.Env {
		for moduleName, overlay := range env.Modules {
			_, installed := cfg.Modules[moduleName]
			switch {
			case includedSourceIsLocal(ctx, statFS, configDir, overlay.Source):
				// An overlay that installs a local module is the same
				// violation as a base entry that does.
				delete(env.Modules, moduleName)
				dropped = append(dropped, workspace.JoinConfigPath("env", envName, "modules", moduleName))
				if !installed {
					// Nothing installs the module any more, so a port
					// forwarding to it has nothing left to reach.
					droppedModules = append(droppedModules, moduleName)
				}
			case overlay.Source == "" && !installed:
				// Settings for a module that was just dropped configure
				// nothing. An overlay with its own remote source stands on its
				// own and stays.
				delete(env.Modules, moduleName)
			}
		}
		if len(env.Modules) == 0 {
			env.Modules = nil
		}
		cfg.Env[envName] = env
	}

	dropPortsForDroppedModules(cfg, droppedModules)
	sort.Strings(dropped)
	return dropped
}

// includedSourceIsLocal reports whether a module source in an included config
// addresses something next to that config.
//
// Classification is deliberately not ParseRefString: for an ambiguous ref that
// is not a local directory, that falls back to local when git endpoint
// discovery fails, which would drop a perfectly good remote module whenever the
// network is unavailable. A dot-free or dot/slash-prefixed source is
// unambiguously local; anything else is local only if it really is a directory
// next to the included config. The pin is deliberately ignored, because the
// module loader classifies with an empty pin too — passing it would let
// `source = "./ci", pin = "…"` masquerade as remote.
func includedSourceIsLocal(ctx context.Context, statFS StatFS, configDir, source string) bool {
	if source == "" {
		return false
	}
	switch gitref.FastKindCheck(source, "") {
	case gitref.KindLocal:
		return true
	case gitref.KindGit:
		return false
	}

	// Ambiguous: local only if the included config really has a directory
	// there. Statting where the config was written — not the consuming
	// workspace — is the whole point: `modules/foo.bar` must be judged where it
	// was written. Without a filesystem to ask, the ref keeps its remote
	// reading, which is what it looks like.
	if statFS == nil {
		return false
	}
	probe := source
	if !filepath.IsAbs(probe) {
		probe = path.Join(configDir, probe)
	}
	_, stat, err := statFS.Stat(ctx, probe)
	if err != nil {
		// A missing path is the common case for a remote ref; a stat that fails
		// for any other reason is still not evidence of a local module.
		return false
	}
	return stat.IsDir()
}

// dropPortsForDroppedModules removes included port mappings that forward to a
// module that no longer exists here. BackendService is a colon-joined service
// path whose first segment is the module's CLI-cased name.
func dropPortsForDroppedModules(cfg *workspace.Config, droppedNames []string) {
	if len(cfg.Ports) == 0 || len(droppedNames) == 0 {
		return
	}
	droppedModules := make(map[string]bool, len(droppedNames))
	for _, name := range droppedNames {
		droppedModules[strcase.ToKebab(name)] = true
	}
	for host, mapping := range cfg.Ports {
		module, _, _ := strings.Cut(mapping.BackendService, ":")
		if droppedModules[strcase.ToKebab(module)] {
			delete(cfg.Ports, host)
		}
	}
}

// warnDroppedIncludedModules reports dropped entries once per client and
// include source. It lives here rather than in the callers because the surfaces
// that most need it — `dagger workspace config` and the other config reads —
// connect with workspace module loading skipped entirely.
func warnDroppedIncludedModules(ctx context.Context, include string, dropped []string) {
	if len(dropped) == 0 || !shouldWarnDroppedIncludedModules(ctx, include) {
		return
	}

	msg := fmt.Sprintf(
		"Included config %s declares local modules that cannot be used here: %s. Only its remote modules are inherited.",
		include, strings.Join(dropped, ", "),
	)
	slog.Warn(msg, "include", include, "dropped", dropped)
	fmt.Fprintln(telemetry.GlobalWriter(ctx, ""), msg)
}

// shouldWarnDroppedIncludedModules dedups the warning, because every read path
// resolves the include and they would otherwise repeat the same line several
// times for one command. Scoping is per client rather than per session: a
// nested CLI shares its parent's session, so session scope would silence every
// command after the first. Without a store to dedup against, warn rather than
// risk swallowing the only warning a run gets.
func shouldWarnDroppedIncludedModules(ctx context.Context, include string) bool {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return true
	}
	seenKeys, err := query.TelemetrySeenKeyStore(ctx)
	if err != nil {
		return true
	}
	key := "workspace.include.dropped:" + include
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil && clientMetadata.ClientID != "" {
		key += ":" + clientMetadata.ClientID
	}
	return dagql.ShouldEmitTelemetry(ctx, seenKeys, key, false)
}

package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/iancoleman/strcase"
)

// IncludeSource is what an [[include]] entry needs to be resolved: where the
// including config sits inside its workspace, how to read files there, and how
// to resolve a git ref. ConfigDir and the paths handed to ReadWorkspaceFile are
// workspace-relative; the reader may be nil for a caller that can only resolve
// git includes.
type IncludeSource struct {
	Dag               *dagql.Server
	ConfigDir         string
	ReadWorkspaceFile func(ctx context.Context, wsPath string) ([]byte, error)
}

// resolveIncludePath turns an include source into a workspace-relative path,
// following the same rule as every other path a workspace resolves
// (resolveWorkspacePath): a leading "/" means the workspace root, anything else
// is relative — here to the directory of the config that declares the include,
// because that is what the path sits next to.
func (s IncludeSource) resolveIncludePath(source string) (string, error) {
	// filepath.ToSlash is a no-op on the engine reading this, so a path spelled
	// with Windows separators is normalized explicitly, as ResolveSDKManagedPath
	// and resolveWorkspacePath both do.
	clean := path.Clean(strings.ReplaceAll(source, `\`, "/"))
	var resolved string
	if path.IsAbs(clean) {
		resolved = strings.TrimPrefix(clean, "/")
	} else {
		resolved = path.Join(filepath.ToSlash(s.ConfigDir), clean)
	}
	resolved = path.Clean(resolved)
	if resolved == "" {
		resolved = "."
	}
	if resolved != "." && !filepath.IsLocal(filepath.FromSlash(resolved)) {
		return "", fmt.Errorf("%q escapes the workspace root", source)
	}
	return resolved, nil
}

// DefaultIncludeConfigFile is appended to an include source that names a
// directory rather than a config file.
const DefaultIncludeConfigFile = workspace.ConfigFileName

// ApplyIncludes layers cfg on top of the config it includes and returns what
// the workspace will actually use. Every path that builds an effective config
// goes through here, validation included, so a new one cannot resolve an
// include and then skip a step the others take.
//
// explicitKeys are the keys cfg's own file spells out, which is what tells an
// explicit `entrypoint = false` from an absent one; workspace.ExplicitConfigKeys
// reads them from the bytes cfg was parsed from. src is consulted only when cfg
// declares an include, so a caller that has none may pass the zero value rather
// than build a resolver it will not use.
func ApplyIncludes(
	ctx context.Context,
	src IncludeSource,
	cfg *workspace.Config,
	explicitKeys map[string]bool,
) (*workspace.Config, error) {
	if cfg != nil && len(cfg.Include) > 0 {
		if err := workspace.ValidateIncludes(cfg); err != nil {
			return nil, err
		}
		included, err := LoadIncludedConfig(ctx, src, cfg.Include[0].Source)
		if err != nil {
			return nil, err
		}
		cfg, err = workspace.MergeIncludedConfig(included, cfg, explicitKeys)
		if err != nil {
			return nil, err
		}
	}
	if err := workspace.ValidateEffectiveConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

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
		cfg *workspace.Config
		err error
	)
	if IncludeSourceIsGit(include) {
		cfg, err = source.loadGitInclude(ctx, include)
	} else {
		cfg, err = source.loadLocalInclude(ctx, include)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace include %q: %w", include, err)
	}

	warnDroppedIncludedModules(callerCtx, include, dropLocalIncludedModules(cfg))

	return cfg, nil
}

// IncludeSourceIsGit reports whether an include source addresses a git
// repository rather than a path next to the including config.
//
// This is workspace.IsLocalRef, the classifier every other ref in a workspace
// config is read with: only the segment ahead of the first separator can be a
// host, so common/base.toml is a path and github.com/acme/base@v1 is a ref,
// neither needing a scheme. Answering the same question the same way
// everywhere is worth more than a rule tuned for the file paths an include
// happens to favour — the one place they differ, a dotted filename directly
// beside the config, is spelled ./base.toml.
func IncludeSourceIsGit(source string) bool {
	return !workspace.IsLocalRef(source, "")
}

func (s IncludeSource) loadGitInclude(
	ctx context.Context,
	include string,
) (*workspace.Config, error) {
	if s.Dag == nil {
		return nil, fmt.Errorf("git includes cannot be resolved here")
	}

	parsedRef, err := ParseWorkspaceRemoteRef(ctx, include)
	if err != nil {
		if looksLikeSiblingConfigFile(include) {
			// The source read as a ref only because its first segment has a
			// dot, which is also true of a filename. Say so, since the file it
			// names is otherwise reported as a repository nobody can find.
			return nil, fmt.Errorf("parsing git ref: %w (a config file beside this one is spelled %q)", err, "./"+include)
		}
		return nil, fmt.Errorf("parsing git ref: %w", err)
	}

	tree, _, err := CloneWorkspaceGitTree(ctx, s.Dag, parsedRef.CloneRef, parsedRef.Version)
	if err != nil {
		return nil, err
	}

	configPath := includeConfigPath(parsedRef.WorkspaceSubdir)
	data, err := DirectoryReadFile(ctx, tree, configPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	return workspace.ParseConfig(data)
}

func (s IncludeSource) loadLocalInclude(
	ctx context.Context,
	include string,
) (*workspace.Config, error) {
	if s.ReadWorkspaceFile == nil {
		return nil, fmt.Errorf("path includes cannot be resolved here")
	}

	resolved, err := s.resolveIncludePath(include)
	if err != nil {
		return nil, err
	}
	configPath := includeConfigPath(resolved)
	data, err := s.ReadWorkspaceFile(ctx, configPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	return workspace.ParseConfig(data)
}

// looksLikeSiblingConfigFile reports whether a source that failed to parse as a
// git ref is more plausibly a file below the including config: nothing that
// only a ref carries — scheme, fragment, version — and a config file's name at
// the end. Deliberately narrower than the file rule includeConfigPath applies,
// so a genuine ref that happens to end in .git is never told it is a path.
func looksLikeSiblingConfigFile(source string) bool {
	return !strings.ContainsAny(source, "#@:") && path.Ext(source) == ".toml"
}

// includeConfigPath appends the default config filename to a source that names
// a directory, so a `…#main:dagger/common` fragment and `../common` both reach
// the config inside.
//
// Which one a source names is decided from its spelling — an extension means a
// file — because the two filesystems an include is read through, a local
// workspace and a cloned git tree, have no statting in common. That keeps a
// mistyped dagger.tml a file rather than a directory to look inside; a
// directory whose own name has an extension is reached by naming the config
// in it.
func includeConfigPath(p string) string {
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if p == "." || p == "/" {
		return DefaultIncludeConfigFile
	}
	if path.Ext(p) != "" {
		return p
	}
	return path.Join(p, DefaultIncludeConfigFile)
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
func dropLocalIncludedModules(cfg *workspace.Config) []string {
	// The labels name config paths, so they cannot be used to match ports: the
	// module names the dropped entries denote are tracked separately.
	var dropped, droppedModules []string
	for name, entry := range cfg.Modules {
		if !includedSourceIsLocal(entry.Source) {
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
			case includedSourceIsLocal(overlay.Source):
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
// This is workspace.IsLocalRef, the same classifier the module loader reaches
// through ResolveModuleEntrySource, and with the same empty pin: agreeing with
// it is the whole correctness criterion here, because anything this keeps is
// something the loader will then resolve. Passing the entry's own pin would
// disagree — any non-empty pin reads as git — and let `source = "./ci",
// pin = "…"` through to be resolved against the consuming workspace.
func includedSourceIsLocal(source string) bool {
	return source != "" && workspace.IsLocalRef(source, "")
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

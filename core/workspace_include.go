package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
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
		cfg     *workspace.Config
		address includedModuleAddresser
		err     error
	)
	if IncludeSourceIsGit(include) {
		cfg, address, err = source.loadGitInclude(ctx, include)
	} else {
		cfg, address, err = source.loadLocalInclude(ctx, include)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace include %q: %w", include, err)
	}

	warnDroppedIncludedModules(callerCtx, include, addressIncludedModules(address, cfg))

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
) (*workspace.Config, includedModuleAddresser, error) {
	if s.Dag == nil {
		return nil, nil, fmt.Errorf("git includes cannot be resolved here")
	}

	parsedRef, err := ParseWorkspaceRemoteRef(ctx, include)
	if err != nil {
		if looksLikeSiblingConfigFile(include) {
			// The source read as a ref only because its first segment has a
			// dot, which is also true of a filename. Say so, since the file it
			// names is otherwise reported as a repository nobody can find.
			return nil, nil, fmt.Errorf("parsing git ref: %w (a config file beside this one is spelled %q)", err, "./"+include)
		}
		return nil, nil, fmt.Errorf("parsing git ref: %w", err)
	}

	tree, gitRef, err := CloneWorkspaceGitTree(ctx, s.Dag, parsedRef.CloneRef, parsedRef.Version)
	if err != nil {
		return nil, nil, err
	}
	commit, err := includedGitCommit(gitRef)
	if err != nil {
		return nil, nil, err
	}

	configPath := includeConfigPath(parsedRef.WorkspaceSubdir)
	data, err := DirectoryReadFile(ctx, tree, configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, nil, err
	}
	return cfg, gitIncludedModuleAddresser(parsedRef.CloneRef, commit, path.Dir(configPath)), nil
}

func (s IncludeSource) loadLocalInclude(
	ctx context.Context,
	include string,
) (*workspace.Config, includedModuleAddresser, error) {
	if s.ReadWorkspaceFile == nil {
		return nil, nil, fmt.Errorf("path includes cannot be resolved here")
	}

	resolved, err := s.resolveIncludePath(include)
	if err != nil {
		return nil, nil, err
	}
	configPath := includeConfigPath(resolved)
	data, err := s.ReadWorkspaceFile(ctx, configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, nil, err
	}
	return cfg, pathIncludedModuleAddresser(path.Dir(configPath), s.ConfigDir), nil
}

// includedGitCommit is the commit an included config was read at. Every module
// re-addressed into its repository is pinned to it, so the config and the
// modules it names always come from one revision even if the branch moves
// mid-run — and a commit SHA short-circuits the lock lookup, so no per-module
// lock entry appears.
func includedGitCommit(gitRef dagql.ObjectResult[*GitRef]) (string, error) {
	if gitRef.Self() == nil || gitRef.Self().Ref == nil {
		return "", fmt.Errorf("resolving the included commit: no git ref")
	}
	commit := gitRef.Self().Ref.SHA
	if !gitutil.IsCommitSHA(commit) {
		return "", fmt.Errorf("resolving the included commit: %q is not a commit SHA", commit)
	}
	return commit, nil
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

// includedModuleAddresser re-addresses one local module source of an included
// config so that it names the same module from the consuming workspace, or
// reports that it cannot be named from here at all.
type includedModuleAddresser func(source string) (string, bool)

// addressIncludedModules makes the included config's own modules usable here,
// and removes the ones that cannot be. It returns what was removed, labelled
// the way the config spells it — including dotted `env.<name>.modules.<mod>`
// paths for env overlays.
//
// A local source in an included config names a directory next to *that* config,
// so resolving it as written would resolve it against the consuming workspace
// instead — a different module, or none. Re-addressing is what makes the
// primary monorepo shape work: a shared config installs the modules beside it,
// and the projects that include it get them.
func addressIncludedModules(address includedModuleAddresser, cfg *workspace.Config) []string {
	// The labels carry a reason and name config paths, so they can match
	// neither modules nor ports: what each set is keyed by is tracked
	// separately.
	var dropped []string
	droppedModules := map[string]bool{}
	readdressedModules := map[string]bool{}
	for name, entry := range cfg.Modules {
		// The as-sdk marker is what makes a bare runtime name a built-in
		// install rather than a directory, which is the same condition the
		// loader skips on.
		readdressed, verdict, why := addressIncludedSource(address, entry.Source, entry.AsSDK != nil)
		switch verdict {
		case includedSourceKeep:
		case includedSourceReaddress:
			// The pin described the source that was just replaced; what the new
			// ref resolves to is fixed by the ref itself.
			entry.Source = readdressed
			entry.Pin = ""
			cfg.Modules[name] = entry
			readdressedModules[name] = true
		case includedSourceDrop:
			delete(cfg.Modules, name)
			dropped = append(dropped, name+" ("+why+")")
			droppedModules[name] = true
		}
	}

	for envName, env := range cfg.Env {
		for moduleName, overlay := range env.Modules {
			_, installed := cfg.Modules[moduleName]
			// An overlay never carries as-sdk state, so a runtime name there is
			// an ordinary source.
			readdressed, verdict, why := addressIncludedSource(address, overlay.Source, false)
			switch {
			case verdict == includedSourceReaddress:
				overlay.Source = readdressed
				overlay.Pin = ""
				env.Modules[moduleName] = overlay
				// The overlay installs the module even where the base entry
				// could not be addressed, so a port forwarding to it still has
				// something to reach.
				delete(droppedModules, moduleName)
			case verdict == includedSourceDrop:
				// An overlay that installs a module with no address here is the
				// same problem as a base entry that does.
				delete(env.Modules, moduleName)
				dropped = append(dropped, workspace.JoinConfigPath("env", envName, "modules", moduleName)+" ("+why+")")
				if !installed {
					// Nothing installs the module any more, so a port
					// forwarding to it has nothing left to reach.
					droppedModules[moduleName] = true
				}
			case overlay.Source == "" && !installed:
				// Settings for a module that was just dropped configure
				// nothing. An overlay with its own source stands on its own.
				delete(env.Modules, moduleName)
			case overlay.Source == "" && readdressedModules[moduleName] && overlay.Pin != "":
				// A lone pin updates the base entry's pin, and that entry now
				// names something else entirely.
				overlay.Pin = ""
				env.Modules[moduleName] = overlay
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

type includedSourceVerdict int

const (
	// includedSourceKeep is a source that already addresses something outside
	// the included config's own tree: a remote ref, or nothing at all.
	includedSourceKeep includedSourceVerdict = iota
	// includedSourceReaddress is one of the included config's own modules,
	// nameable from the consuming workspace.
	includedSourceReaddress
	// includedSourceDrop is local to the included config and has no address
	// here.
	includedSourceDrop
)

// addressIncludedSource decides what becomes of one source string. It returns
// the re-addressed ref when the verdict is includedSourceReaddress, and why the
// source has no address here when it is includedSourceDrop.
//
// sdkInstall says the entry carries as-sdk state, which is what makes a bare
// runtime name a built-in install rather than a directory that happens to be
// called "go". The loader draws the line in the same place, so an entry without
// it is an ordinary path however it is spelled.
func addressIncludedSource(address includedModuleAddresser, source string, sdkInstall bool) (string, includedSourceVerdict, string) {
	if source == "" {
		return "", includedSourceKeep, ""
	}
	// Ahead of the path check, because "go@v1.2.3" carries a dot and so reads
	// as a ref rather than a path. The runtime resolves in-engine either way,
	// not by fetching, and the as-sdk state that gave the entry its purpose
	// does not survive the merge.
	if name, _, _ := strings.Cut(source, "@"); sdkInstall && sdkmeta.IsBuiltin(name) {
		return "", includedSourceDrop, "built-in SDK runtime"
	}
	if !includedSourceIsLocal(source) {
		return "", includedSourceKeep, ""
	}
	if readdressed, ok := address(source); ok {
		return readdressed, includedSourceReaddress, ""
	}
	return "", includedSourceDrop, "no address outside the config it came from"
}

// includedSourceIsLocal reports whether a module source in an included config
// addresses something next to that config.
//
// This is workspace.IsLocalRef, the same classifier the module loader reaches
// through ResolveModuleEntrySource, and with the same empty pin: agreeing with
// it is the whole correctness criterion here, because anything this leaves
// alone is something the loader will then resolve as written. Passing the
// entry's own pin would disagree — any non-empty pin reads as git — and let
// `source = "./ci", pin = "…"` through to be resolved against the consuming
// workspace.
func includedSourceIsLocal(source string) bool {
	return source != "" && workspace.IsLocalRef(source, "")
}

// gitIncludedModuleAddresser names an included config's own modules as refs
// into the repository the config came from, pinned to the commit it was read
// at: `modules/ci` beside a config at the repository root becomes
// `<clone-ref>/modules/ci@<commit>`. That is the same rewrite remote workspace
// selection already performs for a workspace loaded with -W.
func gitIncludedModuleAddresser(cloneRef, commit, configDir string) includedModuleAddresser {
	return func(source string) (string, bool) {
		treePath, ok := includedModulePath(configDir, source)
		if !ok {
			return "", false
		}
		if strings.ContainsAny(treePath, "@#") {
			// Both spell "version" in a ref, so a path carrying either would be
			// cut short by the parser and resolve somewhere else entirely.
			return "", false
		}
		return GitRefString(cloneRef, treePath, commit), true
	}
}

// pathIncludedModuleAddresser names an included config's own modules the way
// the consuming config would have written them itself. Both configs live in one
// workspace, so only what the path is relative to changes.
//
// The result is always dot-prefixed, because a bare rewritten path whose first
// segment carries a dot ("shared.v2/ci") would otherwise read back as a git
// ref — the very confusion between a path and a ref this exists to prevent.
func pathIncludedModuleAddresser(includedDir, consumerDir string) includedModuleAddresser {
	if consumerDir == "" {
		consumerDir = "."
	}
	return func(source string) (string, bool) {
		wsPath, ok := includedModulePath(includedDir, source)
		if !ok {
			return "", false
		}
		rel, err := filepath.Rel(filepath.FromSlash(consumerDir), filepath.FromSlash(wsPath))
		if err != nil {
			return "", false
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, ".") {
			rel = "./" + rel
		}
		return rel, true
	}
}

// includedModulePath turns a local module source in an included config into the
// path it names inside that config's own tree, or reports that it names nothing
// addressable from outside it.
func includedModulePath(configDir, source string) (string, bool) {
	// Normalize separators first: a path spelled on Windows reaches a Linux
	// engine with backslashes, and \\server\share would otherwise read as
	// relative and be rebased under the config's directory.
	source = strings.ReplaceAll(source, `\`, "/")
	if path.IsAbs(source) || filepath.IsAbs(source) {
		// An absolute path addresses the machine the config was authored on.
		// Nothing in a shared tree corresponds to it.
		return "", false
	}
	treePath := path.Clean(path.Join(filepath.ToSlash(configDir), source))
	if treePath == "." {
		// The tree's own root is a workspace, not a module.
		return "", false
	}
	if !filepath.IsLocal(filepath.FromSlash(treePath)) {
		// Escapes the tree it came from. GitRefString would quietly normalize
		// this to a root-level path, resolving the wrong module rather than
		// failing.
		return "", false
	}
	return treePath, true
}

// dropPortsForDroppedModules removes included port mappings that forward to a
// module that no longer exists here. BackendService is a colon-joined service
// path whose first segment is the module's CLI-cased name.
func dropPortsForDroppedModules(cfg *workspace.Config, droppedNames map[string]bool) {
	if len(cfg.Ports) == 0 || len(droppedNames) == 0 {
		return
	}
	kebab := make(map[string]bool, len(droppedNames))
	for name := range droppedNames {
		kebab[strcase.ToKebab(name)] = true
	}
	for host, mapping := range cfg.Ports {
		module, _, _ := strings.Cut(mapping.BackendService, ":")
		if kebab[strcase.ToKebab(module)] {
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
		"Included config %s declares modules that were left out, with the reason for each: %s.",
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

package core

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
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

// DefaultIncludeConfigFile is appended to an include source that names a
// directory rather than a config file.
const DefaultIncludeConfigFile = workspace.ConfigFileName

// ApplyIncludes layers cfg on top of the config it includes. Every path that
// builds an effective config goes through here, so a new one cannot resolve an
// include and then skip a step the others take. Completeness is checked later,
// by workspace.ValidateEffectiveConfig, once the user and env overlays that can
// still supply a source have applied.
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
	userOverlay *workspace.UserWorkspaceOverlay,
) (*workspace.Config, error) {
	if cfg != nil && len(cfg.Include) > 0 {
		if err := workspace.ValidateIncludes(cfg); err != nil {
			return nil, err
		}
		// What the include's entries apply to is decided against everything
		// that can install a module above it: the local config and the
		// user-level overrides.
		consumer, err := workspace.ApplyUserOverlay(cfg, userOverlay)
		if err != nil {
			return nil, err
		}
		included, err := LoadIncludedConfig(ctx, src, cfg.Include[0].Source, consumer)
		if err != nil {
			return nil, err
		}
		return workspace.MergeIncludedConfig(included, cfg, explicitKeys)
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
	consumer *workspace.Config,
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

	warnDroppedIncludedModules(callerCtx, include, addressIncludedModules(address, cfg, consumer))

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

	tree, gitRef, err := CloneWorkspaceGitTree(ctx, s.Dag, parsedRef)
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
	cfg, err := parseIncludedConfig(ctx, data, configPath)
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

	// The same rule as every other path in a workspace config: a leading "/"
	// is the workspace root, anything else is relative to the including config.
	resolved, err := workspace.ResolveSDKManagedPath(s.ConfigDir, include)
	if err != nil {
		return nil, nil, err
	}
	configPath := includeConfigPath(resolved)
	data, err := s.ReadWorkspaceFile(ctx, configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := parseIncludedConfig(ctx, data, configPath)
	if err != nil {
		return nil, nil, err
	}
	return cfg, pathIncludedModuleAddresser(path.Dir(configPath), s.ConfigDir), nil
}

// parseIncludedConfig rejects unknown fields the way loading a workspace's own
// config does. It is not ParseConfigAt: that also reconciles SDK scopes against
// the config's directory, and an included config's SDK declarations are never
// inherited.
func parseIncludedConfig(ctx context.Context, data []byte, configPath string) (*workspace.Config, error) {
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, err
	}
	warnings, err := workspace.CheckConfigFields(data, configPath)
	if err != nil {
		return nil, err
	}
	for _, warning := range warnings {
		slog.GlobalLogger(ctx, "dagger/workspace").Warn(warning)
	}
	return cfg, nil
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
//
// What dangles is decided against the consumer as well: a module the consumer
// installs keeps the settings, env overlays and ports the include gives it,
// even when the include's own source for it has no address here.
func addressIncludedModules(address includedModuleAddresser, cfg, consumer *workspace.Config) []string {
	// Being some SDK's provider is what makes a bare runtime name a built-in
	// install rather than a directory that happens to be called "go". Read
	// from the config the entries came from, since that is where the claim was
	// made — the merge discards these declarations straight afterwards.
	sdkProviders := make(map[string]bool, len(cfg.SDKs))
	for _, sdk := range cfg.SDKs {
		if sdk.Module != "" {
			sdkProviders[sdk.Module] = true
		}
	}
	consumerInstalls := func(env, name string) bool {
		if consumer == nil {
			return false
		}
		if consumer.Modules[name].Source != "" {
			return true
		}
		return env != "" && consumer.Env[env].Modules[name].Source != ""
	}

	var dropped []string
	rejected := map[string]bool{}
	readdressed := map[string]bool{}
	for name, entry := range cfg.Modules {
		source, verdict, why := addressIncludedSource(address, entry.Source, sdkProviders[name])
		switch {
		case verdict == includedSourceKeep:
			continue
		case verdict == includedSourceReaddress:
			// The pin described the source that was just replaced; what the new
			// ref resolves to is fixed by the ref itself.
			entry.Source, entry.Pin = source, ""
			readdressed[name] = true
		case consumerInstalls("", name):
			// The consumer's own source fills it in at the merge.
			entry.Source, entry.Pin = "", ""
		default:
			delete(cfg.Modules, name)
			dropped = append(dropped, name+" ("+why+")")
			rejected[name] = true
			// Where an env installs it, from either config, its settings still
			// apply there.
			envNames := slices.Collect(maps.Keys(cfg.Env))
			if consumer != nil {
				envNames = append(envNames, slices.Collect(maps.Keys(consumer.Env))...)
			}
			for _, envName := range envNames {
				if cfg.Env[envName].Modules[name].Source != "" || consumerInstalls(envName, name) {
					graftIncludedSettings(cfg, consumer, envName, name, entry.Settings)
				}
			}
			continue
		}
		cfg.Modules[name] = entry
	}

	for envName, env := range cfg.Env {
		for name, overlay := range env.Modules {
			_, base := cfg.Modules[name]
			installed := base || consumerInstalls(envName, name)
			// SDKs are not env-scoped, so a runtime name in an overlay is an
			// ordinary source.
			source, verdict, why := addressIncludedSource(address, overlay.Source, false)
			switch {
			case verdict == includedSourceReaddress:
				overlay.Source, overlay.Pin = source, ""
			case verdict == includedSourceDrop:
				// An overlay that installs a module with no address here is the
				// same problem as a base entry that does. Its settings still
				// apply to a module installed some other way.
				dropped = append(dropped, workspace.JoinConfigPath("env", envName, "modules", name)+" ("+why+")")
				rejected[name] = true
				if !installed {
					delete(env.Modules, name)
					continue
				}
				overlay.Source, overlay.Pin = "", ""
			case overlay.Source == "" && !installed:
				// Settings for a module that nothing installs configure nothing.
				delete(env.Modules, name)
				continue
			case overlay.Source == "" && readdressed[name]:
				// A lone pin updates the base entry's pin, and that entry now
				// names something else entirely.
				overlay.Pin = ""
			}
			env.Modules[name] = overlay
		}
		if len(env.Modules) == 0 {
			env.Modules = nil
		}
		cfg.Env[envName] = env
	}

	// A port is dropped only with a module nothing installs any more, whichever
	// env or layer would have installed it.
	for name := range rejected {
		if installedAnywhere(cfg, name) || consumer != nil && installedAnywhere(consumer, name) {
			delete(rejected, name)
		}
	}
	dropPortsForDroppedModules(cfg, rejected)
	sort.Strings(dropped)
	return dropped
}

// graftIncludedSettings carries an included module's base settings into one
// env overlay. It only fills keys nothing above the included base sets — the
// included env itself, or the consumer's base, user-level or env settings — so a
// grafted default never outranks a value someone wrote.
func graftIncludedSettings(cfg, consumer *workspace.Config, envName, name string, settings map[string]any) {
	if cfg.Env == nil {
		cfg.Env = map[string]workspace.EnvOverlay{}
	}
	env := cfg.Env[envName]
	if env.Modules == nil {
		env.Modules = map[string]workspace.EnvModuleOverlay{}
	}
	overlay := env.Modules[name]
	for key, value := range settings {
		if _, set := overlay.Settings[key]; set {
			continue
		}
		if consumer != nil {
			if _, set := consumer.Modules[name].Settings[key]; set {
				continue
			}
			if _, set := consumer.Env[envName].Modules[name].Settings[key]; set {
				continue
			}
		}
		if overlay.Settings == nil {
			overlay.Settings = map[string]any{}
		}
		overlay.Settings[key] = value
	}
	env.Modules[name] = overlay
	cfg.Env[envName] = env
}

// installedAnywhere reports whether cfg installs name in its base config or in
// any env.
func installedAnywhere(cfg *workspace.Config, name string) bool {
	if cfg.Modules[name].Source != "" {
		return true
	}
	for _, env := range cfg.Env {
		if env.Modules[name].Source != "" {
			return true
		}
	}
	return false
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
// sdkInstall says the included config names the entry as an SDK provider,
// which is what makes a bare runtime name a built-in install rather than a
// directory that happens to be called "go". The loader draws the line in the
// same place, so an entry without it is an ordinary path however it is spelled.
func addressIncludedSource(address includedModuleAddresser, source string, sdkInstall bool) (string, includedSourceVerdict, string) {
	if source == "" {
		return "", includedSourceKeep, ""
	}
	// Ahead of the path check, because "go@v1.2.3" carries a dot and so reads
	// as a ref rather than a path. The runtime resolves in-engine either way,
	// not by fetching, and the [sdks] declaration that gave the entry its
	// purpose is not inherited.
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
	return func(source string) (string, bool) {
		wsPath, ok := includedModulePath(includedDir, source)
		if !ok {
			return "", false
		}
		rel, err := workspace.SDKManagedPathFor(consumerDir, wsPath)
		if err != nil {
			return "", false
		}
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

package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
)

const initialWorkspaceConfig = `# Dagger workspace configuration
# Install modules with: dagger install <module>
# Example:
#   dagger install github.com/dagger/dagger/modules/wolfi

[modules]
`

func workspaceBuildkit(ctx context.Context) (*engineutil.Client, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine client: %w", err)
	}
	return bk, nil
}

func workspaceConfigDirectoryForWrite(ws *core.Workspace, here bool) string {
	if here {
		return cleanWorkspaceRelPath(ws.Cwd)
	}
	if ws.ConfigFile != "" {
		return filepath.Dir(cleanWorkspaceRelPath(ws.ConfigFile))
	}
	return "."
}

func workspaceConfigDirectory(ws *core.Workspace) (string, error) {
	configFile, err := workspaceConfigFile(ws)
	if err != nil {
		return "", err
	}
	return filepath.Dir(configFile), nil
}

func workspaceConfigFile(ws *core.Workspace) (string, error) {
	if ws.ConfigFile == "" {
		return "", fmt.Errorf("no dagger.toml found in workspace")
	}
	return cleanWorkspaceRelPath(ws.ConfigFile), nil
}

func workspaceSameConfigDirectory(ws *core.Workspace, configDir string) bool {
	selectedDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return false
	}
	return selectedDir == cleanWorkspaceRelPath(configDir)
}

func setWorkspaceConfigSelection(ws *core.Workspace, configDir string) {
	configDir = cleanWorkspaceRelPath(configDir)
	configFile := cleanWorkspaceRelPath(filepath.Join(configDir, workspace.ConfigFileName))
	ws.LockFile = cleanWorkspaceRelPath(filepath.Join(configDir, workspace.LockFileName))
	ws.ConfigFile = configFile
}

func cleanWorkspaceRelPath(p string) string {
	if p == "" || p == "." {
		return "."
	}
	return filepath.Clean(p)
}

func workspaceHostPath(ws *core.Workspace, rel ...string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	if err := requireLocalWorkspace(ws, "workspace host access"); err != nil {
		return "", err
	}

	parts := append([]string{ws.HostPath()}, rel...)
	return filepath.Join(parts...), nil
}

func readConfigBytes(ctx context.Context, ws *core.Workspace) ([]byte, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	configFile, err := workspaceConfigFile(ws)
	if err != nil {
		return nil, err
	}
	return readWorkspaceFileBytes(ctx, ws, configFile)
}

// readWorkspaceFileBytes reads a workspace-relative file whichever way this
// workspace is backed: a synthetic source directory, a host path (with any
// overlay edit taking precedence), or a remote rootfs.
func readWorkspaceFileBytes(ctx context.Context, ws *core.Workspace, wsPath string) ([]byte, error) {
	if rootfs, ok := ws.SourceDirectory(); ok && rootfs.Self() != nil {
		data, err := core.DirectoryReadFile(ctx, rootfs, wsPath)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return data, nil
	}

	if ws.HostPath() != "" {
		// Host overlay edits live only in the changeset's delta side (host
		// overlays store no full read root — see overlayEdit); untouched files
		// read straight from the host below.
		if deltaRoot, ok := ws.OverlayDeltaRoot(); ok && ws.OverlayPathTouched(wsPath) {
			data, err := core.DirectoryReadFile(ctx, deltaRoot, wsPath)
			if err != nil {
				return nil, fmt.Errorf("reading config: %w", err)
			}
			return data, nil
		}

		ctx, err := withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		hostPath, err := workspaceHostPath(ws, wsPath)
		if err != nil {
			return nil, err
		}
		bk, err := workspaceBuildkit(ctx)
		if err != nil {
			return nil, err
		}

		data, err := bk.ReadCallerHostFile(ctx, hostPath)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return data, nil
	}

	rootfs := ws.Rootfs()
	if rootfs.Self() == nil {
		return nil, fmt.Errorf("workspace has no host path or rootfs")
	}
	data, err := core.DirectoryReadFile(ctx, rootfs, wsPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return data, nil
}

// statWorkspaceFile stats a workspace-relative path, so an included config's
// module sources can be judged where they were written.
func statWorkspaceFile(ctx context.Context, ws *core.Workspace, wsPath string) (*core.Stat, error) {
	if rootfs, ok := ws.SourceDirectory(); ok && rootfs.Self() != nil {
		_, stat, err := (&core.DirectoryStatFS{Dir: rootfs}).Stat(ctx, wsPath)
		return stat, err
	}
	if ws.HostPath() != "" {
		ctx, err := withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		hostPath, err := workspaceHostPath(ws, wsPath)
		if err != nil {
			return nil, err
		}
		bk, err := workspaceBuildkit(ctx)
		if err != nil {
			return nil, err
		}
		_, stat, err := core.NewCallerStatFS(bk).Stat(ctx, hostPath)
		return stat, err
	}
	rootfs := ws.Rootfs()
	if rootfs.Self() == nil {
		return nil, fmt.Errorf("workspace has no host path or rootfs")
	}
	_, stat, err := (&core.DirectoryStatFS{Dir: rootfs}).Stat(ctx, wsPath)
	return stat, err
}

// readWorkspaceConfig returns the workspace's effective config: its own
// dagger.toml with any included config merged underneath. Every read surface
// goes through here, so what they report is what module loading uses.
//
// Writers deliberately do not: loadWorkspaceConfigForOverlay keeps parsing the
// raw local file, because a write must land in the file the user owns.
func readWorkspaceConfig(ctx context.Context, ws *core.Workspace) (*workspace.Config, error) {
	cfg, _, err := readEffectiveWorkspaceConfig(ctx, ws)
	return cfg, err
}

// readEffectiveWorkspaceConfig also returns the raw local config bytes, for
// callers that need to answer about the file itself as well.
func readEffectiveWorkspaceConfig(ctx context.Context, ws *core.Workspace) (*workspace.Config, []byte, error) {
	data, err := readConfigBytes(ctx, ws)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}

	cfg, err = applyWorkspaceIncludes(ctx, ws, cfg, data)
	if err != nil {
		return nil, nil, err
	}
	if err := workspace.ValidateEffectiveConfig(cfg); err != nil {
		return nil, nil, err
	}
	return cfg, data, nil
}

// applyWorkspaceIncludes merges the config's includes underneath it. data is
// the raw config bytes cfg was parsed from, which is what tells an explicitly
// set key from an absent one.
//
// The include resolves in the workspace owner's client context: a git lookup
// records its pin in that workspace's lockfile, which is the wrong one when the
// receiver is not the caller's own workspace, and a local include reads through
// that workspace's filesystem.
func applyWorkspaceIncludes(
	ctx context.Context,
	ws *core.Workspace,
	cfg *workspace.Config,
	data []byte,
) (*workspace.Config, error) {
	if cfg == nil || len(cfg.Include) == 0 {
		return cfg, nil
	}
	if err := workspace.ValidateIncludes(cfg); err != nil {
		return nil, err
	}

	if ws.ClientID != "" {
		clientCtx, err := withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		ctx = clientCtx
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	source, err := workspaceIncludeSource(ws, dag)
	if err != nil {
		return nil, err
	}
	included, err := core.LoadIncludedConfig(ctx, source, cfg.Include[0].Source)
	if err != nil {
		return nil, err
	}
	explicitKeys, err := workspace.ExplicitConfigKeys(data)
	if err != nil {
		return nil, err
	}
	return workspace.MergeIncludedConfig(included, cfg, explicitKeys)
}

// workspaceIncludeSource lets an include address a config in this workspace,
// whichever way the workspace reads its own files.
func workspaceIncludeSource(ws *core.Workspace, dag *dagql.Server) (core.IncludeSource, error) {
	configFile, err := workspaceConfigFile(ws)
	if err != nil {
		return core.IncludeSource{}, err
	}

	return core.IncludeSource{
		Dag:       dag,
		ConfigDir: filepath.Dir(configFile),
		ReadWorkspaceFile: func(ctx context.Context, wsPath string) ([]byte, error) {
			return readWorkspaceFileBytes(ctx, ws, wsPath)
		},
		StatWorkspaceFile: func(ctx context.Context, wsPath string) (*core.Stat, error) {
			return statWorkspaceFile(ctx, ws, wsPath)
		},
	}, nil
}

type configReadArgs struct {
	Key string `default:""`
}

func (s *workspaceSchema) configRead(
	ctx context.Context,
	parent *core.Workspace,
	args configReadArgs,
) (dagql.String, error) {
	if parent.ConfigFile == "" {
		if envName, ok := selectedWorkspaceEnv(ctx); ok {
			return "", fmt.Errorf("workspace env %q requires dagger.toml", envName)
		}
		result, err := workspace.ReadConfigValue(nil, args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil
	}

	// The include list is answered from the local file whatever else is layered
	// on: it is this workspace's own declaration, and every effective view (env,
	// user overlay, merged include) strips it, so reading it from one of those
	// would report "key is not set" — and would resolve the include just to
	// return something the local file already says.
	//
	// `include` reports one source per line rather than the raw array of
	// tables, matching how it is written: `config include <source>`.
	if isIncludeConfigKey(args.Key) {
		data, err := readConfigBytes(ctx, parent)
		if err != nil {
			return "", err
		}
		if args.Key == "include" {
			cfg, err := workspace.ParseConfig(data)
			if err != nil {
				return "", err
			}
			if len(cfg.Include) == 0 {
				return "", fmt.Errorf("key %q is not set", args.Key)
			}
			sources := make([]string, 0, len(cfg.Include))
			for _, include := range cfg.Include {
				sources = append(sources, include.Source)
			}
			return dagql.String(strings.Join(sources, "\n")), nil
		}
		result, err := workspace.ReadConfigValue(data, args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil
	}

	envName, envSelected := selectedWorkspaceEnv(ctx)
	overlay := parent.UserConfigOverlay()
	switch {
	case envSelected && !isExplicitEnvConfigKey(args.Key):
		// Env-scoped reads return the effective active config: base values
		// with the user-level overlay and the selected env applied, env
		// tables hidden.
		cfg, err := readWorkspaceConfig(ctx, parent)
		if err != nil {
			return "", err
		}

		effective, err := effectiveWorkspaceConfigBytes(parent, cfg, envName)
		if err != nil {
			return "", err
		}

		result, err := workspace.ReadConfigValue(effective, args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil

	case overlay != nil:
		// User-level overrides merge over the repo config for reads; env
		// tables stay visible (including user-added envs) since no env is
		// being applied here.
		cfg, err := readWorkspaceConfig(ctx, parent)
		if err != nil {
			return "", err
		}
		merged, err := workspace.ApplyUserOverlay(cfg, overlay)
		if err != nil {
			return "", err
		}

		result, err := workspace.ReadConfigValue(effectiveConfigBytes(merged), args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil
	}

	cfg, data, err := readEffectiveWorkspaceConfig(ctx, parent)
	if err != nil {
		return "", err
	}
	// An include turns reads into the effective view, the same way an env
	// selection or a user overlay already does. Without one the file is
	// returned verbatim, comments and formatting included.
	if len(cfg.Include) > 0 {
		data = effectiveConfigBytes(cfg)
	}

	result, err := workspace.ReadConfigValue(data, args.Key)
	if err != nil {
		return "", err
	}
	return dagql.String(result), nil
}

func isIncludeConfigKey(key string) bool {
	return key == "include" || strings.HasPrefix(key, "include.")
}

// effectiveConfigBytes serializes an effective config as a standalone snapshot:
// the blueprint that produced the inherited values is named in a comment rather
// than left as a live key. Keeping the key would make the output a config that
// inlines the blueprint's values *and* names the blueprint again underneath.
//
// Same reasoning as effectiveWorkspaceConfigBytes clearing Env: the layer is
// applied, so it should not be re-applicable.
func effectiveConfigBytes(cfg *workspace.Config) []byte {
	if cfg == nil {
		return nil
	}
	if len(cfg.Include) == 0 {
		return workspace.SerializeConfig(cfg)
	}

	snapshot := *cfg
	snapshot.Include = nil
	header := &strings.Builder{}
	for _, include := range cfg.Include {
		fmt.Fprintf(header, "# included: %s\n", include.Source)
	}
	header.WriteString("\n")
	return append([]byte(header.String()), workspace.SerializeConfig(&snapshot)...)
}

type workspaceConfigValueArgs struct {
	Key    string
	Value  string
	Values dagql.Optional[dagql.ArrayInput[dagql.String]]
	Here   bool `default:"false"`
}

type workspaceConfigKeyArgs struct {
	Key  string
	Here bool `default:"false"`
}

func selectedWorkspaceEnv(ctx context.Context) (string, bool) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || clientMetadata.WorkspaceEnv == nil || *clientMetadata.WorkspaceEnv == "" {
		return "", false
	}
	return *clientMetadata.WorkspaceEnv, true
}

func isExplicitEnvConfigKey(key string) bool {
	return key == "env" || strings.HasPrefix(key, "env.")
}

// effectiveWorkspaceConfigBytes serializes cfg with the workspace's user-level
// overlay and the selected env overlay (when envName is non-empty) applied.
// The merge order matches module loading: base config, then user-level
// overrides, then the selected environment.
func effectiveWorkspaceConfigBytes(ws *core.Workspace, cfg *workspace.Config, envName string) ([]byte, error) {
	applied, err := workspace.ApplyUserOverlay(cfg, ws.UserConfigOverlay())
	if err != nil {
		return nil, err
	}
	applied, err = workspace.ApplyEnvOverlay(applied, envName)
	if err != nil {
		return nil, err
	}
	applied.Env = nil
	return effectiveConfigBytes(applied), nil
}

// envScopedConfigKey maps a modules.<name>.settings.* key into the selected
// env's overlay storage. Under workspaceConfigInitIfMissing a missing env is
// created by the write (writing a setting is the gesture that creates an env);
// under workspaceConfigMustExist a missing env is rejected, so unsets keep
// requiring the env to exist.
func envScopedConfigKey(cfg *workspace.Config, envName, key string, policy workspaceConfigMutationPolicy) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("workspace env %q requires dagger.toml", envName)
	}
	if _, ok := cfg.Env[envName]; !ok && policy == workspaceConfigMustExist {
		return "", workspace.NewUndefinedEnvError(cfg, envName)
	}

	parts, err := workspace.SplitConfigPath(key)
	if err != nil {
		return "", err
	}
	if len(parts) < 4 || parts[0] != "modules" || parts[2] != "settings" {
		return "", fmt.Errorf("key %q cannot be set in env %q; only modules.<name>.settings.* is supported", key, envName)
	}

	moduleName := parts[1]
	if _, ok := cfg.Modules[moduleName]; !ok {
		// The module may be one the env itself adds, which only exists in the
		// overlay.
		if _, ok := cfg.Env[envName].Modules[moduleName]; !ok {
			return "", fmt.Errorf("workspace env %q cannot set settings for unknown module %q", envName, moduleName)
		}
	}

	return workspace.JoinConfigPath(append([]string{"env", envName}, parts...)...), nil
}

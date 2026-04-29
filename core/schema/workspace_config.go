package schema

import (
	"context"
	"fmt"
	"os"
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

func (s *workspaceSchema) workspaceInit(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	if parent.HostPath() == "" {
		return "", fmt.Errorf("workspace init is local-only")
	}

	configPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}
	configDir := filepath.Dir(configPath)

	if parent.Initialized {
		return "", fmt.Errorf("workspace already initialized at %s", configDir)
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return "", err
	}

	if err := ensureWorkspaceInitialized(ctx, bk, parent); err != nil {
		return "", fmt.Errorf("initialize workspace: %w", err)
	}

	return dagql.String(configDir), nil
}

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

type workspaceConfigMutationPolicy int

const (
	workspaceConfigMustExist workspaceConfigMutationPolicy = iota
	workspaceConfigInitIfMissing
)

// loadWorkspaceConfigForMutation is the single policy choke point for commands
// that need a writable workspace config. Future flags like --require-init should
// only need to change the policy passed here.
func loadWorkspaceConfigForMutation(
	ctx context.Context,
	ws *core.Workspace,
	policy workspaceConfigMutationPolicy,
) (*workspace.Config, bool, error) {
	if ws.HasConfig {
		cfg, err := readWorkspaceConfig(ctx, ws)
		return cfg, false, err
	}

	if policy == workspaceConfigMustExist {
		return nil, false, fmt.Errorf("no config.toml found in workspace")
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := ensureWorkspaceInitialized(ctx, bk, ws); err != nil {
		return nil, false, fmt.Errorf("initialize workspace: %w", err)
	}

	return &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}, true, nil
}

func ensureWorkspaceInitialized(ctx context.Context, bk *engineutil.Client, ws *core.Workspace) error {
	if ws.HasConfig {
		return nil
	}

	if err := exportConfigToHost(ctx, bk, ws, []byte(initialWorkspaceConfig)); err != nil {
		return err
	}

	if ws.Cwd != "" {
		ws.Path = filepath.Join(ws.Path, ws.Cwd)
		ws.Cwd = ""
	}
	ws.ConfigPath = filepath.Join(ws.Path, workspace.LockDirName, workspace.ConfigFileName)
	ws.Initialized = true
	ws.HasConfig = true
	return nil
}

func configHostPath(ws *core.Workspace) (string, error) {
	if !ws.HasConfig && ws.Cwd != "" {
		return workspaceHostPath(ws, ws.Cwd, workspace.LockDirName, workspace.ConfigFileName)
	}
	return workspaceHostPath(ws, workspace.LockDirName, workspace.ConfigFileName)
}

func workspaceHostPath(ws *core.Workspace, rel ...string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace has no host path")
	}

	parts := append([]string{ws.HostPath(), ws.Path}, rel...)
	return filepath.Join(parts...), nil
}

func exportConfigToHost(ctx context.Context, bk *engineutil.Client, ws *core.Workspace, config []byte) error {
	configPath, err := configHostPath(ws)
	if err != nil {
		return err
	}
	return exportWorkspaceFileToHost(ctx, bk, configPath, config)
}

func readConfigBytes(ctx context.Context, ws *core.Workspace) ([]byte, error) {
	if !ws.HasConfig {
		return nil, fmt.Errorf("no config.toml found in workspace")
	}

	configPath, err := configHostPath(ws)
	if err != nil {
		return nil, err
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return nil, err
	}

	data, err := bk.ReadCallerHostFile(ctx, configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return data, nil
}

func readWorkspaceConfig(ctx context.Context, ws *core.Workspace) (*workspace.Config, error) {
	data, err := readConfigBytes(ctx, ws)
	if err != nil {
		return nil, err
	}

	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	return cfg, nil
}

func writeWorkspaceConfig(ctx context.Context, ws *core.Workspace, cfg *workspace.Config) error {
	return writeWorkspaceConfigWithHints(ctx, ws, cfg, nil)
}

func writeWorkspaceConfigWithHints(
	ctx context.Context,
	ws *core.Workspace,
	cfg *workspace.Config,
	hints map[string][]workspace.ConstructorArgHint,
) error {
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	existingData, err := readConfigBytes(ctx, ws)
	if err != nil {
		return err
	}
	updated, err := workspace.UpdateConfigBytesWithHints(existingData, cfg, hints)
	if err != nil {
		return err
	}
	return writeConfigBytes(ctx, ws, updated)
}

func writeConfigBytes(ctx context.Context, ws *core.Workspace, data []byte) error {
	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return err
	}
	return exportConfigToHost(ctx, bk, ws, data)
}

type configReadArgs struct {
	Key string `default:""`
}

func (s *workspaceSchema) configRead(
	ctx context.Context,
	parent *core.Workspace,
	args configReadArgs,
) (dagql.String, error) {
	if envName, ok := selectedWorkspaceEnv(ctx); ok && !isExplicitEnvConfigKey(args.Key) {
		cfg, err := readWorkspaceConfig(ctx, parent)
		if err != nil {
			return "", err
		}

		effective, err := effectiveWorkspaceConfigBytes(cfg, envName)
		if err != nil {
			return "", err
		}

		result, err := workspace.ReadConfigValue(effective, args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil
	}

	data, err := readConfigBytes(ctx, parent)
	if err != nil {
		return "", err
	}

	result, err := workspace.ReadConfigValue(data, args.Key)
	if err != nil {
		return "", err
	}
	return dagql.String(result), nil
}

type configWriteArgs struct {
	Key   string
	Value string
}

func (s *workspaceSchema) configWrite(
	ctx context.Context,
	parent *core.Workspace,
	args configWriteArgs,
) (dagql.String, error) {
	if !parent.HasConfig {
		return "", fmt.Errorf("no config.toml found in workspace")
	}

	data, err := readConfigBytes(ctx, parent)
	if err != nil {
		return "", err
	}

	writeKey := args.Key
	if envName, ok := selectedWorkspaceEnv(ctx); ok && !isExplicitEnvConfigKey(args.Key) {
		cfg, err := workspace.ParseConfig(data)
		if err != nil {
			return "", err
		}
		writeKey, err = envScopedConfigKey(cfg, envName, args.Key)
		if err != nil {
			return "", err
		}
	}

	updated, err := workspace.WriteConfigValue(data, writeKey, args.Value)
	if err != nil {
		return "", err
	}

	if err := writeConfigBytes(ctx, parent, updated); err != nil {
		return "", err
	}

	return dagql.String(args.Value), nil
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

func effectiveWorkspaceConfigBytes(cfg *workspace.Config, envName string) ([]byte, error) {
	applied, err := workspace.ApplyEnvOverlay(cfg, envName)
	if err != nil {
		return nil, err
	}
	applied.Env = nil
	return workspace.SerializeConfig(applied), nil
}

func envScopedConfigKey(cfg *workspace.Config, envName, key string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("workspace env %q requires .dagger/config.toml", envName)
	}
	if _, ok := cfg.Env[envName]; !ok {
		return "", fmt.Errorf("workspace env %q is not defined", envName)
	}

	parts := strings.Split(key, ".")
	if len(parts) < 4 || parts[0] != "modules" || parts[2] != "settings" {
		return "", fmt.Errorf("key %q cannot be set in env %q; only modules.<name>.settings.* is supported", key, envName)
	}

	moduleName := parts[1]
	if _, ok := cfg.Modules[moduleName]; !ok {
		return "", fmt.Errorf("workspace env %q cannot set settings for unknown module %q", envName, moduleName)
	}

	return strings.Join(append([]string{"env", envName}, parts...), "."), nil
}

func exportWorkspaceFileToHost(ctx context.Context, bk *engineutil.Client, hostPath string, contents []byte) error {
	tmpFile, err := os.CreateTemp("", "workspace-file-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(contents); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := bk.LocalFileExport(ctx, tmpFile.Name(), filepath.Base(hostPath), hostPath, true); err != nil {
		return fmt.Errorf("export %s: %w", filepath.Base(hostPath), err)
	}
	return nil
}

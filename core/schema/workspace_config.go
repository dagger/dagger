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
	args struct {
		Here bool `default:"false"`
	},
) (dagql.String, error) {
	if parent.HostPath() == "" {
		return "", fmt.Errorf("workspace init is local-only")
	}
	if parent.CompatWorkspace() != nil {
		return "", fmt.Errorf("workspace is using legacy dagger.json config; run dagger migrate first")
	}

	configDirRel := workspaceConfigDirectoryForWrite(parent, args.Here)
	configPath, err := workspaceHostPath(parent, configDirRel, workspace.ConfigFileName)
	if err != nil {
		return "", err
	}
	configDir := filepath.Dir(configPath)

	if parent.ConfigFile != "" && workspaceSameConfigDirectory(parent, configDirRel) {
		return "", fmt.Errorf("workspace config already exists at %s", configDir)
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return "", err
	}

	if err := ensureWorkspaceInitialized(ctx, bk, parent, args.Here); err != nil {
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
	here bool,
) (*workspace.Config, bool, error) {
	if ws.ConfigFile != "" && (!here || workspaceSameConfigDirectory(ws, workspaceConfigDirectoryForWrite(ws, true))) {
		cfg, err := readWorkspaceConfig(ctx, ws)
		return cfg, false, err
	}

	if ws.CompatWorkspace() != nil {
		return nil, false, fmt.Errorf("workspace is using legacy dagger.json config; run dagger migrate first")
	}
	if policy == workspaceConfigMustExist {
		return nil, false, fmt.Errorf("no config.toml found in workspace")
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := ensureWorkspaceInitialized(ctx, bk, ws, here); err != nil {
		return nil, false, fmt.Errorf("initialize workspace: %w", err)
	}

	return &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}, true, nil
}

func ensureWorkspaceInitialized(ctx context.Context, bk *engineutil.Client, ws *core.Workspace, here bool) error {
	configDirRel := workspaceConfigDirectoryForWrite(ws, here)
	if ws.ConfigFile != "" && workspaceSameConfigDirectory(ws, configDirRel) {
		return nil
	}

	configPath, err := workspaceHostPath(ws, configDirRel, workspace.ConfigFileName)
	if err != nil {
		return err
	}
	if err := exportWorkspaceFileToHost(ctx, bk, configPath, []byte(initialWorkspaceConfig)); err != nil {
		return err
	}

	setWorkspaceConfigSelection(ws, configDirRel)
	return nil
}

func workspaceConfigDirectoryForWrite(ws *core.Workspace, here bool) string {
	if here {
		return cleanWorkspaceRelPath(filepath.Join(ws.Cwd, workspace.LockDirName))
	}
	if ws.ConfigFile != "" {
		return filepath.Dir(cleanWorkspaceRelPath(ws.ConfigFile))
	}
	return workspace.LockDirName
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
		return "", fmt.Errorf("no config.toml found in workspace")
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
	if ws.LockFile == "" {
		ws.LockFile = cleanWorkspaceRelPath(filepath.Join(configDir, workspace.LockFileName))
	}
	ws.ConfigFile = configFile
}

func cleanWorkspaceRelPath(p string) string {
	if p == "" || p == "." {
		return "."
	}
	return filepath.Clean(p)
}

func configHostPath(ws *core.Workspace) (string, error) {
	configFile, err := workspaceConfigFile(ws)
	if err != nil {
		return "", err
	}
	return workspaceHostPath(ws, configFile)
}

func workspaceHostPath(ws *core.Workspace, rel ...string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace has no host path")
	}

	parts := append([]string{ws.HostPath()}, rel...)
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
	Here  bool `default:"false"`
}

func (s *workspaceSchema) configWrite(
	ctx context.Context,
	parent *core.Workspace,
	args configWriteArgs,
) (dagql.String, error) {
	if _, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing, args.Here); err != nil {
		return "", err
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

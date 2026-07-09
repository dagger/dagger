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

	if rootfs, ok := ws.SourceDirectory(); ok && rootfs.Self() != nil {
		data, err := core.DirectoryReadFile(ctx, rootfs, configFile)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return data, nil
	}

	if ws.HostPath() != "" {
		ctx, err = withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		configPath, err := workspaceHostPath(ws, configFile)
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

	rootfs := ws.Rootfs()
	if rootfs.Self() == nil {
		return nil, fmt.Errorf("workspace has no host path or rootfs")
	}
	data, err := core.DirectoryReadFile(ctx, rootfs, configFile)
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

type configReadArgs struct {
	Key string `default:""`
}

func (s *workspaceSchema) configRead(
	ctx context.Context,
	parent *core.Workspace,
	args configReadArgs,
) (dagql.String, error) {
	if parent.ConfigFile == "" {
		result, err := workspace.ReadConfigValue(nil, args.Key)
		if err != nil {
			return "", err
		}
		return dagql.String(result), nil
	}

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

type workspaceConfigValueArgs struct {
	Key   string
	Value string
	Here  bool `default:"false"`
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
		return "", fmt.Errorf("workspace env %q requires dagger.toml", envName)
	}
	if _, ok := cfg.Env[envName]; !ok {
		return "", fmt.Errorf("workspace env %q is not defined", envName)
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
		return "", fmt.Errorf("workspace env %q cannot set settings for unknown module %q", envName, moduleName)
	}

	return workspace.JoinConfigPath(append([]string{"env", envName}, parts...)...), nil
}

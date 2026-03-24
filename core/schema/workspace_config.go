package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
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

func workspaceBuildkit(ctx context.Context) (*buildkit.Client, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}
	return bk, nil
}

func ensureWorkspaceInitialized(ctx context.Context, bk *buildkit.Client, ws *core.Workspace) error {
	if ws.HasConfig {
		return nil
	}

	if err := exportConfigToHost(ctx, bk, ws, []byte(initialWorkspaceConfig)); err != nil {
		return err
	}

	ws.ConfigPath = filepath.Join(ws.Path, workspace.LockDirName, workspace.ConfigFileName)
	ws.Initialized = true
	ws.HasConfig = true
	return nil
}

func configHostPath(ws *core.Workspace) (string, error) {
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

func exportConfigToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, config []byte) error {
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
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	return writeConfigBytes(ctx, ws, workspace.SerializeConfig(cfg))
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

	updated, err := workspace.WriteConfigValue(data, args.Key, args.Value)
	if err != nil {
		return "", err
	}

	if err := writeConfigBytes(ctx, parent, updated); err != nil {
		return "", err
	}

	return dagql.String(args.Value), nil
}

func exportWorkspaceFileToHost(ctx context.Context, bk *buildkit.Client, hostPath string, contents []byte) error {
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

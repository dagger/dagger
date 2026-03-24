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

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	if err := exportConfigToHost(ctx, bk, parent, []byte(initialWorkspaceConfig)); err != nil {
		return "", fmt.Errorf("initialize workspace: %w", err)
	}

	parent.ConfigPath = filepath.Join(parent.Path, workspace.LockDirName, workspace.ConfigFileName)
	parent.Initialized = true
	parent.HasConfig = true

	return dagql.String(configDir), nil
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

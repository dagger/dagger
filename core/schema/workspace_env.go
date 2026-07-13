package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) envList(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.Array[dagql.String], error) {
	if parent.ConfigFile == "" {
		return dagql.Array[dagql.String]{}, nil
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return nil, err
	}

	names := workspace.EnvNames(cfg)
	out := make(dagql.Array[dagql.String], len(names))
	for i, name := range names {
		out[i] = dagql.String(name)
	}
	return out, nil
}

type workspaceConfigEnvArgs struct {
	Name string
	Here bool `default:"false"`
}

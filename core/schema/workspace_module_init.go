package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceModuleInitArgs struct {
	Name      string
	SDK       string   `default:""`
	Source    string   `default:""`
	Include   []string `default:"[]"`
	Blueprint string   `default:""`
	SelfCalls bool     `default:"false"`
}

func (s *workspaceSchema) moduleInit(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceModuleInitArgs,
) (dagql.String, error) {
	if !parent.HasConfig {
		return "", fmt.Errorf("no config.toml found in workspace")
	}
	if args.Name == "" {
		return "", fmt.Errorf("module name is required")
	}
	if args.Blueprint != "" && args.SDK != "" {
		return "", fmt.Errorf("cannot specify both --sdk and --blueprint; use one or the other")
	}
	if args.SelfCalls && args.SDK == "" {
		return "", fmt.Errorf("cannot enable self-calls feature without specifying --sdk")
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return "", err
	}
	if _, exists := cfg.Modules[args.Name]; exists {
		return "", fmt.Errorf("module %q already exists in workspace config", args.Name)
	}

	modulePath, err := workspaceHostPath(parent, workspace.LockDirName, "modules", args.Name)
	if err != nil {
		return "", err
	}

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return "", err
	}
	cwd, err := bk.AbsPath(ctx, ".")
	if err != nil {
		return "", fmt.Errorf("cwd: %w", err)
	}
	relPath, err := filepath.Rel(cwd, modulePath)
	if err != nil {
		return "", fmt.Errorf("compute relative module path: %w", err)
	}

	if _, err := s.exportWorkspaceModule(ctx, relPath, args); err != nil {
		return "", err
	}

	cfg.Modules[args.Name] = workspace.ModuleEntry{
		Source: path.Join("modules", args.Name),
	}
	if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
		return "", err
	}

	configPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}

	msg := fmt.Sprintf("Created module %q at %s\nInstalled in %s", args.Name, modulePath, configPath)
	if args.Blueprint != "" {
		msg += fmt.Sprintf("\nUsing blueprint %s", args.Blueprint)
	}
	return dagql.String(msg), nil
}

func (s *workspaceSchema) exportWorkspaceModule(
	ctx context.Context,
	refPath string,
	args workspaceModuleInitArgs,
) (string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("dagql server: %w", err)
	}

	baseSelector := workspaceModuleInitSourceSelector(refPath)

	var configExists dagql.Boolean
	if err := srv.Select(ctx, srv.Root(), &configExists,
		baseSelector,
		dagql.Selector{Field: "configExists"},
	); err != nil {
		return "", fmt.Errorf("check module exists: %w", err)
	}
	if bool(configExists) {
		return "", fmt.Errorf("module %q already exists at %s", args.Name, refPath)
	}

	var contextDirPath dagql.String
	if err := srv.Select(ctx, srv.Root(), &contextDirPath,
		baseSelector,
		dagql.Selector{Field: "localContextDirectoryPath"},
	); err != nil {
		return "", fmt.Errorf("resolve local context directory: %w", err)
	}

	selectors := []dagql.Selector{
		baseSelector,
		{
			Field: "withName",
			Args: []dagql.NamedInput{{
				Name:  "name",
				Value: dagql.String(args.Name),
			}},
		},
	}

	if args.SDK != "" {
		selectors = append(selectors, dagql.Selector{
			Field: "withSDK",
			Args: []dagql.NamedInput{{
				Name:  "source",
				Value: dagql.String(args.SDK),
			}},
		})
	}
	if args.Source != "" {
		selectors = append(selectors, dagql.Selector{
			Field: "withSourceSubpath",
			Args: []dagql.NamedInput{{
				Name:  "path",
				Value: dagql.String(args.Source),
			}},
		})
	}
	if len(args.Include) > 0 {
		patterns := make(dagql.ArrayInput[dagql.String], len(args.Include))
		for i, include := range args.Include {
			patterns[i] = dagql.String(include)
		}
		selectors = append(selectors, dagql.Selector{
			Field: "withIncludes",
			Args: []dagql.NamedInput{{
				Name:  "patterns",
				Value: patterns,
			}},
		})
	}
	if args.Blueprint != "" {
		var blueprint dagql.ObjectResult[*core.ModuleSource]
		if err := srv.Select(ctx, srv.Root(), &blueprint,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(args.Blueprint)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			},
		); err != nil {
			return "", fmt.Errorf("load blueprint module: %w", err)
		}
		selectors = append(selectors, dagql.Selector{
			Field: "withBlueprint",
			Args: []dagql.NamedInput{{
				Name:  "blueprint",
				Value: dagql.NewID[*core.ModuleSource](blueprint.ID()),
			}},
		})
	}
	if args.SelfCalls {
		features := dagql.ArrayInput[core.ModuleSourceExperimentalFeature]{
			core.ModuleSourceExperimentalFeatureSelfCalls,
		}
		selectors = append(selectors, dagql.Selector{
			Field: "withExperimentalFeatures",
			Args: []dagql.NamedInput{{
				Name:  "features",
				Value: features,
			}},
		})
	}

	selectors = append(selectors,
		dagql.Selector{
			Field: "withEngineVersion",
			Args: []dagql.NamedInput{{
				Name:  "version",
				Value: dagql.String(modules.EngineVersionLatest),
			}},
		},
		dagql.Selector{Field: "generatedContextDirectory"},
		dagql.Selector{
			Field: "export",
			Args: []dagql.NamedInput{{
				Name:  "path",
				Value: contextDirPath,
			}},
		},
	)

	var exported string
	if err := srv.Select(ctx, srv.Root(), &exported, selectors...); err != nil {
		return "", fmt.Errorf("generate module: %w", err)
	}

	return string(contextDirPath), nil
}

func workspaceModuleInitSourceSelector(refPath string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(refPath)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
			{Name: "allowNotExists", Value: dagql.Boolean(true)},
			{Name: "requireKind", Value: dagql.Opt(core.ModuleSourceKindLocal)},
		},
	}
}

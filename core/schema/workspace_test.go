package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestWorkspacePrivateSourceFieldsAreNotGraphQLFields(t *testing.T) {
	typ := reflect.TypeOf(core.Workspace{})
	for _, name := range []string{"source", "rootfs", "hostPath", "ClientID"} {
		field, ok := typ.FieldByName(name)
		require.True(t, ok, "missing Workspace field %s", name)
		require.NotEqual(t, "true", field.Tag.Get("field"), "Workspace.%s must stay private", name)
	}
}

// TestInitialWorkspaceConfigOmitsCheckGenerated verifies the default dagger.toml
// (written by dagger install / workspace init) does not set check-generated: the
// engine never writes it by default, and an absent setting already behaves as
// check-generated = true.
func TestInitialWorkspaceConfigOmitsCheckGenerated(t *testing.T) {
	t.Parallel()

	require.Contains(t, initialWorkspaceConfig, "# Dagger workspace configuration")
	require.Contains(t, initialWorkspaceConfig, "[modules]")
	require.NotContains(t, initialWorkspaceConfig, "check-generated")

	cfg, err := workspace.ParseConfig([]byte(initialWorkspaceConfig))
	require.NoError(t, err)
	require.Nil(t, cfg.CheckGenerated)
}

func TestMatchWorkspaceInclude(t *testing.T) {
	ctx := context.Background()
	node := modTreeNode("go", "lint")

	t.Run("empty include matches everything", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, nil)
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("module-prefixed pattern matches", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"go:lint"})
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("wildcard module pattern matches", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"go:**"})
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("other module does not match", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"helm:**"})
		require.NoError(t, err)
		require.False(t, match)
	})
}

func TestWorkspaceConfigWithCompatFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("no config and no compat returns empty config", func(t *testing.T) {
		cfg, err := workspaceConfigWithCompatFallback(ctx, &core.Workspace{})
		require.NoError(t, err)
		require.Empty(t, cfg.Modules)
		require.Empty(t, cfg.Ports)
	})

	t.Run("compat workspace projects skips and port mappings", func(t *testing.T) {
		compat, err := workspace.ParseCompatWorkspace([]byte(`{
			"name": "app",
			"toolchains": [{
				"name": "hello-with-services",
				"source": "./hello-with-services",
				"ignoreServices": ["redis", "infra:database"],
				"portMappings": {
					"web": ["3000:80"]
				}
			}]
		}`))
		require.NoError(t, err)
		require.NotNil(t, compat)

		ws := &core.Workspace{}
		ws.SetCompatWorkspace(compat)
		cfg, err := workspaceConfigWithCompatFallback(ctx, ws)
		require.NoError(t, err)
		require.Equal(t, []string{"redis", "infra:database"}, cfg.Modules["hello-with-services"].Up.Skip)
		require.Equal(t, workspace.PortMapping{
			BackendService: "hello-with-services:web",
			BackendPort:    80,
		}, cfg.Ports["3000"])
	})
}

func TestWorkspaceConfigSkipPatterns(t *testing.T) {
	ctx := context.Background()

	t.Run("missing config has no skips", func(t *testing.T) {
		patterns, err := workspaceConfigSkipPatterns(ctx, &core.Workspace{}, func(entry workspace.ModuleEntry) []string {
			return entry.Generate.Skip
		})
		require.NoError(t, err)
		require.Empty(t, patterns)
	})

	t.Run("compat workspace uses projected skips", func(t *testing.T) {
		compat, err := workspace.ParseCompatWorkspace([]byte(`{
			"name": "app",
			"toolchains": [{
				"name": "hello-with-generators",
				"source": "./hello-with-generators",
				"ignoreGenerators": ["generate-other-files", "other-generators:*"]
			}]
		}`))
		require.NoError(t, err)
		require.NotNil(t, compat)

		ws := &core.Workspace{}
		ws.SetCompatWorkspace(compat)
		patterns, err := workspaceConfigSkipPatterns(ctx, ws, func(entry workspace.ModuleEntry) []string {
			return entry.Generate.Skip
		})
		require.NoError(t, err)
		require.Equal(t, map[string][]string{
			"hello-with-generators": {"generate-other-files", "other-generators:*"},
		}, patterns)
	})
}

func TestFilterGeneratorsByInclude(t *testing.T) {
	ctx := context.Background()
	generators := []*core.Generator{
		{Node: modTreeNode("hello-with-generators-java", "generate-files")},
		{Node: modTreeNode("hello-with-generators-java", "generate-other-files")},
	}

	t.Run("workspace-qualified patterns still match", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"hello-with-generators-java:generate-*"},
			false,
		)
		require.NoError(t, err)
		require.Len(t, filtered, 2)
	})

	t.Run("single generator module keeps legacy include semantics", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"generate-*"},
			true,
		)
		require.NoError(t, err)
		require.Len(t, filtered, 2)
	})

	t.Run("legacy include does not match without compat fallback", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"generate-*"},
			false,
		)
		require.NoError(t, err)
		require.Empty(t, filtered)
	})
}

func TestSelectVisibleGeneratorModules(t *testing.T) {
	names := func(entries []workspaceGeneratorModule) []string {
		result := make([]string, 0, len(entries))
		for _, entry := range entries {
			result = append(result, entry.name)
		}
		return result
	}

	t.Run("wrapper hides raw blueprint alias", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
			{name: "app", sourceDigest: "sha256:blueprint", isWrapper: true},
		})
		require.Equal(t, []string{"app"}, names(visible))
	})

	t.Run("single raw module remains visible", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
		})
		require.Equal(t, []string{"hello-with-generators"}, names(visible))
	})

	t.Run("multiple wrappers sharing one implementation remain visible", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
			{name: "app", sourceDigest: "sha256:blueprint", isWrapper: true},
			{name: "ci", sourceDigest: "sha256:blueprint", isWrapper: true},
		})
		require.Equal(t, []string{"app", "ci"}, names(visible))
	})
}

func TestResolveWorkspacePath(t *testing.T) {
	t.Run("relative path resolves from workspace cwd", func(t *testing.T) {
		got, err := resolveWorkspacePath("src", "services/payment")
		require.NoError(t, err)
		require.Equal(t, "services/payment/src", got)
	})

	t.Run("dot resolves to workspace cwd", func(t *testing.T) {
		got, err := resolveWorkspacePath(".", "services/payment")
		require.NoError(t, err)
		require.Equal(t, "services/payment", got)
	})

	t.Run("absolute path resolves from workspace root", func(t *testing.T) {
		got, err := resolveWorkspacePath("/shared/config", "services/payment")
		require.NoError(t, err)
		require.Equal(t, "shared/config", got)
	})

	t.Run("root absolute path resolves to workspace root", func(t *testing.T) {
		got, err := resolveWorkspacePath("/", "services/payment")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("relative path cannot escape workspace root", func(t *testing.T) {
		got, err := resolveWorkspacePath("../../..", "services/payment")
		require.ErrorContains(t, err, "escapes workspace root", fmt.Sprintf("got %q instead of an error", got))
	})
}

func TestWorkspaceAPIPath(t *testing.T) {
	t.Run("boundary root is slash", func(t *testing.T) {
		require.Equal(t, "/", workspaceAPIPath(""))
		require.Equal(t, "/", workspaceAPIPath("."))
	})

	t.Run("nested path is absolute from boundary", func(t *testing.T) {
		require.Equal(t, "/services/payment", workspaceAPIPath("services/payment"))
	})
}

func TestWorkspaceRootfsRequiresDirectory(t *testing.T) {
	_, err := workspaceRootfs(&core.Workspace{})
	require.ErrorContains(t, err, "workspace has no root filesystem")
}

func TestWorkspaceMigrationWarningsKeepsGapWarningsAggregated(t *testing.T) {
	plan := &workspace.MigrationPlan{
		Warnings: []string{
			"old setting one",
			"old setting two",
		},
		MigrationGapCount:   2,
		MigrationReportPath: ".dagger/migration-report.md",
	}

	appendWorkspaceMigrationNonGapWarnings(plan, []string{"hint warning"})

	require.Equal(t, []string{
		"hint warning",
		"2 old setting(s) need review; see .dagger/migration-report.md",
	}, workspaceMigrationWarnings(plan))
}

func TestWorkspaceConfigUsesMigratedModuleSourcesResolvesFromConfigDir(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"api": {Source: filepath.Join(workspace.LockDirName, "modules", "api")},
		},
	}

	require.True(t, workspaceConfigUsesMigratedModuleSources(cfg, "."))
	require.False(t, workspaceConfigUsesMigratedModuleSources(cfg, workspace.LockDirName))
}

func TestWorkspaceMigrationRootPath(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	ws := &core.Workspace{}
	ws.SetHostPath(root)
	plan := &workspace.MigrationPlan{
		ProjectRoot: filepath.Join(root, "services", "api"),
	}

	got, err := workspaceMigrationRootPath(ws, plan, workspace.ConfigFileName)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("services", "api", workspace.ConfigFileName), got)
}

func TestWorkspaceMigrationParentPlans(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	ws := &core.Workspace{}
	ws.SetHostPath(root)

	t.Run("plain module outside migrated workspaces creates root parent with SDK module", func(t *testing.T) {
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, nil)
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.Equal(t, root, plans[0].ProjectRoot)
		cfg, err := workspace.ParseConfig(plans[0].WorkspaceConfigData)
		require.NoError(t, err)
		require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
		require.Equal(t, []string{
			"modules/video requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m modules/video ...`.",
		}, plans[0].Warnings)
		require.Equal(t, filepath.Join(workspace.LockDirName, "migration-report.md"), plans[0].MigrationReportPath)
		require.Contains(t, string(plans[0].MigrationReportData), "## modules/video requires explicit loading")
		require.Contains(t, string(plans[0].MigrationReportData), "**This works**: `dagger -m modules/video call --help`")
		require.Contains(t, string(plans[0].MigrationReportData), "**This no longer works**: `cd modules/video; dagger call --help`")
	})

	t.Run("plain root module creates root parent with SDK module", func(t *testing.T) {
		plain := testRuntimeCompatWorkspace(t, root, `{
  "name": "myapp",
  "sdk": {"source": "go"}
}`)

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, nil)
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.Equal(t, root, plans[0].ProjectRoot)
		cfg, err := workspace.ParseConfig(plans[0].WorkspaceConfigData)
		require.NoError(t, err)
		require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
		require.Equal(t, []string{
			"Root module requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m . ...`.",
		}, plans[0].Warnings)
		require.Equal(t, filepath.Join(workspace.LockDirName, "migration-report.md"), plans[0].MigrationReportPath)
		require.Contains(t, string(plans[0].MigrationReportData), "## Root module requires explicit loading")
		require.Contains(t, string(plans[0].MigrationReportData), "**This works**: `dagger -m . call --help`")
		require.Contains(t, string(plans[0].MigrationReportData), "**This no longer works**: `dagger call --help`")
	})

	t.Run("plain module under migrated workspace installs SDK in migrated workspace", func(t *testing.T) {
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api", "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot:         filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		}

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		require.Empty(t, plans)
		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
		require.Equal(t, []string{
			"services/api/modules/video requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m services/api/modules/video ...`.",
		}, workspaceMigrationWarnings(migrated))
		require.Equal(t, filepath.Join(workspace.LockDirName, "migration-report.md"), migrated.MigrationReportPath)
		require.Contains(t, string(migrated.MigrationReportData), "## services/api/modules/video requires explicit loading")
	})

	t.Run("plain module beside migrated workspace creates root parent", func(t *testing.T) {
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot:         filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		}

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.Equal(t, root, plans[0].ProjectRoot)
	})

	t.Run("parent SDK modules are deduped", func(t *testing.T) {
		video := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)
		audio := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "audio"), `{
  "name": "audio",
  "sdk": {"source": "go"}
}`)

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{video, audio}, nil)
		require.NoError(t, err)
		require.Len(t, plans, 1)
		cfg, err := workspace.ParseConfig(plans[0].WorkspaceConfigData)
		require.NoError(t, err)
		require.Len(t, cfg.Modules, 1)
		require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
	})

	t.Run("parent SDK module name conflicts get a stable alternate name", func(t *testing.T) {
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api", "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot: filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(`[modules.dagger-go-sdk]
source = "github.com/acme/custom-go-sdk"
`),
		}

		_, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/custom-go-sdk", cfg.Modules["dagger-go-sdk"].Source)
		require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk-runtime"].Source)
	})
}

func TestWorkspaceMigrationModuleConfigConversions(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")

	plain := testRuntimeCompatWorkspace(t, filepath.Join(root, ".dagger", "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"},
  "dependencies": [
    {"name": "dep", "source": "github.com/acme/dep@main", "pin": "sha256:abc"},
    {"name": "local", "source": "./local"}
  ]
}`)
	rootSDKOnly := testRuntimeCompatWorkspace(t, root, `{
  "name": "app",
  "sdk": {"source": "go"}
}`)
	workspaceConfig := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api"), `{
  "name": "api",
  "sdk": {"source": "go"},
  "source": "src"
}`)
	noSDKPlain, err := workspaceMigrationCompatWorkspaceForLegacyConfig([]byte(`{
  "name": "data"
}`), filepath.Join(root, "modules", "data", workspace.LegacyModuleConfigFileName))
	require.NoError(t, err)
	require.NotNil(t, noSDKPlain)

	conversions, err := workspaceMigrationModuleConfigConversions([]*workspace.CompatWorkspace{
		plain,
		rootSDKOnly,
		workspaceConfig,
		noSDKPlain,
	})
	require.NoError(t, err)
	require.Len(t, conversions, 2)
	require.Equal(t, filepath.Join(root, ".dagger", "modules", "video"), conversions[0].ProjectRoot)
	cfg, err := modules.ParseModuleConfigForFilename(conversions[0].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "video", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "github.com/acme/dep@main", cfg.Dependencies[0].Source)
	require.Equal(t, "sha256:abc", cfg.Dependencies[0].Pin)
	require.Equal(t, "./local", cfg.Dependencies[1].Source)

	require.Equal(t, filepath.Join(root, "modules", "data"), conversions[1].ProjectRoot)
	cfg, err = modules.ParseModuleConfigForFilename(conversions[1].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "data", cfg.Name)
	require.Nil(t, cfg.SDK)
}

func TestWorkspaceMigrationLegacyLockProjectRootsIncludesModuleConfigConversions(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")

	require.Equal(t, []string{
		root,
		filepath.Join(root, "modules", "video"),
		filepath.Join(root, "modules", "data"),
	}, workspaceMigrationLegacyLockProjectRoots(workspaceMigrationPlanBundle{
		WorkspacePlans: []*workspace.MigrationPlan{
			{ProjectRoot: root},
		},
		ModuleConfigConversions: []workspaceMigrationModuleConfigConversion{
			{ProjectRoot: filepath.Join(root, "modules", "video")},
			{ProjectRoot: filepath.Join(root, "modules", "data")},
		},
	}))
}

func TestEnvScopedConfigKeyQuotesDynamicSegments(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"my.module": {Source: "modules/my.module"},
		},
		Env: map[string]workspace.EnvOverlay{
			"review env": {},
		},
	}

	key, err := envScopedConfigKey(cfg, "review env", `modules."my.module".settings."some.key"`)
	require.NoError(t, err)
	require.Equal(t, `env."review env".modules."my.module".settings."some.key"`, key)
}

func TestWorkspaceSettingConfigKeyQuotesDynamicSegments(t *testing.T) {
	require.Equal(t,
		`modules."my.module".settings."some.key"`,
		workspaceSettingConfigKey("my.module", "some.key"),
	)
}

func TestWorkspaceMigrationRootTargetPathsRejectsDuplicates(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	ws := &core.Workspace{}
	ws.SetHostPath(root)

	_, err := workspaceMigrationRootTargetPaths(ws, workspaceMigrationPlanBundle{
		WorkspacePlans: []*workspace.MigrationPlan{
			{ProjectRoot: root},
		},
		ParentPlans: []workspaceMigrationParentPlan{
			{ProjectRoot: root},
		},
	})
	require.ErrorContains(t, err, `migration target "dagger.toml" is planned more than once`)

	_, err = workspaceMigrationRootTargetPaths(ws, workspaceMigrationPlanBundle{
		WorkspacePlans: []*workspace.MigrationPlan{
			{
				ProjectRoot:              root,
				MigratedModuleConfigData: []byte("{}"),
				MigratedModuleConfigPath: workspace.ModuleConfigFileName,
			},
		},
		ModuleConfigConversions: []workspaceMigrationModuleConfigConversion{
			{ProjectRoot: root},
		},
	})
	require.ErrorContains(t, err, `migration target "dagger-module.toml" is planned more than once`)
}

func TestWorkspaceMigrationHiddenPath(t *testing.T) {
	require.True(t, workspaceMigrationHiddenPath(filepath.Join(".git", "hooks", workspace.LegacyModuleConfigFileName)))
	require.True(t, workspaceMigrationHiddenPath(filepath.Join("app", ".dagger", "modules", workspace.LegacyModuleConfigFileName)))
	require.False(t, workspaceMigrationHiddenPath(filepath.Join(workspace.LockDirName, "modules", "app", workspace.LegacyModuleConfigFileName)))
	require.True(t, workspaceMigrationHiddenPath(filepath.Join(workspace.LockDirName, "modules", "app", ".hidden", workspace.LegacyModuleConfigFileName)))
	require.True(t, workspaceMigrationHiddenPath(filepath.Join("app", ".hidden", workspace.LegacyModuleConfigFileName)))
	require.False(t, workspaceMigrationHiddenPath(filepath.Join("app", "modules", workspace.LegacyModuleConfigFileName)))
}

func TestWorkspaceMigrationFilterLegacyLockDataRemovesModuleResolve(t *testing.T) {
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, workspace.LookupResult{
		Value:  "sha256:deadbeef",
		Policy: workspace.PolicyPin,
	}))
	require.NoError(t, lock.SetLookup("", workspaceMigrationLockModulesResolveOperation, []any{"github.com/acme/mod@main"}, workspace.LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: workspace.PolicyFloat,
	}))

	data, err := lock.Marshal()
	require.NoError(t, err)

	filteredData, err := workspaceMigrationFilterLegacyLockData(data)
	require.NoError(t, err)
	filtered, err := workspace.ParseLock(filteredData)
	require.NoError(t, err)

	container, ok, err := filtered.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, workspace.LookupResult{Value: "sha256:deadbeef", Policy: workspace.PolicyPin}, container)

	_, ok, err = filtered.GetLookup("", workspaceMigrationLockModulesResolveOperation, []any{"github.com/acme/mod@main"})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestWorkspaceMigrationUniqueSortedPaths(t *testing.T) {
	require.Equal(t, []string{
		"dagger.json",
		filepath.Join("modules", "api", "dagger.json"),
		filepath.Join("modules", "web", "dagger.json"),
	}, workspaceMigrationUniqueSortedPaths([]string{
		filepath.Join("modules", "web", "dagger.json"),
		"dagger.json",
		filepath.Join("modules", "api", "dagger.json"),
		"dagger.json",
		filepath.Join("modules", "web", "dagger.json"),
	}))
}

func TestWorkspaceMigrationHasExplicitConfigAncestor(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(string(filepath.Separator), "repo")
	configPath := filepath.Join(root, "services", "api", workspace.ConfigFileName)
	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		if filepath.Clean(path) != configPath {
			return "", nil, os.ErrNotExist
		}
		return filepath.Dir(path), &core.Stat{Name: filepath.Base(path)}, nil
	})

	got, err := workspaceMigrationHasExplicitConfigAncestor(ctx, statFS, root, filepath.Join(root, "services", "api", "modules", "child"))
	require.NoError(t, err)
	require.True(t, got)

	got, err = workspaceMigrationHasExplicitConfigAncestor(ctx, statFS, root, filepath.Join(root, "services", "other"))
	require.NoError(t, err)
	require.False(t, got)
}

func testRuntimeCompatWorkspace(t *testing.T, projectRoot string, data string) *workspace.CompatWorkspace {
	t.Helper()

	compatWorkspace, err := workspace.ParseRuntimeCompatWorkspaceAt([]byte(data), filepath.Join(projectRoot, workspace.LegacyModuleConfigFileName))
	require.NoError(t, err)
	require.NotNil(t, compatWorkspace)
	return compatWorkspace
}

func TestWorkspaceFilterWithDirectoryArgs(t *testing.T) {
	args := workspaceFilterWithDirectoryArgs(nil, core.CopyFilter{
		Include: []string{"app/**"},
		Exclude: []string{".git"},
	}, false)

	require.Len(t, args, 4)
	require.Equal(t, "path", args[0].Name)
	require.Equal(t, "source", args[1].Name)
	require.Equal(t, "include", args[2].Name)
	require.Equal(t, "exclude", args[3].Name)
	for _, arg := range args {
		require.NotEqual(t, "directory", arg.Name)
	}
}

func TestWorkspaceIgnoreForPath(t *testing.T) {
	t.Run("root keeps configured patterns", func(t *testing.T) {
		require.Equal(t,
			[]string{"ignored/**", "sub/generated/**", "*.log"},
			workspaceIgnoreForPath([]string{"ignored/**", "sub/generated/**", "*.log"}, "."),
		)
	})

	t.Run("subdirectory uses relative matching patterns", func(t *testing.T) {
		require.Equal(t,
			[]string{"generated/**", "*.log", "**/cache/**", "!keep.log", "*"},
			workspaceIgnoreForPath([]string{
				"sub/generated/**",
				"other/generated/**",
				"*.log",
				"**/cache/**",
				"!sub/keep.log",
				"sub",
			}, "sub"),
		)
	})

	t.Run("nested subdirectory trims workspace prefix", func(t *testing.T) {
		require.Equal(t,
			[]string{"*.pb.go"},
			workspaceIgnoreForPath([]string{"services/api/*.pb.go", "services/web/*.pb.go"}, filepath.Join("services", "api")),
		)
	})

	t.Run("ancestor directory ignore covers nested requests", func(t *testing.T) {
		require.Equal(t,
			[]string{"*", "*", "!keep.txt"},
			workspaceIgnoreForPath([]string{"sub", "sub/**", "!sub/nested/keep.txt"}, filepath.Join("sub", "nested")),
		)
	})
}

func TestWorkspaceFilterWithIgnore(t *testing.T) {
	ws := &core.Workspace{}
	ws.SetIgnorePatterns([]string{"sub/generated/**"})

	filter := workspaceFilterWithIgnore(ws, "sub", core.CopyFilter{
		Include: []string{"*.go"},
		Exclude: []string{"*.tmp"},
	})

	require.Equal(t, []string{"*.go"}, filter.Include)
	require.Equal(t, []string{"*.tmp", "generated/**"}, filter.Exclude)
}

func modTreeNode(parts ...string) *core.ModTreeNode {
	parent := &core.ModTreeNode{}
	for _, part := range parts {
		parent = &core.ModTreeNode{
			Parent: parent,
			Name:   part,
		}
	}
	return parent
}

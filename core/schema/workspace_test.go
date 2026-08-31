package schema

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspacePrivateSourceFieldsAreNotGraphQLFields(t *testing.T) {
	typ := reflect.TypeOf(core.Workspace{})
	for _, name := range []string{"source", "rootfs", "mounts", "mountPoints", "hostPath", "ClientID", "userConfigKey", "userConfigOverlay"} {
		field, ok := typ.FieldByName(name)
		require.True(t, ok, "missing Workspace field %s", name)
		require.NotEqual(t, "true", field.Tag.Get("field"), "Workspace.%s must stay private", name)
	}
}

// TestEffectiveWorkspaceConfigBytesAppliesUserOverlay verifies the schema-level
// effective-config path merges in the same order as module loading: base
// config, then the workspace's user-level overlay, then the selected env.
func TestEffectiveWorkspaceConfigBytesAppliesUserOverlay(t *testing.T) {
	t.Parallel()

	baseCfg := func() *workspace.Config {
		return &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"aws": {
					Source:   "github.com/dagger/aws",
					Settings: map[string]any{"profile": "shared", "region": "us-east-1"},
				},
			},
		}
	}
	ws := &core.Workspace{}
	ws.SetUserConfigOverlay(&workspace.UserWorkspaceOverlay{
		Modules: map[string]workspace.EnvModuleOverlay{
			"aws": {Settings: map[string]any{"profile": "alice-dev"}},
		},
		Env: map[string]workspace.EnvOverlay{
			"dev": {Modules: map[string]workspace.EnvModuleOverlay{
				"aws": {Settings: map[string]any{"region": "us-west-2"}},
			}},
		},
	})

	t.Run("without env", func(t *testing.T) {
		t.Parallel()
		data, err := effectiveWorkspaceConfigBytes(ws, baseCfg(), "")
		require.NoError(t, err)

		profile, err := workspace.ReadConfigValue(data, "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "alice-dev", profile)

		region, err := workspace.ReadConfigValue(data, "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", region)
	})

	t.Run("with user-defined env", func(t *testing.T) {
		t.Parallel()
		data, err := effectiveWorkspaceConfigBytes(ws, baseCfg(), "dev")
		require.NoError(t, err)

		region, err := workspace.ReadConfigValue(data, "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", region)
	})

	t.Run("no overlay leaves config unchanged", func(t *testing.T) {
		t.Parallel()
		data, err := effectiveWorkspaceConfigBytes(&core.Workspace{}, baseCfg(), "")
		require.NoError(t, err)

		profile, err := workspace.ReadConfigValue(data, "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "shared", profile)
	})
}

func TestWorkspaceMountedPath(t *testing.T) {
	ws := (&core.Workspace{}).
		WithMounted(dagql.ObjectResult[*core.Directory]{}, ".refs/notes.txt").
		WithMounted(dagql.ObjectResult[*core.Directory]{}, "deps/vendored")

	t.Run("at or under a mount point", func(t *testing.T) {
		cases := []struct {
			in      string
			mounted bool
		}{
			{".refs/notes.txt", true},
			{"deps/vendored", true},
			{"deps/vendored/lib/util.go", true},
			// parents of mount points are not themselves mounted
			{".refs", false},
			{"deps", false},
			{".", false},
			// prefix spoofing does not match
			{"deps/vendored-extra", false},
			{".refs/notes.txt.bak", false},
			{"src/main.go", false},
		}
		for _, tc := range cases {
			t.Run(tc.in, func(t *testing.T) {
				require.Equal(t, tc.mounted, ws.MountedPath(tc.in))
			})
		}
	})

	t.Run("mounts under", func(t *testing.T) {
		cases := []struct {
			in    string
			under bool
		}{
			{".", true},
			{".refs", true},
			{"deps", true},
			// mount points themselves have no mounts strictly below
			{"deps/vendored", false},
			{"src", false},
			{"deps/vendored-extra", false},
		}
		for _, tc := range cases {
			t.Run(tc.in, func(t *testing.T) {
				require.Equal(t, tc.under, ws.HasMountsUnder(tc.in))
			})
		}
	})

	t.Run("no mounts", func(t *testing.T) {
		empty := &core.Workspace{}
		require.False(t, empty.MountedPath("anything"))
		require.False(t, empty.HasMountsUnder("."))
	})
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

	// A path typed on Windows reaches the Linux engine spelled with
	// backslashes, where filepath reads the whole thing as one element: the
	// segments never separate, "\.." never collapses, and the escape guard
	// above never sees a "..".
	t.Run("windows separators", func(t *testing.T) {
		for _, tc := range []struct{ arg, base, want string }{
			{`src\gen`, "services/payment", "services/payment/src/gen"},
			{`..\shared`, "services/payment", "services/shared"},
			{`\shared\config`, "services/payment", "shared/config"},
		} {
			got, err := resolveWorkspacePath(tc.arg, tc.base)
			require.NoError(t, err)
			require.Equal(t, tc.want, got, tc.arg)
		}

		got, err := resolveWorkspacePath(`..\..\..`, "services/payment")
		require.ErrorContains(t, err, "escapes workspace root", fmt.Sprintf("got %q instead of an error", got))
	})
}

func TestWorkspacePathInOrLeadingToCwd(t *testing.T) {
	cwd := ".dagger/modules/myapp"

	for _, path := range []string{
		".dagger/modules/myapp",
		".dagger/modules/myapp/main.go",
		".dagger/",
		"./.dagger/",
		".dagger/modules/",
	} {
		require.True(t, workspacePathInOrLeadingToCwd(path, cwd), path)
	}

	for _, path := range []string{"README.md", ".dagger/modules/other"} {
		require.False(t, workspacePathInOrLeadingToCwd(path, cwd), path)
	}
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

func TestWorkspacePathRelativeToCwd(t *testing.T) {
	tests := []struct {
		name        string
		rootRelPath string
		cwd         string
		want        string
	}{
		{
			name:        "root cwd",
			rootRelPath: "dagger.toml",
			cwd:         ".",
			want:        "dagger.toml",
		},
		{
			name:        "nested cwd",
			rootRelPath: "app/dagger.toml",
			cwd:         "app/sub",
			want:        "../dagger.toml",
		},
		{
			name:        "selected workspace cwd",
			rootRelPath: "selected/dagger.toml",
			cwd:         "selected",
			want:        "dagger.toml",
		},
		{
			name:        "no path",
			rootRelPath: "",
			cwd:         "app/sub",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workspacePathRelativeToCwd(tt.rootRelPath, tt.cwd)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
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

	t.Run("plain module in a subdirectory gets no parent workspace", func(t *testing.T) {
		// Setup run from a module subdirectory migrates just the module: the
		// config converts in place and no workspace is created anywhere — no
		// runtime pin, no explicit-loading warning, no migration report.
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"}
}`)

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain}, nil)
		require.NoError(t, err)
		require.Empty(t, plans)
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
		sdk := cfg.Modules["dagger-go-sdk"]
		require.Equal(t, "go", sdk.Source)
		require.NotNil(t, sdk.AsSDK)
		require.Equal(t, []string{
			"Root module requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m . ...`.",
		}, plans[0].Warnings)
		require.Equal(t, filepath.Join(workspace.LockDirName, "migration-report.md"), plans[0].MigrationReportPath)
		require.Contains(t, string(plans[0].MigrationReportData), "## Root module requires explicit loading")
		require.Contains(t, string(plans[0].MigrationReportData), "**This works**: `dagger -m . call --help`")
		require.Contains(t, string(plans[0].MigrationReportData), "**This no longer works**: `dagger call --help`")
	})

	t.Run("subdirectory toolchains config installs SDK in the hoisted root plan", func(t *testing.T) {
		// A subdirectory config with toolchains hoists them into a dagger.toml
		// planned at the workspace root; its SDK runtime pin and the
		// explicit-loading warning land on that hoisted plan.
		moduleRepo := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api"), `{
  "name": "api",
  "sdk": {"source": "go"},
  "toolchains": [{"name": "tc", "source": "./tc"}]
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot:         root,
			ModuleProjectRoot:   filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		}

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{moduleRepo}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		require.Empty(t, plans)
		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		sdk := cfg.Modules["dagger-go-sdk"]
		require.Equal(t, "go", sdk.Source)
		require.NotNil(t, sdk.AsSDK)
		require.Equal(t, []string{
			"services/api requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m services/api ...`.",
		}, workspaceMigrationWarnings(migrated))
		require.Equal(t, filepath.Join(workspace.LockDirName, "migration-report.md"), migrated.MigrationReportPath)
		require.Contains(t, string(migrated.MigrationReportData), "## services/api requires explicit loading")
	})

	t.Run("project-style module at the root is not assigned a runtime pin", func(t *testing.T) {
		// A must-migrate config at the workspace root with its source in a
		// subdirectory installs the module into its own migrated workspace
		// config with the SDK recorded as-sdk; the parent-plan flow must leave
		// it alone entirely.
		project := testRuntimeCompatWorkspace(t, root, `{
  "name": "api",
  "sdk": {"source": "go"},
  "source": "src"
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot:         root,
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		}

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{project}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		require.Empty(t, plans)
		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		require.Empty(t, cfg.Modules)
		require.Empty(t, workspaceMigrationWarnings(migrated))
	})

	t.Run("subdirectory module without toolchains gets no runtime pin", func(t *testing.T) {
		// A subdirectory config that must migrate only because its source is
		// in a subdirectory is the module-only case: no workspace is planned
		// anywhere, so there is no runtime pin and no warning.
		project := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api"), `{
  "name": "api",
  "sdk": {"source": "go"},
  "source": "src"
}`)

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{project}, nil)
		require.NoError(t, err)
		require.Empty(t, plans)
	})

	t.Run("subdirectory module-only migration leaves discovered dependency SDKs unrecorded", func(t *testing.T) {
		// With no workspace planned anywhere, a discovered local dependency has
		// no config to record its SDK in; the install is skipped rather than
		// failing the migration.
		plain := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video"), `{
  "name": "video",
  "sdk": {"source": "go"},
  "dependencies": [{"name": "dep", "source": "./dep"}]
}`)
		dep := testRuntimeCompatWorkspace(t, filepath.Join(root, "modules", "video", "dep"), `{
  "name": "dep",
  "sdk": {"source": "go"}
}`)
		dep.DiscoveredLocalModule = true

		plans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{plain, dep}, nil)
		require.NoError(t, err)
		require.Empty(t, plans)
		plans, err = workspaceMigrationInstallDiscoveredModuleSDKs(nil, plans, []*workspace.CompatWorkspace{plain, dep})
		require.NoError(t, err)
		require.Empty(t, plans)
	})

	t.Run("parent SDK module name conflicts get a stable alternate name", func(t *testing.T) {
		moduleRepo := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api"), `{
  "name": "api",
  "sdk": {"source": "go"},
  "toolchains": [{"name": "tc", "source": "./tc"}]
}`)
		migrated := &workspace.MigrationPlan{
			ProjectRoot:       root,
			ModuleProjectRoot: filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(`[modules.dagger-go-sdk]
source = "github.com/acme/custom-go-sdk"
`),
		}

		_, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{moduleRepo}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/custom-go-sdk", cfg.Modules["dagger-go-sdk"].Source)
		sdk := cfg.Modules["dagger-go-sdk-2"]
		require.Equal(t, "go", sdk.Source)
		require.NotNil(t, sdk.AsSDK)
	})

	t.Run("module repo runtime pin and discovered dependency share one SDK install", func(t *testing.T) {
		// One SDK install serves every module in the repo: the hoisted
		// runtime pin and a discovered local dependency using the same runtime
		// must collapse into a single [modules.<sdk>] entry in the root plan.
		moduleRepo := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api"), `{
  "name": "api",
  "sdk": {"source": "go"},
  "toolchains": [{"name": "tc", "source": "./tc"}]
}`)
		dep := testRuntimeCompatWorkspace(t, filepath.Join(root, "services", "api", "libs", "dep"), `{
  "name": "dep",
  "sdk": {"source": "go"}
}`)
		dep.DiscoveredLocalModule = true
		migrated := &workspace.MigrationPlan{
			ProjectRoot:         root,
			ModuleProjectRoot:   filepath.Join(root, "services", "api"),
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		}

		parentPlans, err := workspaceMigrationParentPlansForPlainModules(ws, []*workspace.CompatWorkspace{moduleRepo, dep}, []*workspace.MigrationPlan{migrated})
		require.NoError(t, err)
		parentPlans, err = workspaceMigrationInstallDiscoveredModuleSDKs([]*workspace.MigrationPlan{migrated}, parentPlans, []*workspace.CompatWorkspace{moduleRepo, dep})
		require.NoError(t, err)
		require.Empty(t, parentPlans)

		cfg, err := workspace.ParseConfig(migrated.WorkspaceConfigData)
		require.NoError(t, err)
		require.Len(t, cfg.Modules, 1, "one SDK install serves every module in the repo")
		sdk := cfg.Modules["dagger-go-sdk"]
		require.Equal(t, "go", sdk.Source)
		require.NotNil(t, sdk.AsSDK)
		require.Len(t, sdk.AsSDK.Modules, 1)
		require.Equal(t, filepath.Join("services", "api", "libs", "dep"), filepath.FromSlash(sdk.AsSDK.Modules[0].Path))
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

	ws := &core.Workspace{}
	ws.SetHostPath(root)
	conversions, err := workspaceMigrationModuleConfigConversions(ws, []*workspace.CompatWorkspace{
		plain,
		rootSDKOnly,
		workspaceConfig,
		noSDKPlain,
	})
	require.NoError(t, err)
	require.Len(t, conversions, 4, "every selected shape without toolchains converts in place, including a subdirectory config with a source subdir")
	require.Equal(t, filepath.Join(root, ".dagger", "modules", "video"), conversions[0].ProjectRoot)
	cfg, err := modules.ParseModuleConfigForFilename(conversions[0].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "video", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "github.com/acme/dep@main", cfg.Dependencies[0].Source)
	require.Equal(t, "sha256:abc", cfg.Dependencies[0].Pin)
	require.Equal(t, "./local", cfg.Dependencies[1].Source)

	// A root sdk-only config is the "repo is just a dagger module" shape and
	// converts in place rather than being left as legacy.
	require.Equal(t, root, conversions[1].ProjectRoot)
	cfg, err = modules.ParseModuleConfigForFilename(conversions[1].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "app", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)

	// A subdirectory config that must migrate only because its source is in a
	// subdirectory is the plain "migrate this one module" case: it converts in
	// place with its source preserved, and no workspace is created for it.
	require.Equal(t, filepath.Join(root, "services", "api"), conversions[2].ProjectRoot)
	cfg, err = modules.ParseModuleConfigForFilename(conversions[2].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "api", cfg.Name)
	require.Equal(t, "src", cfg.Source)

	require.Equal(t, filepath.Join(root, "modules", "data"), conversions[3].ProjectRoot)
	cfg, err = modules.ParseModuleConfigForFilename(conversions[3].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "data", cfg.Name)
	require.Nil(t, cfg.SDK)
}

func TestWorkspaceMigrationJoinRelPathAndScope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dir         string
		source      string
		wantDir     string
		projectRoot string
		wantWithin  bool
	}{
		{name: "child from root", dir: ".", source: "./toolchain", wantDir: "toolchain", projectRoot: "", wantWithin: true},
		{name: "nested child", dir: "libs/foo", source: "./sub", wantDir: "libs/foo/sub", projectRoot: "", wantWithin: true},
		{name: "sibling within project dir", dir: "app/libs/foo", source: "../bar", wantDir: "app/libs/bar", projectRoot: "app", wantWithin: true},
		{name: "self", dir: "libs/foo", source: ".", wantDir: "libs/foo", projectRoot: "", wantWithin: true},
		{name: "escapes workspace root", dir: ".", source: "../shared", wantDir: "../shared", projectRoot: "", wantWithin: false},
		{name: "sibling outside project", dir: "app", source: "../hello", wantDir: "hello", projectRoot: "app", wantWithin: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workspaceMigrationJoinRelPath(tc.dir, tc.source)
			require.Equal(t, tc.wantDir, got)
			require.Equal(t, tc.wantWithin, workspaceMigrationWithinProject(tc.projectRoot, got))
		})
	}

	// An absolute source must be caught before joining: path.Join silently
	// strips the leading slash and rebases it under the referrer, so the scope
	// check alone would not flag it — the DFS special-cases path.IsAbs first.
	require.Equal(t, "app/tmp/foo", workspaceMigrationJoinRelPath("app", "/tmp/foo"))
	require.True(t, path.IsAbs("/tmp/foo"))
}

func TestWorkspaceMigrationDiscoveredLocalModuleConversions(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")

	// A normal toolchain (sdk + non-root source) would look "workspace-shaped"
	// to mustMigrateToWorkspaceConfig, but as a discovered local module it must
	// convert in place, keeping its source.
	toolchain := testRuntimeCompatWorkspace(t, filepath.Join(root, "toolchain"), `{
  "name": "tc",
  "sdk": {"source": "go"},
  "source": "src"
}`)
	toolchain.DiscoveredLocalModule = true

	// A discovered module that owns toolchains is a genuine nested workspace and
	// must be left as legacy, not converted in place.
	nested := testRuntimeCompatWorkspace(t, filepath.Join(root, "nested"), `{
  "name": "nested",
  "sdk": {"source": "go"},
  "toolchains": [{"name": "x", "source": "./x"}]
}`)
	nested.DiscoveredLocalModule = true

	ws := &core.Workspace{}
	ws.SetHostPath(root)
	conversions, err := workspaceMigrationModuleConfigConversions(ws, []*workspace.CompatWorkspace{toolchain, nested})
	require.NoError(t, err)
	require.Len(t, conversions, 1, "the normal toolchain converts; the nested workspace is left as legacy")
	require.Equal(t, filepath.Join(root, "toolchain"), conversions[0].ProjectRoot)
	cfg, err := modules.ParseModuleConfigForFilename(conversions[0].ConfigData, workspace.ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "tc", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "src", cfg.Source)
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

	key, err := envScopedConfigKey(cfg, "review env", `modules."my.module".settings."some.key"`, workspaceConfigMustExist)
	require.NoError(t, err)
	require.Equal(t, `env."review env".modules."my.module".settings."some.key"`, key)
}

func TestPlanWorkspaceEnvInstallConfig(t *testing.T) {
	t.Run("creates the env and records the module in its overlay", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}

		plan, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "dep")
		require.NoError(t, err)
		require.True(t, plan.Changed)
		require.True(t, plan.Added)
		require.Equal(t, "dep", cfg.Env["dev"].Modules["dep"].Source)
		require.Empty(t, cfg.Modules)
	})

	t.Run("reinstall with the same source is a no-op", func(t *testing.T) {
		cfg := &workspace.Config{
			Env: map[string]workspace.EnvOverlay{
				"dev": {Modules: map[string]workspace.EnvModuleOverlay{
					"dep": {Source: "dep"},
				}},
			},
		}

		plan, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "dep")
		require.NoError(t, err)
		require.False(t, plan.Changed)
	})

	t.Run("conflicting source in the same env is rejected", func(t *testing.T) {
		cfg := &workspace.Config{
			Env: map[string]workspace.EnvOverlay{
				"dev": {Modules: map[string]workspace.EnvModuleOverlay{
					"dep": {Source: "dep"},
				}},
			},
		}

		_, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "other/dep")
		require.ErrorContains(t, err, `module "dep" already exists in env "dev"`)
	})

	t.Run("base module with another source is overridden, not rejected", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"dep": {Source: "base/dep"},
			},
		}

		plan, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "dep")
		require.NoError(t, err)
		require.True(t, plan.Changed)
		require.Equal(t, "dep", cfg.Env["dev"].Modules["dep"].Source)
		require.Equal(t, "base/dep", cfg.Modules["dep"].Source)
		require.Empty(t, cfg.Env["dev"].Modules["dep"].Pin)
	})

	t.Run("redundant overlay of a base module carries the base pin", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"dep": {Source: "github.com/foo/dep", Pin: "abc123"},
			},
		}

		plan, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "github.com/foo/dep")
		require.NoError(t, err)
		require.True(t, plan.Changed)
		require.Equal(t, "github.com/foo/dep", cfg.Env["dev"].Modules["dep"].Source)
		require.Equal(t, "abc123", cfg.Env["dev"].Modules["dep"].Pin)
	})

	t.Run("settings-only overlay entry is upgraded in place", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"dep": {Source: "base/dep"},
			},
			Env: map[string]workspace.EnvOverlay{
				"dev": {Modules: map[string]workspace.EnvModuleOverlay{
					"dep": {Settings: map[string]any{"region": "eu"}},
				}},
			},
		}

		plan, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "dep", "dep")
		require.NoError(t, err)
		require.True(t, plan.Changed)
		require.True(t, plan.Added)
		entry := cfg.Env["dev"].Modules["dep"]
		require.Equal(t, "dep", entry.Source)
		require.Equal(t, map[string]any{"region": "eu"}, entry.Settings)
	})

	t.Run("SDK installs are rejected under an env selection", func(t *testing.T) {
		cfg := &workspace.Config{}

		_, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{AsSdk: true}, "go-sdk", "go-sdk")
		require.ErrorContains(t, err, `SDKs cannot be installed in env "dev"`)
		require.Empty(t, cfg.Env)
	})

	t.Run("re-sourcing a base SDK entry is rejected", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"go-sdk": {Source: "sdk/go", AsSDK: &workspace.ModuleAsSDK{}},
			},
		}

		_, err := planWorkspaceEnvInstallConfig(cfg, "dev", workspaceInstallArgs{}, "go-sdk", "other/go")
		require.ErrorContains(t, err, `module "go-sdk" is an SDK; SDKs cannot be installed in env "dev"`)
		require.Empty(t, cfg.Env)
	})
}

func TestEnvScopedConfigKeyMissingEnv(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"aws": {Source: "modules/aws"},
		},
	}

	// Writes create the env, so the key maps even when the env is undefined.
	key, err := envScopedConfigKey(cfg, "staging", "modules.aws.settings.region", workspaceConfigInitIfMissing)
	require.NoError(t, err)
	require.Equal(t, "env.staging.modules.aws.settings.region", key)

	// Unsets still require the env to exist.
	_, err = envScopedConfigKey(cfg, "staging", "modules.aws.settings.region", workspaceConfigMustExist)
	require.ErrorContains(t, err, `workspace env "staging" is not defined`)
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
	require.NoError(t, lock.SetLookup("", "oci-sha", []any{"alpine:latest"}, "sha256:deadbeef"))
	require.NoError(t, lock.SetLookup("", workspaceMigrationLockModulesResolveOperation, []any{"github.com/acme/mod@main"}, "0123456789abcdef0123456789abcdef01234567"))

	data, err := lock.Marshal()
	require.NoError(t, err)

	filteredData, err := workspaceMigrationFilterLegacyLockData(data)
	require.NoError(t, err)
	filtered, err := workspace.ParseLock(filteredData)
	require.NoError(t, err)

	container, ok := filtered.GetLookup("", "oci-sha", []any{"alpine:latest"})
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", container)

	_, ok = filtered.GetLookup("", workspaceMigrationLockModulesResolveOperation, []any{"github.com/acme/mod@main"})
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

package schema

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	dangv2 "github.com/dagger/dagger/core/sdk/dang/v2"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestModuleLocaLSource(t *testing.T) {
	schema := &moduleSourceSchema{}
	ctx := context.Background()

	src := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/home/user/dagger-test-modules",
		},
	}

	for _, tc := range []struct {
		name           string
		source         *core.ModuleSource
		expectedResult string
		expectError    bool
		fn             func(ctx context.Context, source *core.ModuleSource, args struct{}) (string, error)
	}{
		{
			name:           "Local module source commit",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceCommit,
		},
		{
			name:           "Local module source html repo url",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceHTMLRepoURL,
		},
		{
			name:           "Local module source version",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(ctx, tc.source, struct{}{})
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

func TestLegacyWorkspaceFieldHandling(t *testing.T) {
	t.Parallel()

	local := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/repo-b",
		},
		OriginalRefString: ".",
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
		ConfigToolchains: []*modules.ModuleConfigDependency{{
			Name:   "go",
			Source: "./toolchains/go",
		}},
	}

	require.True(t, local.UsesLegacyWorkspaceFields())
	require.Equal(t, []string{"blueprint", "toolchains"}, local.LegacyWorkspaceFieldNames())

	stripped := local.StripLegacyWorkspaceFields()
	require.Nil(t, stripped.ConfigBlueprint)
	require.Nil(t, stripped.ConfigToolchains)
	require.False(t, stripped.UsesLegacyWorkspaceFields())

	directErr := local.DirectLegacyWorkspaceLoadError()
	require.EqualError(t,
		directErr,
		"This module's dagger.json uses toolchains or blueprints, which have moved to workspaces.\n\nTry: dagger -W .\n\nTo learn more: https://docs.dagger.io/reference/upgrade-to-workspaces",
	)
	var ext dagql.ExtendedError
	require.True(t, errors.As(directErr, &ext))
	require.Equal(t, map[string]any{
		"_quiet":    true,
		"_message":  "This module's dagger.json uses toolchains or blueprints, which have moved to workspaces.\n\nTry: dagger -W .\n\nTo learn more: https://docs.dagger.io/reference/upgrade-to-workspaces",
		"_exitCode": 1,
	}, ext.Extensions())
	require.EqualError(t,
		local.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"/work/repo-b\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint, toolchains\"\n\nrun `dagger workspace migrate` in \"/work/repo-b\", then update this source to point at one of the migrated modules under \"/work/repo-b/.dagger/modules\"",
	)

	remote := &core.ModuleSource{
		Kind: core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			CloneRef: "https://github.com/acme/repo-b",
			Version:  "main",
		},
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
	}

	require.EqualError(t,
		remote.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"https://github.com/acme/repo-b@main\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint\"\n\nuse a migrated ref that points at one of its real modules. If you control that repo, migrate it first",
	)
}

func TestLoadCurrentModuleSourceConfigPreservesGitDependencySourceAndPin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dag, err := dagql.NewServer(ctx, &core.Query{})
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))

	dep := &core.ModuleSource{
		Kind:              core.ModuleSourceKindGit,
		ModuleName:        "dep",
		OriginalRefString: "https://github.com/acme/dep/sdk",
		SourceRootSubpath: "sdk",
		Git: &core.GitModuleSource{
			CloneRef:     "https://github.com/acme/dep",
			Version:      "v1.2.3",
			VersionQuery: "v1.2",
			Commit:       "1234567890abcdef",
		},
	}
	parent := &core.ModuleSource{
		Kind:               core.ModuleSourceKindLocal,
		ConfigFilename:     modules.Filename,
		ModuleOriginalName: "parent",
		EngineVersion:      engine.Version,
		SourceRootSubpath:  ".",
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/parent",
		},
		Dependencies: dagql.ObjectResultArray[*core.ModuleSource]{
			moduleSourceObjectResult(t, dag, "dep", dep),
		},
	}

	cfg, err := (&moduleSourceSchema{}).loadModuleSourceConfig(parent)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 1)
	require.Equal(t, "1234567890abcdef", cfg.Dependencies[0].Pin)

	out, err := modules.MarshalModuleConfigForFilename(cfg, modules.Filename)
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "parent"`)
	require.Contains(t, string(out), `engineVersion = "`+engine.Version+`"`)
	require.Contains(t, string(out), `name = "dep"`)
	require.Contains(t, string(out), `source = "https://github.com/acme/dep/sdk@v1.2"`)
	require.Contains(t, string(out), `pin = "1234567890abcdef"`)
	require.NotContains(t, string(out), `version =`)
}

// A manifest version 2 module declares its engine version in the dang.toml of
// its entrypoint, not in the manifest, so the version is checked on the source
// and not written to the manifest. currentModule.source loads the config this
// way from inside a module.
func TestLoadModuleSourceConfigAcceptsEntrypointManifest(t *testing.T) {
	t.Parallel()

	entrypoint := &modules.ModuleEntrypointConfig{
		Kind:   modules.ModuleEntrypointKindDang,
		Source: "./entrypoint",
	}
	src := &core.ModuleSource{
		Kind:               core.ModuleSourceKindLocal,
		ConfigFilename:     modules.Filename,
		ModuleOriginalName: "tiny",
		EngineVersion:      "v1.0.0-beta.20",
		SourceRootSubpath:  ".",
		Entrypoint:         entrypoint,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/tiny",
		},
	}

	cfg, err := (&moduleSourceSchema{}).loadModuleSourceConfig(src)
	require.NoError(t, err)
	require.Equal(t, "tiny", cfg.Name)
	require.Equal(t, entrypoint, cfg.Entrypoint)
	require.Empty(t, cfg.EngineVersion)
}

func TestLoadModuleSourceConfigChecksEntrypointEngineVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		version string
		err     string
	}{
		{version: "v999.0.0", err: "module requires dagger v999.0.0, but you have " + engine.Version},
		{version: "v0.9.0", err: "module requires dagger v0.9.0, but support for that version has been removed"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			src := &core.ModuleSource{
				Kind:               core.ModuleSourceKindLocal,
				ConfigFilename:     modules.Filename,
				ModuleOriginalName: "tiny",
				EngineVersion:      tc.version,
				SourceRootSubpath:  ".",
				Entrypoint: &modules.ModuleEntrypointConfig{
					Kind:   modules.ModuleEntrypointKindDang,
					Source: "./entrypoint",
				},
				Local: &core.LocalModuleSource{
					ContextDirectoryPath: "/work/tiny",
				},
			}
			_, err := (&moduleSourceSchema{}).loadModuleSourceConfig(src)
			require.EqualError(t, err, tc.err)
		})
	}
}

func moduleSourceObjectResult(t *testing.T, dag *dagql.Server, op string, self *core.ModuleSource) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()

	res, err := dagql.NewObjectResultForCall(self, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "module-source-" + op,
		Type:        dagql.NewResultCallType(self.Type()),
	})
	require.NoError(t, err)
	return res
}

// A finite in-memory filesystem drives the real mutators/resolver without an
// engine service. Every test gets its own cache and a bounded context.
func versionMutationSource(t *testing.T, caching *bool) (context.Context, *moduleSourceSchema, *core.ModuleSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "nested", SessionID: "version-mutation"})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	facade := &foreignWorkspaceTestServer{&currentTypeDefsTestServer{}}
	query := core.NewRoot(facade)
	ctx = core.ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	dag, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	facade.dag = dag
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Directory]{}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.File]{}))
	files := map[string]string{
		"a/dang.toml":       "dang = \"1.0\"\n[imports.Dagger]\ndagger = \"v0.19.0\"\n",
		"b/dang.toml":       "dang = \"2.0\"\n[imports.Dagger]\ndagger = \"" + engine.IdentifierCasingVersion + "\"\n",
		"default/dang.toml": "[imports.Dagger]\ndagger = \"v0.19.0\"\n",
	}
	fileContents := map[*core.File]string{}
	dagql.Fields[*core.Directory]{
		dagql.Func("digest", func(context.Context, *core.Directory, struct{}) (string, error) {
			return "version-files-digest", nil
		}),
		dagql.Func("exists", func(_ context.Context, _ *core.Directory, args struct{ Path string }) (bool, error) {
			path := filepath.Clean(args.Path)
			_, exists := files[path]
			return exists || path == "." || path == "a" || path == "b" || path == "none" || path == "default", nil
		}),
		dagql.Func("file", func(_ context.Context, _ *core.Directory, args struct{ Path string }) (*core.File, error) {
			contents, exists := files[filepath.Clean(args.Path)]
			if !exists {
				return nil, fmt.Errorf("unexpected read %s", args.Path)
			}
			file := &core.File{}
			fileContents[file] = contents
			return file, nil
		}),
	}.Install(dag)
	dagql.Fields[*core.File]{dagql.Func("contents", func(_ context.Context, file *core.File, _ struct{}) (string, error) {
		return fileContents[file], nil
	})}.Install(dag)
	dir, err := dagql.NewObjectResultForCall(&core.Directory{}, dag, &dagql.ResultCall{
		Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "version-files", Type: dagql.NewResultCallType((&core.Directory{}).Type()),
	})
	require.NoError(t, err)
	s := &moduleSourceSchema{}
	src := &core.ModuleSource{Kind: core.ModuleSourceKindDir, SourceRootSubpath: ".", ContextDirectory: dir, DirSrc: &core.DirModuleSource{OriginalContextDir: dir}, ConfigFilename: "dagger.json"}
	config := `{"name":"tiny","sdk":"dang","source":"a","engineVersion":"v0.21.9"`
	if caching != nil {
		config += fmt.Sprintf(`,"disableDefaultFunctionCaching":%t`, *caching)
	}
	err = s.initFromModConfig([]byte(config+"}"), src)
	require.NoError(t, err)
	require.NoError(t, s.resolveModuleSourceVersions(ctx, src))
	return ctx, s, src
}

func TestDangSourceVersionMutations(t *testing.T) {
	// Run as the engine of Dang24ModuleVersion: an earlier engine caps a
	// module's view, and so its default Dang version, at its own. Not
	// parallel, as it sets engine.Version.
	oldVersion := engine.Version
	engine.Version = dangv2.Dang24ModuleVersion
	t.Cleanup(func() { engine.Version = oldVersion })

	ctx, s, original := versionMutationSource(t, nil)
	require.Equal(t, "v0.19.0", original.EngineVersion)
	require.Equal(t, "1.0", original.DangVersion)
	require.True(t, original.DisableDefaultFunctionCaching)
	for _, path := range []string{"b", "none", "default"} {
		t.Run(path, func(t *testing.T) {
			got, err := s.moduleSourceWithSourceSubpath(ctx, original, struct{ Path string }{path})
			require.NoError(t, err)
			fresh := original.Clone()
			fresh.SourceSubpath = path
			require.NoError(t, s.resolveModuleSourceVersions(ctx, fresh))
			require.Equal(t, fresh.EngineVersion, got.EngineVersion)
			require.Equal(t, fresh.DangVersion, got.DangVersion)
			require.Equal(t, fresh.DisableDefaultFunctionCaching, got.DisableDefaultFunctionCaching)
			require.Equal(t, "v0.21.9", got.ConfigEngineVersion)
			switch path {
			case "b":
				require.Equal(t, engine.IdentifierCasingVersion, got.EngineVersion)
				require.Equal(t, "2.0", got.DangVersion)
				require.False(t, got.DisableDefaultFunctionCaching)
			case "none":
				require.Equal(t, "v0.21.9", got.EngineVersion)
				require.False(t, got.DangVersionDeclared)
			}
		})
	}
	require.Equal(t, "v0.19.0", original.EngineVersion, "mutators do not modify the receiver")

	// Equal-valued overrides still carry provenance and survive later changes.
	for _, override := range []string{"v0.19.0", "v1.0.0-beta.27", dangv2.Dang24ModuleVersion} {
		for _, path := range []string{"b", "none", "default"} {
			t.Run(override+"/"+path, func(t *testing.T) {
				before, err := s.moduleSourceWithEngineVersion(ctx, original, struct{ Version string }{override})
				require.NoError(t, err)
				before, err = s.moduleSourceWithSourceSubpath(ctx, before, struct{ Path string }{path})
				require.NoError(t, err)
				after, err := s.moduleSourceWithSourceSubpath(ctx, original, struct{ Path string }{path})
				require.NoError(t, err)
				after, err = s.moduleSourceWithEngineVersion(ctx, after, struct{ Version string }{override})
				require.NoError(t, err)
				require.Equal(t, override, before.EngineVersion)
				require.Equal(t, before.EngineVersion, after.EngineVersion)
				require.Equal(t, before.DangVersion, after.DangVersion)
				require.Equal(t, before.DisableDefaultFunctionCaching, after.DisableDefaultFunctionCaching)
				require.Equal(t, "v0.21.9", before.ConfigEngineVersion)
				if path == "b" {
					require.Equal(t, "2.0", before.DangVersion, "declared grammar survives API override")
				} else if override == "v0.19.0" {
					require.Equal(t, "1.0", before.DangVersion)
				} else if override == "v1.0.0-beta.27" {
					// The last version before Dang24ModuleVersion.
					require.Equal(t, "2.1", before.DangVersion, "grammar default follows final API version")
				} else {
					require.Equal(t, "2.4", before.DangVersion, "grammar default follows final API version")
				}
			})
		}
	}
}

func TestDangSDKVersionMutations(t *testing.T) {
	ctx, s, original := versionMutationSource(t, nil)
	// The Go SDK is a module the engine image carries, which this server
	// can't load. The test needs only an SDK that isn't Dang's. Not parallel:
	// it replaces the package's SDK loader.
	loadSDK := loadModuleSourceSDK
	loadModuleSourceSDK = func(ctx context.Context, query *core.Query, cfg *core.SDKConfig, src *core.ModuleSource) (core.SDK, error) {
		if cfg.Source == "go" {
			return &prebuildTestSDK{}, nil
		}
		return loadSDK(ctx, query, cfg, src)
	}
	t.Cleanup(func() { loadModuleSourceSDK = loadSDK })
	for _, sdk := range []string{"go", ""} {
		for _, override := range []string{"", engine.IdentifierCasingVersion} {
			t.Run(sdk+"/"+override, func(t *testing.T) {
				src := original
				var err error
				if override != "" {
					src, err = s.moduleSourceWithEngineVersion(ctx, src, struct{ Version string }{override})
					require.NoError(t, err)
				}
				got, err := s.moduleSourceWithSDK(ctx, src, struct{ Source string }{sdk})
				require.NoError(t, err)
				want := "v0.21.9"
				if override != "" {
					want = override
				}
				require.Equal(t, want, got.EngineVersion)
				require.Empty(t, got.DangVersion)
				require.False(t, got.DangVersionDeclared)
				if override != "" {
					after, err := s.moduleSourceWithSDK(ctx, original, struct{ Source string }{sdk})
					require.NoError(t, err)
					after, err = s.moduleSourceWithEngineVersion(ctx, after, struct{ Version string }{override})
					require.NoError(t, err)
					require.Equal(t, got.EngineVersion, after.EngineVersion)
					require.Equal(t, got.DangVersion, after.DangVersion)
					require.Equal(t, got.DisableDefaultFunctionCaching, after.DisableDefaultFunctionCaching)
				}
				got, err = s.moduleSourceWithSDK(ctx, got, struct{ Source string }{"dang"})
				require.NoError(t, err)
				want = "v0.19.0"
				if override != "" {
					want = override
				}
				require.Equal(t, want, got.EngineVersion)
				require.Equal(t, "1.0", got.DangVersion)
			})
		}
	}
}

func TestDangVersionOverrideScopedMutation(t *testing.T) {
	ctx, s, original := versionMutationSource(t, nil)
	dag, err := core.CurrentDagqlServer(ctx)
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{}))
	dagql.Fields[*core.ModuleSource]{
		dagql.NodeFunc("_implementationScoped", s.moduleSourceImplementationScoped),
		dagql.Func("withSourceSubpath", s.moduleSourceWithSourceSubpath),
	}.Install(dag)
	scopedMutation := func(name string, src *core.ModuleSource) *core.ModuleSource {
		t.Helper()
		inst := moduleSourceObjectResult(t, dag, name, src)
		var got dagql.ObjectResult[*core.ModuleSource]
		require.NoError(t, dag.Select(ctx, inst, &got,
			dagql.Selector{Field: "_implementationScoped"},
			dagql.Selector{Field: "withSourceSubpath", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("b")}}},
		))
		return got.Self()
	}
	// Warm the same mutation without an override first. The effective source
	// versions match, but future mutation behavior must not share that cache.
	warm := scopedMutation("plain", original)
	require.Equal(t, engine.IdentifierCasingVersion, warm.EngineVersion)
	overridden, err := s.moduleSourceWithEngineVersion(ctx, original, struct{ Version string }{original.EngineVersion})
	require.NoError(t, err)
	got := scopedMutation("overridden", overridden)
	require.Equal(t, original.EngineVersion, got.EngineVersion)
	require.Equal(t, original.EngineVersion, got.EngineVersionOverride)

	baseDigest, err := original.SourceImplementationDigest(ctx)
	require.NoError(t, err)
	seen := map[string]bool{baseDigest.String(): true}
	for _, change := range []func(*core.ModuleSource){
		func(src *core.ModuleSource) { src.EngineVersionOverride = src.EngineVersion },
		func(src *core.ModuleSource) { src.ConfigEngineVersion = "v0.19.4" },
		func(src *core.ModuleSource) { src.ConfigDisableDefaultFunctionCaching = ptr(false) },
		func(src *core.ModuleSource) { src.ConfigDisableDefaultFunctionCaching = ptr(true) },
	} {
		src := original.Clone()
		change(src)
		digest, err := src.SourceImplementationDigest(ctx)
		require.NoError(t, err)
		require.NotContains(t, seen, digest.String(), "scopes must distinguish every config/override provenance")
		seen[digest.String()] = true
	}
}

func TestEntrypointReloadResolvesBeforeSources(t *testing.T) {
	ctx, s, src := versionMutationSource(t, nil)
	src.Entrypoint = &modules.ModuleEntrypointConfig{Kind: modules.ModuleEntrypointKindDang, Source: "./b"}
	src.ConfigEngineVersion = ""
	src.EngineVersionOverride = "v0.19.0"
	raw := src.DirSrc.OriginalContextDir
	dag, err := core.CurrentDagqlServer(ctx)
	require.NoError(t, err)
	// Model a previous sources output. Reload must discard it before reading
	// the governing config or evaluating sources again.
	src.ContextDirectory, err = dagql.NewObjectResultForCall(&core.Directory{}, dag, &dagql.ResultCall{
		Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "old-sources-output", Type: dagql.NewResultCallType((&core.Directory{}).Type()),
	})
	require.NoError(t, err)
	stop := errors.New("stop before mounting entrypoint")
	calls := 0
	dagql.Fields[*core.Directory]{dagql.Func("directory", func(_ context.Context, dir *core.Directory, args struct{ Path string }) (*core.Directory, error) {
		calls++
		require.Same(t, raw.Self(), dir)
		require.Equal(t, "b", args.Path)
		require.Equal(t, "v0.19.0", src.EngineVersion, "override applied before sources")
		require.Equal(t, "2.0", src.DangVersion, "declared grammar resolved before sources")
		require.True(t, src.DisableDefaultFunctionCaching, "default caching follows the override")
		return nil, stop
	})}.Install(dag)
	require.ErrorIs(t, s.reloadModuleSourceContext(ctx, src), stop)
	require.Equal(t, 1, calls)
	cfg, err := s.buildModuleConfig(src)
	require.NoError(t, err)
	require.Empty(t, cfg.EngineVersion, "entrypoint overrides are never written to manifests")
}

func TestDangVersionConfigProvenance(t *testing.T) {
	for _, explicit := range []*bool{nil, ptr(false), ptr(true)} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			ctx, s, src := versionMutationSource(t, explicit)
			got, err := s.moduleSourceWithSourceSubpath(ctx, src, struct{ Path string }{"b"})
			require.NoError(t, err)
			cfg, err := s.buildModuleConfig(got)
			require.NoError(t, err)
			require.Equal(t, "v0.21.9", cfg.EngineVersion, "dang.toml must not leak into config")
			require.Equal(t, explicit, cfg.DisableDefaultFunctionCaching)
			if explicit != nil {
				require.Equal(t, *explicit, got.DisableDefaultFunctionCaching)
			}
			got, err = s.moduleSourceWithEngineVersion(ctx, got, struct{ Version string }{"v999.0.0"})
			require.NoError(t, err, "a future requirement can be written without loading module code")
			cfg, err = s.buildModuleConfig(got)
			require.NoError(t, err)
			require.Equal(t, "v999.0.0", cfg.EngineVersion)
			require.Equal(t, explicit, cfg.DisableDefaultFunctionCaching)
			// Fresh load drops API-chain overrides; the source's dang.toml wins again.
			got, err = s.moduleSourceWithEngineVersion(ctx, got, struct{ Version string }{"v0.19.0"})
			require.NoError(t, err)
			cfg, err = s.buildModuleConfig(got)
			require.NoError(t, err)
			data, err := modules.MarshalModuleConfigForFilename(cfg, "dagger.json")
			require.NoError(t, err)
			fresh := got.Clone()
			err = s.initFromModConfig(data, fresh)
			require.NoError(t, err)
			require.Empty(t, fresh.EngineVersionOverride)
			require.NoError(t, s.resolveModuleSourceVersions(ctx, fresh))
			require.Equal(t, engine.IdentifierCasingVersion, fresh.EngineVersion)
		})
	}
}

// A rewritten version 2 manifest keeps an include it declares, which replaces
// sources, and drops a fat manifest's, like the fat manifest's other pre-v2
// fields, so the rewrite never turns that include into a replacement.
func TestBuildModuleConfigKeepsOnlyADeclaredInclude(t *testing.T) {
	entrypoint := &modules.ModuleEntrypointConfig{Kind: modules.ModuleEntrypointKindDang, Source: "./entrypoint"}
	s := &moduleSourceSchema{}

	declared, err := s.buildModuleConfig(&core.ModuleSource{
		ModuleOriginalName: "tiny",
		Entrypoint:         entrypoint,
		IncludePaths:       []string{},
		IncludeDeclared:    true,
	})
	require.NoError(t, err)
	out, err := modules.MarshalModuleConfigForFilename(declared, modules.Filename)
	require.NoError(t, err)
	require.Contains(t, string(out), "include = []")

	fat, err := s.buildModuleConfig(&core.ModuleSource{
		ModuleOriginalName: "tiny",
		Entrypoint:         entrypoint,
		IncludePaths:       []string{"../shared/*.txt"},
	})
	require.NoError(t, err)
	out, err = modules.MarshalModuleConfigForFilename(fat, modules.Filename)
	require.NoError(t, err)
	require.NotContains(t, string(out), "include")
}

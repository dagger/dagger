package daggercmd

import (
	"context"
	"encoding/json"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

// legacyModuleRegistry is the hand-written registry that modules.json
// replaced, kept verbatim so tests can prove the declarative matchers behave
// exactly like the callbacks did.
func legacyModuleRegistry() []registryModule {
	return []registryModule{
		{
			Name:        "go",
			Description: "Test and generate code for Go projects with the Go toolchain",
			Repo:        "dagger.io/go",
			Recommend:   SimpleRecommend("**/go.mod"),
		},
		{
			Name:        "golangci-lint",
			Description: "Lint Go projects with golangci-lint",
			Repo:        "dagger.io/go/golangci-lint",
			Recommend:   SimpleRecommend("**/.golangci.yml", "**/.golangci.yaml", "**/.golangci.toml", "**/.golangci.json"),
		},
		{
			Name:        "staticcheck",
			Description: "Lint Go projects with Staticcheck",
			Repo:        "dagger.io/go/staticcheck",
			Recommend:   SimpleRecommend("**/staticcheck.conf"),
		},
		{
			Name:        "deno",
			Description: "Test, lint, format, and type-check Deno projects with Dagger",
			Repo:        "dagger.io/js/deno",
			Recommend:   SimpleRecommend("**/deno.json", "**/deno.jsonc", "**/deno.lock"),
		},
		{
			Name:        "eslint",
			Description: "Lint JavaScript and TypeScript with ESLint",
			Repo:        "dagger.io/js/eslint",
			Recommend:   SimpleRecommend("**/.eslintrc*", "**/eslint.config.*"),
		},
		{
			Name:        "prettier",
			Description: "Format code with Prettier",
			Repo:        "dagger.io/js/prettier",
			Recommend:   SimpleRecommend("**/.prettierrc*", "**/prettier.config.*"),
		},
		{
			Name:        "jest",
			Description: "Run JavaScript and TypeScript tests with Jest",
			Repo:        "dagger.io/js/jest",
			Recommend:   SimpleRecommend("**/jest.config.*"),
		},
		{
			Name:        "mochajs",
			Description: "Run JavaScript and TypeScript tests with Mocha",
			Repo:        "dagger.io/js/mocha",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/.mocharc.*")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/package.json", legacyHasMochaProperty)
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "vitest",
			Description: "Run JavaScript and TypeScript tests with Vitest",
			Repo:        "dagger.io/js/vitest",
			Recommend:   SimpleRecommend("**/vitest.config.*"),
		},
		{
			Name:        "pytest",
			Description: "Run Python tests with pytest",
			Repo:        "dagger.io/python/pytest",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/pytest.ini", "**/.pytest.ini")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/pyproject.toml", hasTOMLTable("tool.pytest"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				configs, err = recommendConfigFiles(ctx, ws, "**/tox.ini", hasINISection("pytest"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				configs, err = recommendConfigFiles(ctx, ws, "**/setup.cfg", hasINISection("tool:pytest"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "mypy",
			Description: "Type-check Python projects with mypy",
			Repo:        "dagger.io/python/mypy",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/mypy.ini", "**/.mypy.ini")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/pyproject.toml", hasTOMLTable("tool.mypy"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				configs, err = recommendConfigFiles(ctx, ws, "**/setup.cfg", hasINISection("mypy"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "ruff",
			Description: "Lint and format Python projects with Ruff",
			Repo:        "dagger.io/python/ruff",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/ruff.toml", "**/.ruff.toml")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/pyproject.toml", hasTOMLTable("tool.ruff"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "ty",
			Description: "Type-check Python projects with ty",
			Repo:        "dagger.io/python/ty",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/ty.toml")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/pyproject.toml", hasTOMLTable("tool.ty"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "uv",
			Description: "Lock, build, and audit Python projects with uv",
			Repo:        "dagger.io/python/uv",
			Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/uv.lock", "**/uv.toml")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/pyproject.toml", hasTOMLTable("tool.uv"))
				if err != nil {
					return nil, err
				}
				matches = append(matches, configs...)
				return matches, nil
			},
		},
		{
			Name:        "biomejs",
			Description: "Format and lint JavaScript and TypeScript with Biome",
			Repo:        "dagger.io/js/biome",
			Recommend:   SimpleRecommend("**/biome.json*"),
		},
		{
			Name:        "playwright",
			Description: "Run end-to-end browser tests with Playwright",
			Repo:        "dagger.io/js/playwright",
			Recommend:   SimpleRecommend("**/playwright.config.*"),
		},
		{
			Name:        "android",
			Description: "Run test, lint, and build on Android projects",
			Repo:        "dagger.io/android",
			Recommend:   SimpleRecommend("**/AndroidManifest.xml"),
		},
		{
			Name:        "swift",
			Description: "Test and build Swift packages",
			Repo:        "dagger.io/apple/swift",
			Recommend:   SimpleRecommend("**/Package.swift"),
		},
		{
			Name:        "maven",
			Description: "Run tests and builds with Maven",
			Repo:        "dagger.io/java/maven",
			Recommend:   SimpleRecommend("**/pom.xml"),
		},
	}
}

func legacyHasMochaProperty(contents string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &object); err != nil {
		return false
	}
	_, found := object["mocha"]
	return found
}

// Fixtures for every recommendation entry, from the callback mapping table in
// the module index design. Positive files recommend their module; negative
// files must not recommend anything.
var (
	recommendParityPositive = map[string]string{
		"go.mod":     "module example.com/root",
		"svc/go.mod": "module example.com/svc",

		"lint/.golangci.yml":    "",
		"lint/a/.golangci.yaml": "",
		"lint/b/.golangci.toml": "",
		"lint/c/.golangci.json": "{}",

		"staticcheck.conf": "",

		"deno/deno.json":    "{}",
		"deno/x/deno.jsonc": "{}",
		"deno/y/deno.lock":  "{}",

		"web/.eslintrc":          "{}",
		".eslintrc.json":         "{}",
		"web/eslint.config.mjs":  "",
		"web/.prettierrc":        "{}",
		"web/prettier.config.js": "",
		"web/jest.config.ts":     "",
		"web/vitest.config.ts":   "",
		"web/biome.json":         "{}",
		"web/x/biome.jsonc":      "{}",

		"js/.mocharc.yml":      "",
		"package.json":         `{"mocha": {"timeout": 1000}}`,
		"js/null/package.json": `{"mocha": null}`,

		"py/pytest.ini":    "[pytest]",
		"py/a/.pytest.ini": "[pytest]",
		"py/pyproject.toml": "[tool.pytest.ini_options]\naddopts = '-q'\n" +
			"[tool.mypy]\nstrict = true\n" +
			"[tool.ruff.lint]\nselect = ['ALL']\n" +
			"[tool.ty.rules]\n" +
			"[tool.uv]\n",
		"py/dotted/pyproject.toml": "tool.ruff.line-length = 88",
		"py/inline/pyproject.toml": "tool.uv = {offline = true}",
		"py/native/pyproject.toml": "[tool.pytest]\naddopts = ['-q']",
		"py/tox.ini":               "[tox]\nenvlist = py313\n[pytest] ; options\r\naddopts = -q",
		"py/setup.cfg":             "[metadata]\nname = demo\n[tool:pytest]\naddopts = -q\n[mypy]\nstrict = true",
		"py/mypy.ini":              "[mypy]",
		"py/b/.mypy.ini":           "[mypy]",
		"py/ruff.toml":             "line-length = 88",
		"py/c/.ruff.toml":          "line-length = 88",
		"py/ty.toml":               "[rules]",
		"py/uv.lock":               "version = 1",
		"py/d/uv.toml":             "offline = true",

		"e2e/playwright.config.ts": "",

		"app/src/main/AndroidManifest.xml": "<manifest/>",

		"Package.swift": "// swift-tools-version:5.9",

		"java/pom.xml": "<project/>",
	}

	recommendParityNegative = map[string]string{
		"go.sum":            "",
		"lint/golangci.yml": "",
		"staticcheck.toml":  "",
		"deno/deno.yaml":    "",
		"web/eslint.json":   "{}",
		"web/prettier.json": "{}",
		"web/jest.json":     "{}",
		"web/vitest.ts":     "",
		"web/biome.toml":    "",

		"neg/package.json":     `{"devDependencies": {"mocha": "^10"}, "description": "mocha"}`,
		"neg/bad/package.json": `{"mocha":`,
		"neg/arr/package.json": `[{"mocha": {}}]`,

		"neg/pyproject.toml":         "description = '''\n[tool.pytest]\n[tool.mypy]\n[tool.ruff]\n[tool.ty]\n[tool.uv]\n'''\n# [tool.ruff]",
		"neg/quoted/pyproject.toml":  "['tool.ruff']\n['tool.mypy']",
		"neg/scalar/pyproject.toml":  "tool.ruff = true\ntool.uv = 'x'",
		"neg/invalid/pyproject.toml": "[tool.ruff]\n[tool.mypy]\ninvalid =",
		"neg/tox.ini":                "[tox]\n[pytest-cov]\n# [pytest]\n    [pytest]",
		"neg/setup.cfg":              "[metadata]\ndescription = mypy pytest\n    [tool:pytest]\n[mypy-other]\n[mypy]invalid\n[pytest]",
		"neg/pytest.cfg":             "[pytest]",
		"neg/mypy.cfg":               "[mypy]",
		"neg/ruff.yaml":              "",

		"e2e/playwright.json": "{}",
		"app/Manifest.xml":    "",
		"java/build.gradle":   "",
	}

	recommendParityExcluded = map[string]string{
		"go.mod":               "module example.com/excluded",
		".golangci.yml":        "",
		"staticcheck.conf":     "",
		"deno.json":            "{}",
		".eslintrc":            "{}",
		".prettierrc":          "{}",
		"jest.config.js":       "",
		".mocharc.json":        "{}",
		"package.json":         `{"mocha": {}}`,
		"vitest.config.ts":     "",
		"pytest.ini":           "[pytest]",
		"pyproject.toml":       "[tool.pytest]\n[tool.mypy]\n[tool.ruff]\n[tool.ty]\n[tool.uv]",
		"tox.ini":              "[pytest]",
		"setup.cfg":            "[mypy]\n[tool:pytest]",
		"mypy.ini":             "[mypy]",
		"ruff.toml":            "",
		"ty.toml":              "",
		"uv.lock":              "",
		"biome.json":           "{}",
		"playwright.config.ts": "",
		"AndroidManifest.xml":  "<manifest/>",
		"Package.swift":        "",
		"pom.xml":              "<project/>",
	}

	recommendParityWant = map[string][]string{
		"go":            {"go.mod", "svc/go.mod"},
		"golangci-lint": {"lint/.golangci.yml", "lint/a/.golangci.yaml", "lint/b/.golangci.toml", "lint/c/.golangci.json"},
		"staticcheck":   {"staticcheck.conf"},
		"deno":          {"deno/deno.json", "deno/x/deno.jsonc", "deno/y/deno.lock"},
		"eslint":        {".eslintrc.json", "web/.eslintrc", "web/eslint.config.mjs"},
		"prettier":      {"web/.prettierrc", "web/prettier.config.js"},
		"jest":          {"web/jest.config.ts"},
		"mochajs":       {"js/.mocharc.yml", "package.json", "js/null/package.json"},
		"vitest":        {"web/vitest.config.ts"},
		"pytest":        {"py/pytest.ini", "py/a/.pytest.ini", "py/pyproject.toml", "py/native/pyproject.toml", "py/tox.ini", "py/setup.cfg"},
		"mypy":          {"py/mypy.ini", "py/b/.mypy.ini", "py/pyproject.toml", "py/setup.cfg"},
		"ruff":          {"py/ruff.toml", "py/c/.ruff.toml", "py/pyproject.toml", "py/dotted/pyproject.toml"},
		"ty":            {"py/ty.toml", "py/pyproject.toml"},
		"uv":            {"py/uv.lock", "py/d/uv.toml", "py/pyproject.toml", "py/inline/pyproject.toml"},
		"biomejs":       {"web/biome.json", "web/x/biome.jsonc"},
		"playwright":    {"e2e/playwright.config.ts"},
		"android":       {"app/src/main/AndroidManifest.xml"},
		"swift":         {"Package.swift"},
		"maven":         {"java/pom.xml"},
	}
)

func recommendParityWorkspace(dag *dagger.Client, positive bool) *core.Workspace {
	dir := core.NewQuery(dag).Directory()
	if positive {
		for path, contents := range recommendParityPositive {
			dir = dir.WithNewFile(path, contents)
		}
	}
	for path, contents := range recommendParityNegative {
		dir = dir.WithNewFile(path, contents)
	}
	for _, excluded := range []string{".git", ".dagger", "node_modules", "vendor", "dist", "build", "target"} {
		for _, prefix := range []string{excluded, "py/" + excluded} {
			for path, contents := range recommendParityExcluded {
				dir = dir.WithNewFile(prefix+"/"+path, contents)
			}
		}
	}
	// Recommendations scan from the workspace root, not the working directory.
	return dir.AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/py"})
}

func catalogRecommendRegistry(t *testing.T) map[string]registryModule {
	t.Helper()
	byName := map[string]registryModule{}
	for _, mod := range loadTestdataModuleCatalog(t) {
		if mod.Recommend != nil {
			byName[mod.Name] = mod
		}
	}
	return byName
}

func TestCatalogMatchesLegacyRegistry(t *testing.T) {
	catalog := catalogRecommendRegistry(t)
	legacy := legacyModuleRegistry()
	require.Len(t, catalog, len(legacy))
	require.Len(t, recommendParityWant, len(legacy))
	for _, old := range legacy {
		mod, ok := catalog[old.Name]
		require.True(t, ok, "catalog is missing %s", old.Name)
		require.Equal(t, old.Repo, mod.Repo)
		require.Equal(t, old.Description, mod.Description)
		require.Equal(t, "module", mod.Kind)
		require.Contains(t, recommendParityWant, old.Name)
	}
}

func TestRecommendParity(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })

	catalog := catalogRecommendRegistry(t)
	for _, tc := range []struct {
		name     string
		positive bool
	}{
		{"positive", true},
		{"negative", false},
	} {
		ws := recommendParityWorkspace(dag, tc.positive).WithWorkdir(".")
		t.Run(tc.name, func(t *testing.T) {
			for _, old := range legacyModuleRegistry() {
				t.Run(old.Name, func(t *testing.T) {
					want, err := old.Recommend(ctx, ws)
					require.NoError(t, err)
					got, err := catalog[old.Name].Recommend(ctx, ws)
					require.NoError(t, err)
					// Same paths in the same order as the callback.
					require.Equal(t, want, got)
					if tc.positive {
						require.ElementsMatch(t, recommendParityWant[old.Name], got)
					} else {
						require.Empty(t, got)
					}
				})
			}
		})
	}

	t.Run("recommendModules", func(t *testing.T) {
		ws := recommendParityWorkspace(dag, true)
		mods := loadTestdataModuleCatalog(t)
		installed := map[string]bool{"ruff": true, "go": true}

		summarize := func(recs []recommendation) [][3]string {
			out := make([][3]string, len(recs))
			for i, rec := range recs {
				out[i] = [3]string{rec.Module.Name, rec.Module.Repo, rec.Match}
			}
			return out
		}
		want, err := recommendModules(ctx, ws, legacyModuleRegistry(), installed)
		require.NoError(t, err)
		got, err := recommendModules(ctx, ws, mods, installed)
		require.NoError(t, err)
		require.Equal(t, summarize(want), summarize(got))
		require.Len(t, got, len(recommendParityWant)-len(installed))
		for _, rec := range got {
			require.NotContains(t, installed, rec.Module.Name)
		}
	})
}

func TestRecommendCatalogUnknownMatcher(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })

	mods, err := parseModuleCatalog([]byte(`{
		"version": 1,
		"modules": [
			{"name": "mixed", "ref": "example.com/mixed", "recommend": {"anyOf": [
				{"glob": "**/go.mod", "yamlKey": "mocha"},
				{"glob": "**/pom.xml"}
			]}},
			{"name": "future", "ref": "example.com/future", "recommend": {"anyOf": [
				{"fileContains": "mocha"}
			]}},
			{"name": "newRule", "ref": "example.com/new-rule", "recommend": {
				"anyOf": [{"glob": "**/go.mod"}],
				"noneOf": [{"glob": "**/go.mod"}]
			}}
		]
	}`))
	require.NoError(t, err)
	require.Len(t, mods, 3)

	ws := recommendParityWorkspace(dag, true)
	recs, err := recommendModules(ctx, ws, mods, nil)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "mixed", recs[0].Module.Name)
	require.Equal(t, "java/pom.xml", recs[0].Match)
}

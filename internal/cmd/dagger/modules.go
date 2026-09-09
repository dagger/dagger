package daggercmd

import (
	"context"

	"dagger.io/dagger"
)

// registryModule is one entry in the searchable module registry.
type registryModule struct {
	Name        string
	Description string
	Repo        string
	Aliases     []string
	Recommend   RecommendFn
}

func loadModuleRegistry() []registryModule {
	return []registryModule{
		{
			Name:        "go",
			Description: "Build, test, and lint Go projects with the Go toolchain",
			Repo:        "dagger.io/go",
			Recommend:   SimpleRecommend("**/go.mod"),
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
				matches, err := SimpleRecommend("**/.mocharc.*")(ctx, ws)
				if err != nil {
					return nil, err
				}
				configs, err := recommendConfigFiles(ctx, ws, "**/package.json", hasMochaProperty)
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
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
			Recommend: func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
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
	}
}

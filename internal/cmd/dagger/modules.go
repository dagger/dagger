package daggercmd

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
			Recommend:   SimpleRecommend("**/pytest.ini", "**/pyproject.toml"),
		},
		{
			Name:        "mypy",
			Description: "Type-check Python projects with mypy",
			Repo:        "dagger.io/python/mypy",
		},
		{
			Name:        "ruff",
			Description: "Lint and format Python projects with Ruff",
			Repo:        "dagger.io/python/ruff",
		},
		{
			Name:        "ty",
			Description: "Type-check Python projects with ty",
			Repo:        "dagger.io/python/ty",
		},
		{
			Name:        "uv",
			Description: "Lock, build, and audit Python projects with uv",
			Repo:        "dagger.io/python/uv",
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

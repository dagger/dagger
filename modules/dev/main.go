package main

import (
	"context"
	"dagger/dev/internal/dagger"
	_ "embed"
)

type Dev struct {
	Source      *dagger.Directory
	GithubToken *dagger.Secret
}

//go:embed prompt.md
var prompt string

func New(
	// +optional
	// +defaultPath="/"
	// +ignore=[
	//   "bin",
	//   "**/node_modules",
	//   "**/.venv",
	//   "**/__pycache__",
	//   "docs/node_modules",
	//   "sdk/typescript/node_modules",
	//   "sdk/typescript/dist",
	//   "sdk/rust/examples/backend/target",
	//   "sdk/rust/target",
	//   "sdk/php/vendor"
	// ]
	source *dagger.Directory,

	// GitHub token to use for fetching issue/PR comments
	// +optional
	githubToken *dagger.Secret,
) *Dev {
	return &Dev{
		Source:      source,
		GithubToken: githubToken,
	}
}

// Start a coding agent for the Dagger project.
func (dev *Dev) Agent(
	ctx context.Context,
) (*dagger.LLM, error) {
	src := dev.Source

	gopls := dag.Go(dagger.GoOpts{Source: src}).Base().
		WithExec([]string{"go", "install", "golang.org/x/tools/gopls@latest"}).
		WithDirectory("/workspace", src).
		WithWorkdir("/workspace").
		WithDefaultArgs([]string{"gopls", "mcp"})

	goplsInstructions, err := gopls.WithExec([]string{"gopls", "mcp", "-instructions"}).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	return dag.Doug().Agent(
		dag.LLM().
			WithEnv(
				dag.Env().
					WithCurrentModule().
					WithWorkspace(src)).
			WithSystemPrompt(goplsInstructions).
			WithSystemPrompt(prompt).
			WithMCPServer("gopls", gopls.AsService()),
	), nil
}

// Run the tests, or a subset of tests.
func (dev *Dev) Test(
	ctx context.Context,
	// Filter the test suite, e.g. TestDirectory, TestContainer, or
	// 'TestDirectory|TestContainer' for both.
	// +optional
	suite,
	// Filter a sub-test of the test suite, e.g. FindUp/current.
	// +optional
	filter string,
) error {
	var err error
	if suite == "" {
		_, err = dag.DaggerDev().Test().All(ctx)
	} else {
		_, err = dag.DaggerDev().Test().Specific(ctx, dagger.DaggerDevTestSpecificOpts{Run: suite + "/" + filter})
	}
	return err
}

// Run a git command and return its output.
func (dev *Dev) Git(ctx context.Context, args []string) (string, error) {
	return dev.sandbox().
		WithExec(append([]string{"git"}, args...)).
		CombinedOutput(ctx)
}

// Run a gh command and return its output.
func (dev *Dev) Github(ctx context.Context, args []string) (string, error) {
	ctr := dev.sandbox()
	if dev.GithubToken != nil {
		ctr = ctr.WithSecretVariable("GITHUB_TOKEN", dev.GithubToken)
	}
	return ctr.
		WithExec(append([]string{"gh"}, args...)).
		CombinedOutput(ctx)
}

// A common environment just to minimize building for utilities like RunGit,
// RunGithub, etc.
//
// We don't expose this directly and instead expose wrappers just to keep the
// agent from going wild and relying too much on the shell.
func (dev *Dev) sandbox() *dagger.Container {
	return dag.Wolfi().Container(dagger.WolfiContainerOpts{
		Packages: []string{"bash", "git", "gh"},
	}).
		WithWorkdir("/workspace").
		WithDirectory("/workspace", dev.Source)
}

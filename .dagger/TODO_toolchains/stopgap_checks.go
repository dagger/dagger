

// Lint the Rust SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (r RustSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("check format", func(ctx context.Context) error {
			_, err := r.CheckFormat(ctx)
			return err
		}).
		WithJob("check compilation", func(ctx context.Context) error {
			_, err := r.CheckCompilation(ctx)
			return err
		}).
		Run(ctx)
}

// Lint the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("typescript", func(ctx context.Context) error {
			_, err := t.LintTypescript(ctx)
			return err
		}).
		WithJob("docs snippets", func(ctx context.Context) error {
			_, err := t.LintDocsSnippets(ctx)
			return err
		}).
		Run(ctx)
}

// Test the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Test(ctx context.Context) error {
	return parallel.New().
		WithJob("node", func(ctx context.Context) error {
			_, err := t.TestNode(ctx)
			return err
		}).
		WithJob("bun", func(ctx context.Context) error {
			_, err := t.TestBun(ctx)
			return err
		}).
		Run(ctx)
}

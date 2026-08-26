package main

import (
	"context"
	"fmt"

	"dagger/tests/internal/dagger"
)

type Tests struct{}

// GenerateVersion verifies that a rolling snapshot is replaced in place.
func (m *Tests) GenerateVersion(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) error {
	const (
		markerPath = "docs/current_docs/generate-version-integration.mdx"
		stalePath  = "docs/versioned_docs/version-1.0-beta/stale.mdx"
	)

	base := source.WithNewFile(stalePath, "stale\n")
	versions, err := base.File("docs/versions.json").Contents(ctx)
	if err != nil {
		return err
	}

	release := dag.Directory().
		WithDirectory("docs/current_docs", source.Directory("docs/current_docs")).
		WithFile("docs/sidebars.ts", source.File("docs/sidebars.ts")).
		WithNewFile(markerPath, "---\ntitle: Integration test\n---\n")
	ref := dag.Container().
		From("alpine/git").
		WithoutEntrypoint().
		WithDirectory("/repo", release).
		WithWorkdir("/repo").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "config", "user.email", "docs@example.com"}).
		WithExec([]string{"git", "config", "user.name", "Docs test"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "docs fixture"}).
		WithExec([]string{"git", "tag", "v1.0.0-beta.42"}).
		Directory("/repo").
		AsGit().
		Tag("v1.0.0-beta.42")

	after := dag.DocsDev(dagger.DocsDevOpts{Source: base}).
		GenerateVersion(ref).
		After()
	generatedMarker := "docs/versioned_docs/version-1.0-beta/generate-version-integration.mdx"
	exists, err := after.Exists(ctx, generatedMarker)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("generated snapshot is missing %s", generatedMarker)
	}
	exists, err = after.Exists(ctx, stalePath)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("generated snapshot retained stale file %s", stalePath)
	}
	exists, err = after.Exists(ctx, markerPath)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("generated snapshot leaked into %s", markerPath)
	}

	afterVersions, err := after.File("docs/versions.json").Contents(ctx)
	if err != nil {
		return err
	}
	if afterVersions != versions {
		return fmt.Errorf("rolling snapshot changed docs/versions.json")
	}
	return nil
}

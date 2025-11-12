// Tests for Docusaurus module
//
// Testing pattern adopeted from https://github.com/sagikazarmark/daggerverse
package main

import (
	"context"
	"dagger/tests/internal/dagger"
	"fmt"
	"log/slog"

	"github.com/sourcegraph/conc/pool"
)

type Tests struct{}

// All executes all tests
func (m *Tests) All(ctx context.Context) error {
	p := pool.New().WithErrors().WithContext(ctx)

	p.Go(m.Basic)

	return p.Wait()
}

func (m *Tests) Basic(ctx context.Context) error {
	dir := dag.Git("https://github.com/dagger/dagger").Head().Tree()

	site := dag.
		Docusaurus(
			dir,
			dagger.DocusaurusOpts{Dir: "/docs", DisableCache: true, Yarn: true},
		).
		Build()

	entries, err := site.Entries(ctx)

	slog.Debug("%s", entries)

	// handle case where directory was being copied instead of its contents
	if len(entries) <= 1 {
		return fmt.Errorf("entries should be more than 1, found: %s", entries)
	}

	return err
}

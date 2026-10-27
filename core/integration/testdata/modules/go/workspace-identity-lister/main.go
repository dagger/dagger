// A module that other modules call with the workspace, so a test can reuse
// the same function from clients that loaded different modules.
package main

import (
	"context"
	"crypto/rand"

	"dagger/lister/internal/dagger"
)

type Lister struct{}

// List lists the modules artifact discovery finds through ws. Calls with the
// same inputs are reused.
func (m *Lister) List(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
	mods, err := ws.Artifacts().Modules(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(mods))
	for _, mod := range mods {
		name, err := mod.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

// Token takes ws and returns a value that changes whenever its body runs, so
// two calls return the same value only when the second was a cache hit.
func (m *Lister) Token(ws *dagger.Workspace) string {
	return rand.Text()
}

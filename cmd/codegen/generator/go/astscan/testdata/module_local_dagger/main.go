// Fixture for the module-local generated dagger types import.
// When a module is generated under a go.mod path like "dagger/my-module",
// its dagger.gen.go lives at "dagger/my-module/internal/dagger", which is
// the import path user code uses. The scanner must accept that path as
// equivalent to "dagger.io/dagger" for type resolution.
package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// ContainerEcho returns a container that echoes its argument.
func (m *MyModule) ContainerEcho(stringArg string) *dagger.Container {
	return nil
}

// GrepDir greps a directory.
func (m *MyModule) GrepDir(ctx context.Context, directoryArg *dagger.Directory, pattern string) (string, error) {
	return "", nil
}

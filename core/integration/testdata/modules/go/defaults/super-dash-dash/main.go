// A module with a dash in its name, to test how user defaults handles that

package main

import (
	"context"
	"dagger/super-dash-dash/internal/dagger"
	"fmt"
	"strings"
)

func New(
	// +default="hello"
	greeting string,
	// +defaultPath="."
	dir *dagger.Directory,
	// +optional
	password *dagger.Secret,
	// +optional
	file *dagger.File,
) *SuperDashDash {
	return &SuperDashDash{
		Greeting: greeting,
		Dir:      dir,
		Password: password,
		File:     file,
	}
}

type SuperDashDash struct {
	Greeting string
	Dir      *dagger.Directory
	File     *dagger.File
	Password *dagger.Secret
}

func (m *SuperDashDash) Message(
	ctx context.Context,
	// +default="world"
	name string,
) (string, error) {
	return fmt.Sprintf("%s, %s!", m.Greeting, name), nil
}

// Functions with required arguments:

// List the contents of a directory
func (m *SuperDashDash) Ls(ctx context.Context, dir *dagger.Directory) ([]string, error) {
	return dir.Entries(ctx)
}

// Capitalize a string
func (m *SuperDashDash) Capitalize(s string) string {
	return strings.ToUpper(s)
}

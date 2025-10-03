package main

import (
	"context"
	"dagger/defaults/internal/dagger"
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
) *Defaults {
	return &Defaults{
		Greeting: greeting,
		Dir:      dir,
		Password: password,
		File:     file,
	}
}

type Defaults struct {
	Greeting string
	Dir      *dagger.Directory
	File     *dagger.File
	Password *dagger.Secret
}

func (m *Defaults) Message(
	ctx context.Context,
	// +default="world"
	name string,
) (string, error) {
	msg := fmt.Sprintf("%s, %s", m.Greeting, name)
	return dag.Foobar().Exclaim(ctx, msg)
}

// Functions with required arguments:

// List the contents of a directory
func (m *Defaults) Ls(ctx context.Context, dir *dagger.Directory) ([]string, error) {
	return dir.Entries(ctx)
}

// Capitalize a string
func (m *Defaults) Capitalize(s string) string {
	return strings.ToUpper(s)
}

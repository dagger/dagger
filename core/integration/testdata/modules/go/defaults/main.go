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
	// +optional
	// +ignore=["*", "!**/*.txt", "!**/*.md"]
	docs *dagger.Directory,
) *Defaults {
	return &Defaults{
		Greeting: greeting,
		Dir:      dir,
		Password: password,
		File:     file,
		Docs:     docs,
	}
}

type Defaults struct {
	Greeting string
	Dir      *dagger.Directory
	File     *dagger.File
	Password *dagger.Secret
	Docs     *dagger.Directory
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

// List the contents of text files in a directory (with an ignore applied)
func (m *Defaults) LsText(ctx context.Context,
	// +ignore=["**", "!**/*.txt", "!**/*.md"]
	dir *dagger.Directory) ([]string, error) {
	return dir.Entries(ctx)
}

// Capitalize a string
func (m *Defaults) Capitalize(s string) string {
	return strings.ToUpper(s)
}

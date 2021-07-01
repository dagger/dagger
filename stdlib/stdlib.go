package stdlib

import (
	"embed"
	"path"
)

var (
	// FS contains the filesystem of the stdlib.
	//go:embed **/*.cue **/*/*.cue
	FS embed.FS

	PackageName = "alpha.dagger.io"
	Path        = path.Join("cue.mod", "pkg", PackageName)
)

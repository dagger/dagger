// A Dagger module to manage Dagger modules super well

package main

import (
	"context"
	"dagger/supermod/internal/dagger"
)

func New(
	contextDir *dagger.Directory,
	// +optional
	path string,
) Supermod {
	if contextDir == nil {
		contextDir = dag.Directory()
	}
	return Supermod{
		ContextDir: dag.Directory().WithDirectory("/src", contextDir),
		Path:       path,
	}
}

func (m Supermod) Root() *dagger.Directory {
	return m.ContextDir.Directory(m.Path)
}

func (m Supermod) Config() *dagger.File {
	return m.Root().File("dagger.json")
}

type Supermod struct {
	ContextDir *dagger.Directory
	Path       string
}

func (m Supermod) Check(
	ctx context.Context,
) string {
	mod := m.Load()
	// is this the best way to see if `dagger functions` would pass?
	_, err := mod.Name(ctx)
	// _, err := mod.Objects(ctx)

	if err != nil {
		return m.Path + ": failed\n"
	}
	return m.Path + ": passed\n"
}

func (m Supermod) CheckAll(
	ctx context.Context,
	// +optional
	exclude []string,
) (string, error) {
	// m, err := m.DevelopAll(ctx, exclude)
	// if err != nil {
	// 	return "", err
	// }

	out := "Modules:\n"
	out += m.Check(ctx)

	submodules, err := m.Submodules(ctx, exclude)
	if err != nil {
		return "", err
	}
	for _, submodule := range submodules {
		out += submodule.Check(ctx)
	}

	return out, nil
}

func (m Supermod) Develop() Supermod {
	m.ContextDir = m.Load().GeneratedContextDirectory()
	return m
}

func (m Supermod) DevelopAll(
	ctx context.Context,
	// +optional
	exclude []string,
) (Supermod, error) {
	m = m.Develop()
	submodules, err := m.Submodules(ctx, exclude)
	if err != nil {
		return m, err
	}
	for _, submodule := range submodules {
		m.ContextDir = m.ContextDir.
			WithDirectory("", submodule.Develop().ContextDir)
	}
	return m, nil
}

func (m Supermod) Load() *dagger.Module {
	return m.ContextDir.AsModule(dagger.DirectoryAsModuleOpts{
		SourceRootPath: m.Path,
	})
}

func (m Supermod) Submodule(path string) Supermod {
	return Supermod{
		ContextDir: m.ContextDir,
		Path:       m.Path + "/" + path,
	}
}

func (m Supermod) Source() *dagger.Directory {
	return m.ContextDir.Directory(m.Path)
}

func (m Supermod) Submodules(
	ctx context.Context,
	// +optional
	exclude []string,
) ([]Supermod, error) {
	subpaths, err := dag.Dirdiff().Find(
		ctx,
		m.Root(), "dagger.json",
		dagger.DirdiffFindOpts{Exclude: exclude},
	)
	if err != nil {
		return nil, err
	}
	mods := make([]Supermod, 0, len(subpaths))
	for _, subpath := range subpaths {
		mods = append(mods, m.Submodule(subpath))
	}
	return mods, nil
}

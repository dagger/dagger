package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sync/errgroup"
)

// DirExcludes are directories from the module's source that we always want to exclude.
//
// These directories can affect the build process so we just make sure to remove
// them if found to avoid any conflicts.
var DirExcludes = []string{".venv", "sdk"}

// FileContents are files from the module's source that we always want the contents of.
//
// This is to enable a small performance optimization for loading multiple
// files concurrently rather than making blocking calls later.
var FileContents = []string{"pyproject.toml", ".python-version"}

// PyProject is the parsed pyproject.toml file.
type PyProject struct {
	Project struct {
		Name           string
		RequiresPython string `toml:"requires-python"`
	}
	Tool struct {
		Dagger UserConfig
	}
}

// Discovery is a helper to load information from the target module.
type Discovery struct {
	Config    *PyProject
	ModName   string
	ModSource *ModuleSource

	// ContextDir is a copy of the context directory from the module source.
	//
	// We add files to this directory, always joining paths with the source's
	// subpath. We could use modSource.Directory("") for that if it was read-only,
	// but since we have to mount the context directory in the end, rather than
	// mounting the context dir and then mounting the forked source dir on top,
	// we fork the context dir instead so there's only one mount in the end.
	ContextDir *Directory

	// SubPath is the relative path from the context directory to the source directory.
	SubPath string

	// FileSet is a set of file names from an initial Entries() call for quick lookups.
	FileSet map[string]struct{}

	// Files is a map of file names to their contents.
	Files map[string]string

	// IsInit is true if the module is new and we need to create files from
	// the template (dagger init). It's assumed that this is the case if
	// there's no pyproject.toml file.
	IsInit bool

	// EnableCustomConfig is a flag to enable or disable the discovery of custom
	// configuration, either from loading pyproject.toml or reacting to the
	// the presence of certain files like .python-version.
	EnableCustomConfig bool
}

func NewDiscovery(cfg UserConfig) *Discovery {
	proj := PyProject{}
	proj.Tool.Dagger = cfg
	return &Discovery{
		Config:  &proj,
		FileSet: make(map[string]struct{}),
		Files:   make(map[string]string),

		// Custom config can only be disabled by an extension module.
		EnableCustomConfig: true,
	}
}

// UserConfig is the configuration the user can set in pyproject.toml, under
// the "tool.dagger" table.
func (d *Discovery) UserConfig() *UserConfig {
	return &d.Config.Tool.Dagger
}

// HasFile returns true if the file exists in the original module's source directory.
func (d *Discovery) HasFile(name string) bool {
	_, ok := d.FileSet[name]
	return ok
}

// AddNewFile adds a new file, with contents, to the module's source.
func (d *Discovery) AddNewFile(name, contents string) {
	d.ContextDir = d.ContextDir.WithNewFile(path.Join(d.SubPath, name), contents)
}

// AddFile adds a file to the module's source.
func (d *Discovery) AddFile(name string, file *File) {
	d.ContextDir = d.ContextDir.WithFile(path.Join(d.SubPath, name), file)
}

// GetFile returns a file from the module's source.
func (d *Discovery) GetFile(name string) *File {
	return d.ContextDir.File(path.Join(d.SubPath, name))
}

// AddLockFile adds a lock file to the module's source.
//
// This also adds to the initial file set so it's detected by the SDK installation step.
func (d *Discovery) AddLockFile(lock *File) {
	d.AddFile(LockFilePath, lock)
	d.FileSet[LockFilePath] = struct{}{}
}

// AddDirectory adds a directory to the module's source.
func (d *Discovery) AddDirectory(name string, dir *Directory) {
	d.ContextDir = d.ContextDir.WithDirectory(path.Join(d.SubPath, name), dir)
}

// We could use modSource.Directory("") but we'll need to use the
// context directory in GeneratedCode later, so rather than trying
// to replace the source directory in the context directory, we'll
// just use the context directory with subpath everywhere.
func (d *Discovery) Source() *Directory {
	return d.ContextDir.Directory(d.SubPath)
}

// Load reads from the module source files and metadata.
//
// This is intended to make all the necessary API calls as efficiently as possibly
// with concurrency early on, to avoid unnecessary blocking calls later.
func (d *Discovery) Load(ctx context.Context, modSource *ModuleSource) error {
	d.ModSource = modSource
	d.ContextDir = modSource.ContextDirectory()

	type loadFunc func(context.Context) error

	tasks := []loadFunc{
		d.loadModInfo,
		d.loadFiles,
		d.loadConfig,
	}

	for _, task := range tasks {
		if err := task(ctx); err != nil {
			return err
		}
	}

	d.setBaseImage()

	return nil
}

// loadModInfo loads the module's metadata.
func (d *Discovery) loadModInfo(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)

	doneSubPath := make(chan struct{})

	eg.Go(func() error {
		defer close(doneSubPath)
		p, err := d.ModSource.SourceSubpath(gctx)
		if err != nil {
			return fmt.Errorf("get module source subpath: %w", err)
		}
		d.SubPath = p
		return nil
	})

	eg.Go(func() error {
		// d.Source() depends on SubPath
		<-doneSubPath
		entries, _ := d.Source().Entries(gctx)
		for _, entry := range entries {
			d.FileSet[entry] = struct{}{}
		}
		return nil
	})

	eg.Go(func() error {
		modName, err := d.ModSource.ModuleOriginalName(gctx)
		if err != nil {
			return fmt.Errorf("get module name: %w", err)
		}
		d.ModName = modName
		return nil
	})

	// TODO: Provide runtime modules with a boolean to indicate whether the
	// module is new or not. Could be `dagger init --sdk` or `dagger develop --sdk`.
	//
	// With `dagger init` we can check for the presence of the dagger.json file,
	// which is only being created after this code runs, but in `dagger develop`,
	// the CLI changes the "sdk" field in dagger.json before loading the module.
	//
	// The boolean could be provided to the runtime module's constructor,
	// the codegen function, or call a new and specific function only when using
	// `--sdk` in the CLI, like `Init()`.

	eg.Go(func() error {
		// If there's no dagger.json file, it's definitely a new module
		// (dagger init).
		exists, err := d.ModSource.ConfigExists(gctx)
		if err != nil {
			return fmt.Errorf("check if config exists: %w", err)
		}
		if !exists {
			d.IsInit = true
		}
		return nil
	})

	return eg.Wait()
}

// loadFiles loads the contents of certain module source files.
func (d *Discovery) loadFiles(ctx context.Context) error {
	// These paths should be in "exclude" in dagger.json.
	// Let's remove them just in case, to avoid conflicts.
	for _, exclude := range DirExcludes {
		if d.HasFile(exclude) {
			d.ContextDir = d.ContextDir.WithoutDirectory(
				path.Join(d.SubPath, exclude),
			)
		}
	}

	// If there's a dagger.json and no pyproject.toml, it's an init'ed module
	// adding sources (`dagger develop --sdk`).
	if !d.IsInit && !d.HasFile("pyproject.toml") {
		d.IsInit = true
	}

	eg, gctx := errgroup.WithContext(ctx)

	if d.EnableCustomConfig {
		for _, name := range FileContents {
			name := name
			if d.HasFile(name) {
				eg.Go(func() error {
					contents, err := d.GetFile(name).Contents(gctx)
					if err != nil {
						return fmt.Errorf("get file contents of %q: %w", name, err)
					}
					d.Files[name] = strings.TrimSpace(contents)
					return nil
				})
			}
		}
	}

	// TODO: This can be a performance bottleneck for large directories,
	// which can happen even unintentionally by not excluding certain
	// patterns from module load.
	eg.Go(func() error {
		// We'll use a glob pattern in fileSet to check for the existence of
		// python files later. The error is normal when the target directory
		// on `dagger init` doesn't exist, but just ignore otherwise (best
		// effort).
		entries, err := d.Source().Glob(gctx, "**/*.py")
		if len(entries) > 0 {
			d.FileSet["*.py"] = struct{}{}
		} else if err == nil && !d.IsInit {
			// This can also happen on `dagger develop --sdk` if there's also
			// a pyproject.toml present to customize the base container.
			return fmt.Errorf("no python files found in module source")
		}
		return nil
	})

	return eg.Wait()
}

// loadConfig loads the pyproject.toml file.
func (d *Discovery) loadConfig(ctx context.Context) error {
	if contents, ok := d.Files["pyproject.toml"]; ok {
		return toml.Unmarshal([]byte(contents), d.Config)
	}
	return nil
}

// findPythonVersion returns the python version for the base container image.
//
// Overriding the default version is supported on a best effort:
// 1. Check .python-version contents
// 2. Check pinned version in requires-python (in pyproject.toml)
// 3. Use the default version
func (d *Discovery) findPythonVersion() string {
	if version, ok := d.Files[".python-version"]; ok {
		return version
	}
	// NB: In pyproject.toml, the "requires-python" option refers to a minimum
	// version because it's meant for checking if the (already installed)
	// Python version in the environment is compatible with what a library
	// supports. If it's set, we'll use it as a fallback to decide which
	// version to install.
	minimum := strings.TrimSpace(d.Config.Project.RequiresPython)

	// With ">=" or a relaxed "==" we don't want to go search for the latest
	// version here anyway but we know that as a minimum it'll be supported.
	if strings.HasPrefix(minimum, "==") || strings.HasPrefix(minimum, ">=") {
		return strings.TrimSpace(minimum[2:])
	}

	return DefaultVersion
}

// setBaseImage sets the image reference for the base container image
//
// If not configured, will use default image.
//
// It's possible to override it in pyproject.toml:
// ```toml
// [tool.dagger]
// base-image = "acme/my-python:3.11"
// ```
//
// This can be useful to add customizations to the base image, such as
// additional system dependencies, or just to use a different Python
// version with full image digest.
//
// WARNING: Using an image that deviates from the official slim Python image
// is not supported and may lead to unexpected behavior. Use at own risk.
func (d *Discovery) setBaseImage() {
	cfg := d.UserConfig()
	imageRef := cfg.BaseImage

	if imageRef == "" {
		version := d.findPythonVersion()
		imageRef = fmt.Sprintf(DefaultImage, version)
	}

	if imageRef == fmt.Sprintf(DefaultImage, DefaultVersion) {
		imageRef = fmt.Sprintf("%s@%s", imageRef, DefaultDigest)
	}

	cfg.BaseImage = imageRef
}

package main

import (
	"context"
	"fmt"
	"path"
	"python-sdk/internal/dagger"
	"strings"
	"sync"

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

// Uv config bits we'd like to consume.
type UvConfig struct {
	Index []UvIndexConfig `toml:"index"`
}

type UvIndexConfig struct {
	Name    string `toml:"name"`
	URL     string `toml:"url"`
	Default bool   `toml:"default"`
}

// PyProject is the parsed pyproject.toml file.
type PyProject struct {
	Project struct {
		Name           string
		RequiresPython string `toml:"requires-python"`
	}
	Tool struct {
		Uv     UvConfig
		Dagger UserConfig
	}
}

// Discovery is a helper to load information from the target module.
type Discovery struct {
	Config    *PyProject
	ModName   string
	ModSource *dagger.ModuleSource

	// Images is a map of container image names to their addresses.
	Images map[string]*Image

	// ContextDir is a copy of the context directory from the module source.
	//
	// We add files to this directory, always joining paths with the source's
	// subpath. We could use modSource.Directory("") for that if it was read-only,
	// but since we have to mount the context directory in the end, rather than
	// mounting the context dir and then mounting the forked source dir on top,
	// we fork the context dir instead so there's only one mount in the end.
	ContextDir *dagger.Directory

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

	// Used to synchronize updates.
	mu sync.Mutex
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

// GetImage returns the container image address for the given name.
func (d *Discovery) GetImage(name string) (*Image, error) {
	if len(d.Images) == 0 {
		images, err := extractImages()
		if err != nil {
			return nil, fmt.Errorf("get container image addresses: %w", err)
		}
		d.Images = images
	}
	image, ok := d.Images[name]
	if !ok {
		return nil, fmt.Errorf("%q container image address not found", name)
	}
	return image, nil
}

// UserConfig is the configuration the user can set in pyproject.toml, under
// the "tool.dagger" table.
func (d *Discovery) UserConfig() *UserConfig {
	return &d.Config.Tool.Dagger
}

func (d *Discovery) UvConfig() *UvConfig {
	return &d.Config.Tool.Uv
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
func (d *Discovery) AddFile(name string, file *dagger.File) {
	d.ContextDir = d.ContextDir.WithFile(path.Join(d.SubPath, name), file)
}

// GetFile returns a file from the module's source.
func (d *Discovery) GetFile(name string) *dagger.File {
	return d.ContextDir.File(path.Join(d.SubPath, name))
}

// AddLockFile adds a lock file to the module's source.
//
// This also adds to the initial file set so it's detected by the SDK installation step.
func (d *Discovery) AddLockFile(lock *dagger.File) {
	d.AddFile(PipCompileLock, lock)
	d.FileSet[PipCompileLock] = struct{}{}
}

// UseUvLock returns true if the runtime should expect a uv.lock file.
func (d *Discovery) UseUvLock() bool {
	return d.UserConfig().UseUv && (d.HasFile(UvLock) || !d.HasFile(PipCompileLock) && d.IsInit)
}

// AddDirectory adds a directory to the module's source.
func (d *Discovery) AddDirectory(name string, dir *dagger.Directory) {
	d.ContextDir = d.ContextDir.WithDirectory(path.Join(d.SubPath, name), dir)
}

// We could use modSource.Directory("") but we'll need to use the
// context directory in GeneratedCode later, so rather than trying
// to replace the source directory in the context directory, we'll
// just use the context directory with subpath everywhere.
func (d *Discovery) Source() *dagger.Directory {
	return d.ContextDir.Directory(d.SubPath)
}

// Load reads from the module source files and metadata.
//
// This is intended to make all the necessary API calls as efficiently as possibly
// with concurrency early on, to avoid unnecessary blocking calls later.
func (d *Discovery) Load(ctx context.Context, modSource *dagger.ModuleSource) error {
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
		d.mu.Lock()
		d.SubPath = p
		d.mu.Unlock()
		return nil
	})

	eg.Go(func() error {
		// d.Source() depends on SubPath
		<-doneSubPath
		entries, _ := d.Source().Entries(gctx)
		d.mu.Lock()
		for _, entry := range entries {
			d.FileSet[entry] = struct{}{}
		}
		d.mu.Unlock()
		return nil
	})

	eg.Go(func() error {
		modName, err := d.ModSource.ModuleOriginalName(gctx)
		if err != nil {
			return fmt.Errorf("get module name: %w", err)
		}
		d.mu.Lock()
		d.ModName = modName
		d.mu.Unlock()
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
			d.mu.Lock()
			d.IsInit = true
			d.mu.Unlock()
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
					d.mu.Lock()
					d.Files[name] = strings.TrimSpace(contents)
					d.mu.Unlock()
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
			d.mu.Lock()
			d.FileSet["*.py"] = struct{}{}
			d.mu.Unlock()
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
	contents, ok := d.Files["pyproject.toml"]
	if !ok {
		return nil
	}

	if err := toml.Unmarshal([]byte(contents), d.Config); err != nil {
		return err
	}

	// Get image addresses from the Dockerfile to combine with possible
	// overrides in pyproject.toml.
	baseImage, err := d.GetImage(BaseImageName)
	if err != nil {
		return err
	}
	uvImage, err := d.GetImage(UvImageName)
	if err != nil {
		return err
	}

	cfg := d.UserConfig()

	// The base image can change if the requested Python version is different
	// than the default, or if the user has set a custom base image.
	base, err := d.parseBaseImage(cfg.BaseImage, baseImage)
	if err != nil {
		return err
	}

	// If the image name and tag is the same as the default, reuse the default
	// because of the digest.
	if base != nil && !base.Equal(baseImage) {
		baseImage = base
	}

	// Uv's image tag matches the version exactly.
	if cfg.UvVersion != "" && cfg.UvVersion != uvImage.Tag() {
		uv, err := uvImage.WithTag(cfg.UvVersion)
		if err != nil {
			return err
		}
		uvImage = uv
	}

	d.Images[BaseImageName] = baseImage
	d.Images[UvImageName] = uvImage

	return nil
}

// findPythonVersion looks for a Python version pin in either `.python-version`
// or `requires-python` in pyproject.toml.
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

	return ""
}

// parseBaseImage parses user configuration to look for an override of the base image.
//
// Base image is constructed on a best effort:
// 1. Override in custom `base-image` setting (in pyproject.toml)
// 2. Check `.python-version` contents
// 3. Check pinned version in requires-python (in pyproject.toml)
// 4. Use the default base image
//
// To completely override the base image in pyproject.toml:
// ```toml
// [tool.dagger]
// base-image = "acme/my-python:3.11"
// ```
// This can be useful to add customizations to the base image, such as
// additional system dependencies, or just to use a different Python
// version with full image digest.
//
// WARNING: Using an image that deviates from the official slim Python image
// is not supported and may lead to unexpected behavior. Use at own risk.
func (d *Discovery) parseBaseImage(ref string, defaultImage *Image) (*Image, error) {
	if ref == "" {
		version := d.findPythonVersion()
		if version == "" {
			return nil, nil
		}
		tag := fmt.Sprintf("%s-slim", version)
		return defaultImage.WithTag(tag)
	}
	return parseImageRef(ref)
}

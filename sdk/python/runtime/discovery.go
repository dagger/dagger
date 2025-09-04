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
	Sources struct {
		Dagger UvSource `toml:"dagger-io"`
	} `toml:"sources"`

	// Index is a list of uv index configurations.
	// Ssee [uv v0.4.23](https://github.com/astral-sh/uv/releases/tag/0.4.23)
	Index []UvIndexConfig `toml:"index"`
}

type UvSource struct {
	Path     string `toml:"path"`
	Editable bool   `toml:"editable"`
}

type UvIndexConfig struct {
	Name    string `toml:"name"`
	URL     string `toml:"url"`
	Default bool   `toml:"default"`
}

// PyProject is the parsed pyproject.toml file.
type PyProject struct {
	Project struct {
		Name           string   `toml:"name"`
		RequiresPython string   `toml:"requires-python"`
		Dependencies   []string `toml:"dependencies"`
	} `toml:"project"`
	Tool struct {
		Uv     UvConfig   `toml:"uv"`
		Dagger UserConfig `toml:"dagger"`
	} `toml:"tool"`
}

// Discovery is a helper to load information from the target module.
type Discovery struct {
	Config PyProject

	// Images is a map of container image names to their addresses.
	Images map[string]Image

	// DefaultImages is a map of default container image addresses.
	DefaultImages map[string]Image

	// FileSet is a set of file names in the SDK source directory.
	SdkFileSet map[string]struct{}

	// FileSet is a set of file names from an initial Entries() call for quick lookups.
	FileSet map[string]struct{}

	// Files is a map of file names to their contents.
	Files map[string]string

	// EnableCustomConfig is a flag to enable or disable the discovery of custom
	// configuration, either from loading pyproject.toml or reacting to the
	// the presence of certain files like .python-version.
	EnableCustomConfig bool

	// Used to synchronize updates.
	mu sync.Mutex
}

func NewDiscovery(cfg UserConfig) (*Discovery, error) {
	proj := PyProject{}
	proj.Tool.Dagger = cfg

	// Get image addresses from the Dockerfile
	images, err := extractImages()
	if err != nil {
		return nil, fmt.Errorf("get default container image addresses: %w", err)
	}

	return &Discovery{
		Config:        proj,
		DefaultImages: images,
		Images:        make(map[string]Image),
		SdkFileSet:    make(map[string]struct{}),
		FileSet:       make(map[string]struct{}),
		Files:         make(map[string]string),

		// Custom config can only be disabled by an extension module.
		EnableCustomConfig: true,
	}, nil
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

// SdkHasFile returns true if the file exists in the SDK's source directory.
func (d *Discovery) SdkHasFile(name string) bool {
	_, ok := d.SdkFileSet[name]
	return ok
}

// AddNewFile adds a new file, with contents, to the module's source.
func (m *PythonSdk) AddNewFile(name, contents string) {
	m.ContextDir = m.ContextDir.WithNewFile(path.Join(m.SubPath, name), contents)
}

// AddFile adds a file to the module's source.
func (m *PythonSdk) AddFile(name string, file *dagger.File) {
	m.ContextDir = m.ContextDir.WithFile(path.Join(m.SubPath, name), file)
}

// GetFile returns a file from the module's source.
func (m *PythonSdk) GetFile(name string) *dagger.File {
	return m.ContextDir.File(path.Join(m.SubPath, name))
}

// UseUvLock returns true if the runtime should expect a uv.lock file.
func (m *PythonSdk) UseUvLock() bool {
	d := m.Discovery
	return m.UseUv() && (d.HasFile(UvLock) || !d.HasFile(PipCompileLock) && m.IsInit)
}

// AddDirectory adds a directory to the module's source.
func (m *PythonSdk) AddDirectory(name string, dir *dagger.Directory) {
	m.ContextDir = m.ContextDir.WithDirectory(path.Join(m.SubPath, name), dir)
}

// We could use modSource.Directory("") but we'll need to use the
// context directory in GeneratedCode later, so rather than trying
// to replace the source directory in the context directory, we'll
// just use the context directory with subpath everywhere.
func (m *PythonSdk) Source() *dagger.Directory {
	return m.ContextDir.Directory(m.SubPath)
}

// getImage returns the container image address for the given name.
func (m *PythonSdk) getImage(name string) Image {
	image, exists := m.Discovery.Images[name]
	if !exists {
		return m.Discovery.DefaultImages[name]
	}
	return image
}

// Load reads from the module source files and metadata.
//
// This is intended to make all the necessary API calls as efficiently as possibly
// with concurrency early on, to avoid unnecessary blocking calls later.
func (d *Discovery) Load(ctx context.Context, m *PythonSdk) error {
	type loadFunc func(context.Context, *PythonSdk) error

	tasks := []loadFunc{
		d.loadModInfo,
		d.loadFiles,
		d.loadConfig,
	}

	for _, task := range tasks {
		if err := task(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

// loadModInfo loads the module's metadata.
func (d *Discovery) loadModInfo(ctx context.Context, m *PythonSdk) error {
	eg, gctx := errgroup.WithContext(ctx)

	doneSubPath := make(chan struct{})

	eg.Go(func() error {
		defer close(doneSubPath)
		p, err := m.ModSource.SourceSubpath(gctx)
		if err != nil {
			return fmt.Errorf("get module source subpath: %w", err)
		}
		d.mu.Lock()
		m.SubPath = p
		d.mu.Unlock()
		return nil
	})

	eg.Go(func() error {
		// m.Source() depends on SubPath
		<-doneSubPath
		entries, _ := m.Source().Entries(gctx)
		d.mu.Lock()
		for _, entry := range entries {
			d.FileSet[entry] = struct{}{}
		}
		d.mu.Unlock()
		return nil
	})

	eg.Go(func() error {
		dig, err := m.ModSource.Digest(gctx)
		if err != nil {
			return fmt.Errorf("get module source digest: %w", err)
		}
		d.mu.Lock()
		m.ContextDirPath = path.Join(ModSourceDirPath, dig)
		d.mu.Unlock()
		return nil
	})

	eg.Go(func() error {
		modName, err := m.ModSource.ModuleOriginalName(gctx)
		if err != nil {
			return fmt.Errorf("get module name: %w", err)
		}
		d.mu.Lock()
		m.ModName = modName
		m.MainObjectName = NormalizeObjectName(modName)
		m.ProjectName = NormalizeProjectNameFromModule(modName)
		m.PackageName = NormalizePackageName(m.ProjectName)
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
		exists, err := m.ModSource.ConfigExists(gctx)
		if err != nil {
			return fmt.Errorf("check if config exists: %w", err)
		}
		if !exists {
			d.mu.Lock()
			m.IsInit = true
			d.mu.Unlock()
		}
		return nil
	})

	return eg.Wait()
}

// loadFiles loads the contents of certain module source files.
func (d *Discovery) loadFiles(ctx context.Context, m *PythonSdk) error {
	// If there's a dagger.json and no pyproject.toml, it's an init'ed module
	// adding sources (`dagger develop --sdk`).
	if !m.IsInit && !d.HasFile("pyproject.toml") {
		m.IsInit = true
	}

	// These paths should be in "exclude" in dagger.json.
	// Let's remove them just in case, to avoid conflicts.
	for _, exclude := range DirExcludes {
		if d.HasFile(exclude) {
			m.ContextDir = m.ContextDir.WithoutDirectory(
				path.Join(m.SubPath, exclude),
			)
		}
	}

	eg, gctx := errgroup.WithContext(ctx)

	if d.EnableCustomConfig {
		for _, name := range FileContents {
			if d.HasFile(name) {
				eg.Go(func() error {
					contents, err := m.GetFile(name).Contents(gctx)
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

	eg.Go(func() error {
		// We'll use a glob pattern in fileSet to check for the existence of
		// python files later. The error is normal when the target directory
		// on `dagger init` doesn't exist, but just ignore otherwise (best
		// effort).
		entries, err := m.Source().Glob(gctx, "src/**/*.py|*.py")
		if len(entries) > 0 {
			d.mu.Lock()
			d.FileSet["*.py"] = struct{}{}
			d.mu.Unlock()
		} else if err == nil && !m.IsInit {
			// This can also happen on `dagger develop --sdk` if there's also
			// a pyproject.toml present to customize the base container.
			return fmt.Errorf("no python files found in module source")
		}
		return nil
	})

	eg.Go(func() error {
		entries, _ := m.SdkSourceDir.Entries(gctx)
		d.mu.Lock()
		for _, entry := range entries {
			d.SdkFileSet[entry] = struct{}{}
		}
		// quick check to avoid an unnecessary request
		hasDist := d.SdkHasFile("dist/")
		d.mu.Unlock()

		if hasDist {
			entries, _ = m.SdkSourceDir.Glob(gctx, "dist/*")
			d.mu.Lock()
			for _, entry := range entries {
				d.SdkFileSet[entry] = struct{}{}
			}
			d.mu.Unlock()
		}

		return nil
	})

	return eg.Wait()
}

// loadConfig loads configurations from user files listed in FileContents.
func (d *Discovery) loadConfig(ctx context.Context, m *PythonSdk) error {
	// d.Files can be empty if EnableCustomConfig is false, which can be disabled
	// on extension modules. Otherwise, `pyproject.toml` can only be empty
	// on `dagger init`, in which case it will be created from template.
	contents, exists := d.Files["pyproject.toml"]
	if !exists {
		return nil
	}

	if err := toml.Unmarshal([]byte(contents), &d.Config); err != nil {
		return err
	}

	baseImage, err := d.parseBaseImage(d.DefaultImages[BaseImageName])
	if err != nil {
		return err
	}
	uvImage, err := d.parseUvImage(d.DefaultImages[UvImageName])
	if err != nil {
		return err
	}
	d.Images[BaseImageName] = baseImage
	d.Images[UvImageName] = uvImage

	// For an existing pyproject.toml, the project name may divert from the default
	if d.Config.Project.Name != "" {
		m.ProjectName = d.Config.Project.Name
		m.PackageName = NormalizePackageName(m.ProjectName)
	}

	// Only look for vendor path when uv.lock is being used
	if m.UseUvLock() {
		m.VendorPath = d.Config.Tool.Uv.Sources.Dagger.Path
	}

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
// base-image = "acme/my-python:3.13"
// ```
// This can be useful to add customizations to the base image, such as
// additional system dependencies, or just to use a different Python
// version with full image digest.
//
// WARNING: Using an image that deviates from the official slim Python image
// is not supported and may lead to unexpected behavior. Use at own risk.
func (d *Discovery) parseBaseImage(defaultImage Image) (Image, error) {
	ref := d.UserConfig().BaseImage

	if ref == "" {
		version := d.findPythonVersion()
		if version == "" {
			return defaultImage, nil
		}

		tag := fmt.Sprintf("%s-slim", version)
		image, err := defaultImage.WithTag(tag)

		// If the image name and tag is the same as the default, reuse the default
		// because of the digest.
		if err != nil || image.Equal(defaultImage) {
			return defaultImage, err
		}

		return image, nil
	}
	return NewImage(ref)
}

// parseUvImage parses user configuration to look for an override of the uv image.
//
// To override the uv image in pyproject.toml:
// ```toml
// [tool.dagger]
// uv-version = "0.6.14"
// ```
//
// Can be useful to get a newer version to fix a bug or get a new feature.
func (d *Discovery) parseUvImage(defaultImage Image) (Image, error) {
	version := d.UserConfig().UvVersion

	// Uv's image tag matches the version exactly.
	if version != "" && version != defaultImage.Tag() {
		return defaultImage.WithTag(version)
	}

	return defaultImage, nil
}

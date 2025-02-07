package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"golang.org/x/sync/errgroup"
)

// TODO: bikeshed
const returnToInitialContextPath = "-"

type shellWorkdir struct {
	// Source is an in-memory  representation of ModuleSource to produce paths
	Source shellModSource

	// ContextModules is a list of paths to modules found in the context
	ContextModules []string

	// ModuleRoot is an absolute reference to currently loaded (default) module
	ModuleRoot string

	// Path is an absolute file path, rooted at the context
	Path string
}

// shellModSource is a representation of ModelSource, used to produce paths
//
// TODO: add API field to return something like asString but with any subpath.
// This is more necessary with git sources as you need to add the version after
// joining cloneRef with rootSubpath.
type shellModSource interface {
	Ref(path string) string
	Subpath() string
	Source(dag *dagger.Client) *dagger.ModuleSource
}

type localSource struct {
	Root string
	Path string
}

func (src localSource) Ref(path string) string {
	if path == "" {
		path = src.Path
	}
	return filepath.Join(src.Root, path)
}

func (src localSource) Source(dag *dagger.Client) *dagger.ModuleSource {
	return dag.ModuleSource(src.Ref(src.Path)).ResolveFromCaller()
}

func (src localSource) Subpath() string {
	return src.Path
}

type gitSource struct {
	Root    string
	Version string
	Pin     string
	Path    string
}

func (src gitSource) Ref(path string) string {
	if path == "" {
		path = src.Path
	}
	refPath := src.Root
	subPath := filepath.Join("/", path)
	if subPath != "/" {
		refPath += subPath
	}
	if src.Version != "" {
		refPath += "@" + src.Version
	}
	return refPath
}

func (src gitSource) Source(dag *dagger.Client) *dagger.ModuleSource {
	opts := dagger.ModuleSourceOpts{}
	if src.Pin != "" {
		opts.RefPin = src.Pin
	}
	return dag.ModuleSource(src.Ref(src.Path), opts)
}

func (src gitSource) Subpath() string {
	return src.Path
}

func (wd shellWorkdir) modulePathFindUp(path string) string {
	if path == "" || path == "." {
		return ""
	}
	for _, modRoot := range wd.ContextModules {
		if path == modRoot {
			return path
		}
	}
	if path == "/" {
		return ""
	}
	return wd.modulePathFindUp(filepath.Dir(path))
}

func (h *shellCallHandler) ChangeWorkdir(ctx context.Context, path string) error {
	if path != returnToInitialContextPath && !h.HasContext() {
		conf, err := getModuleConfigurationForSourceRef(ctx, h.dag, path, true, true)
		if err != nil {
			return err
		}
		return h.setContext(ctx, conf)
	}
	return h.setPath(ctx, path)
}

func (h *shellCallHandler) setContext(ctx context.Context, conf *configuredModule) error {
	source, err := newShellSource(ctx, conf)
	if err != nil {
		return err
	}

	moduleRoot := source.Ref("")

	if conf.ModuleSourceConfigExists {
		def, err := h.getOrInitDef(moduleRoot, func() (*moduleDef, error) {
			return initializeModuleConfig(ctx, h.dag, conf)
		})
		if err != nil {
			return err
		}
		if def == nil {
			moduleRoot = ""
		}
	}

	entries, err := conf.Source.ContextDirectory().Glob(ctx, "**/"+modules.Filename)
	if err != nil {
		return err
	}

	modulePaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		modulePaths = append(modulePaths, filepath.Join("/", filepath.Dir(entry)))
	}

	h.mu.Lock()
	h.workdir = shellWorkdir{
		Source:         source,
		ModuleRoot:     moduleRoot,
		ContextModules: modulePaths,
		Path:           filepath.Join("/", source.Subpath()),
	}
	h.mu.Unlock()

	return nil
}

func newShellSource(ctx context.Context, conf *configuredModule) (shellModSource, error) {
	if conf.SourceKind == dagger.ModuleSourceKindLocalSource {
		path, err := filepath.Rel(conf.LocalContextPath, conf.LocalRootSourcePath)
		if err != nil {
			return nil, err
		}
		return localSource{
			Root: conf.LocalContextPath,
			Path: path,
		}, nil
	}

	source := conf.Source.AsGitSource()

	var root string
	var version string
	var pin string
	var path string

	eg, gctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		v, err := source.CloneRef(gctx)
		if err != nil {
			return err
		}
		root = v
		return nil
	})

	eg.Go(func() error {
		v, err := source.Version(gctx)
		if err != nil {
			return err
		}
		version = v
		return nil
	})

	eg.Go(func() error {
		v, err := source.Commit(gctx)
		if err != nil {
			return err
		}
		pin = v
		return nil
	})

	eg.Go(func() error {
		v, err := source.RootSubpath(gctx)
		if err != nil {
			return err
		}
		path = v
		return nil
	})

	if err := eg.Wait(); err != nil {
		return gitSource{}, err
	}

	return gitSource{
		Root:    root,
		Version: version,
		Pin:     pin,
		Path:    path,
	}, nil
}

func (h *shellCallHandler) setPath(ctx context.Context, path string) error {
	path, err := h.absPath(path)
	if err != nil {
		return err
	}
	dir, err := h.Directory(path)
	if err != nil {
		return err
	}
	// TODO: replace with dir.Exists() when it's implemented
	_, err = dir.Sync(ctx)
	if err != nil {
		return err
	}

	var modRef string

	h.mu.RLock()
	if modPath := h.workdir.modulePathFindUp(path); modPath != "" {
		ref := h.workdir.Source.Ref(modPath)

		if h.workdir.ModuleRoot != ref {
			modRef = ref
		}
	}
	h.mu.RUnlock()

	if modRef != "" {
		def, err := h.getOrInitDef(modRef, func() (*moduleDef, error) {
			return initializeModule(ctx, h.dag, modRef, false)
		})
		if err != nil || def == nil {
			return err
		}
	}

	h.mu.Lock()
	h.workdir.ModuleRoot = modRef
	h.workdir.Path = path
	h.mu.Unlock()
	return nil
}

func (h *shellCallHandler) absPath(path string) (string, error) {
	// TODO: bikeshed
	if path == returnToInitialContextPath {
		if _, err := h.GetModuleDef(nil); err != nil {
			return "", err
		}
		path = h.InitialPath()
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.WorkdirPath(), path)

		// path could be empty if input is empty and workdir is "/"
		if path != "" && !filepath.IsLocal(path) {
			return "", fmt.Errorf("can't escape context root: %s", h.ContextRoot())
		}

		path = filepath.Join("/", path)
	}
	return path, nil
}

func (h *shellCallHandler) Workdir() shellWorkdir {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir
}

func (h *shellCallHandler) HasContext() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir.Source != nil
}

func (h *shellCallHandler) ContextRoot() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.Source == nil {
		return ""
	}
	return h.workdir.Source.Ref("/")
}

func (h *shellCallHandler) InitialPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.Source == nil {
		return ""
	}
	return filepath.Join("/", h.workdir.Source.Subpath())
}

func (h *shellCallHandler) WorkdirPath() string {
	return strings.TrimPrefix(h.WorkdirAbsPath(), "/")
}

func (h *shellCallHandler) WorkdirAbsPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir.Path
}

func (h *shellCallHandler) Pwd() string {
	if !h.HasContext() {
		pwd, _ := os.Getwd()
		return pwd
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir.Source.Ref(h.workdir.Path)
}

func (h *shellCallHandler) ContextDirectory() (*dagger.Directory, error) {
	def, err := h.GetModuleDef(nil)
	if err != nil {
		return nil, err
	}
	return def.Conf.Source.ContextDirectory(), nil
}

func (h *shellCallHandler) Directory(path string) (*dagger.Directory, error) {
	dir, err := h.ContextDirectory()
	if err != nil {
		return nil, err
	}
	apath, err := h.absPath(path)
	if err != nil {
		return nil, err
	}
	return dir.Directory(apath), nil
}

func (h *shellCallHandler) DefaultModRef() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir.ModuleRoot
}

// IsDefaultModule returns true if the given module reference is the default loaded module
func (h *shellCallHandler) IsDefaultModule(ref string) bool {
	return ref == "" || ref == h.DefaultModRef()
}

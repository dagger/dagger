package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
)

type shellWorkdir struct {
	// absolute path on the host, or full git ref
	ContextRoot    string
	ContextModules []string

	// like contextRoot, but with subdir to module root
	ModuleRoot string

	// absolute Path, rooted at the context
	Path string
}

func (h *shellCallHandler) ChangeWorkdir(ctx context.Context, path string) error {
	if !h.Workdir().HasContext() || isGitURL(path) {
		conf, err := getModuleConfigurationForSourceRef(ctx, h.dag, path, true, true)
		if err != nil {
			return err
		}
		return h.setContext(ctx, conf)
	}

	if err := h.setPath(path); err != nil {
		return fmt.Errorf("can't change path to %q: %w", path, err)
	}

	return h.updateDefaultModule(ctx)
}

func (h *shellCallHandler) setContext(ctx context.Context, conf *configuredModule) error {
	path, err := filepath.Rel(conf.LocalContextPath, conf.LocalRootSourcePath)
	if err != nil {
		return nil
	}

	wd := shellWorkdir{
		ContextRoot: conf.LocalContextPath,
		Path:        filepath.Join("/", path),
	}

	if conf.FullyInitialized() {
		def, err := h.getOrInitDef(conf.LocalRootSourcePath, func() (*moduleDef, error) {
			return initializeModuleConfig(ctx, h.dag, conf)
		})
		if err != nil {
			return err
		}
		if def != nil {
			wd.ModuleRoot = conf.LocalRootSourcePath
		}

		entries, err := conf.Source.ContextDirectory().Glob(ctx, "**/"+modules.Filename)
		if err != nil {
			return err
		}

		wd.ContextModules = make([]string, 0, len(entries))
		for _, entry := range entries {
			wd.ContextModules = append(wd.ContextModules, filepath.Join("/", filepath.Dir(entry)))
		}
	}

	h.mu.Lock()
	h.workdir = wd
	h.mu.Unlock()

	return nil
}

func (h *shellCallHandler) setPath(path string) error {
	path, err := h.AbsPath(path)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.workdir.Path = path
	h.mu.Unlock()
	return nil
}

func (h *shellCallHandler) updateDefaultModule(ctx context.Context) error {
	var modRef string

	h.mu.RLock()
	if path := h.workdir.modulePathFindUp(h.workdir.Path); path != "" {
		modRef = h.workdir.RootSubpath(path)

		if h.workdir.ModuleRoot == modRef {
			modRef = ""
		}
	}
	h.mu.RUnlock()

	if modRef == "" {
		return nil
	}

	def, err := h.getOrInitDef(modRef, func() (*moduleDef, error) {
		return initializeModule(ctx, h.dag, modRef, false)
	})
	if err != nil || def == nil {
		return err
	}

	h.mu.Lock()
	h.workdir.ModuleRoot = modRef
	h.mu.Unlock()

	return nil
}

func isGitURL(path string) bool {
	_, err := parseGitURL(path)
	return err == nil
}

func (wd shellWorkdir) HasContext() bool {
	return wd.ContextRoot != ""
}

func (wd *shellWorkdir) RootSubpath(path string) string {
	return filepath.Join(wd.ContextRoot, path)
}

func (wd shellWorkdir) AbsPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(strings.TrimPrefix(wd.Path, "/"), path)

		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("can't escape context root")
		}

		path = filepath.Join("/", path)
	}
	return path, nil
}

func (wd shellWorkdir) FindModulePath(path string) (string, error) {
	path, err := wd.AbsPath(path)
	if err != nil {
		return "", nil
	}
	return wd.modulePathFindUp(path), nil
}

func (wd shellWorkdir) modulePathFindUp(path string) string {
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

func (h *shellCallHandler) Workdir() shellWorkdir {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir
}

func (h *shellCallHandler) AbsPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.WorkdirPath(), path)

		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("can't escape context root")
		}

		path = filepath.Join("/", path)
	}
	return path, nil
}

func (h *shellCallHandler) WorkdirPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return strings.TrimPrefix(h.workdir.Path, "/")
}

func (h *shellCallHandler) Pwd() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workdir.RootSubpath(h.workdir.Path)
}

func (h *shellCallHandler) ContextDirectory() (*dagger.Directory, error) {
	def, err := h.GetModuleDef(nil)
	if err != nil {
		return nil, err
	}
	return def.Conf.Source.ContextDirectory(), nil
}

func (h *shellCallHandler) CurrentDirectory(path string) (*dagger.Directory, error) {
	dir, err := h.ContextDirectory()
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = h.WorkdirPath()
	}
	return dir.Directory(path), nil
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

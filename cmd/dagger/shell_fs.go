package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/client/pathutil"
	"golang.org/x/sync/errgroup"
)

// shellWorkdir represents a shell working directory
//
// It is composed of a loaded module (context) and a path within the module's
// context root.
type shellWorkdir struct {
	// Context is an in-memory representation of ModuleSource, used to produce paths
	Context moduleContext

	// Path is an absolute file path, rooted at the context
	Path string

	// Module is the digest of the module source if one is currently loaded
	//
	// This serves two purposes, both as a way to signal that the context is a
	// module but also to validate it since for example, a git source may have
	// different versions/tags for the same commit.
	Module string
}

// ChangeDir changes the current working directory
func (h *shellCallHandler) ChangeDir(ctx context.Context, path string) error {
	if h.Debug() {
		shellDebug(ctx, "changeDir (before)", path, h.Workdir(), h.debugLoadedModules())

		defer func() {
			shellDebug(ctx, "changeDir (after)", path, h.Workdir(), h.debugLoadedModules())
		}()
	}

	switch path {
	case "":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.oldwd, h.wd = h.wd, h.initwd
		return nil

	case "-":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.oldwd, h.wd = h.wd, h.oldwd
		return nil
	}

	var subpath string

	def, cfg, err := h.maybeLoadModule(ctx, path)
	if err != nil {
		return err
	}
	if cfg != nil {
		subpath = cfg.Subpath
	} else {
		// if there's no module, pass original path to newWorkdir
		subpath = path
	}

	wd, err := h.newWorkdir(ctx, def, subpath)
	if err != nil {
		return fmt.Errorf("change workdir: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.oldwd, h.wd = h.wd, *wd

	return nil
}

func (h *shellCallHandler) Workdir() shellWorkdir {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd
}

func (h *shellCallHandler) workdirAbsPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Path
}

func (h *shellCallHandler) Pwd() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.ModRef(h.wd.Path)
}

func (h *shellCallHandler) modDigest() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Module
}

func (h *shellCallHandler) contextRoot() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.ModRef("/")
}

func (h *shellCallHandler) contextModRef(path string) (string, error) {
	apath, err := h.contextAbsPath(path)
	if err != nil {
		return apath, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.ModRef(apath), nil
}

func (h *shellCallHandler) contextArgRef(path string) (string, error) {
	apath, err := h.contextAbsPath(path)
	if err != nil {
		return apath, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.ArgRef(apath), nil
}

// moduleContext is an in-memory representation of a ModuleSource, used to produce paths quickly
type moduleContext interface {
	// ModRef returns an absolute reference that can be used with ModuleSource
	ModRef(subpath string) string

	// ArgRef returns an absolute reference that can be used with a Directory or File flag
	ArgRef(subpath string) string

	// Subpath returns the subpath of the module, within the context root
	Subpath() string

	// Directory returns a Directory object for the given subpath
	Directory(dag *dagger.Client, subpath string) *dagger.Directory

	// File returns a File object for the given subpath
	File(dag *dagger.Client, subpath string) *dagger.File
}

type localSourceContext struct {
	Root string
	Path string
}

func (src localSourceContext) ModRef(subpath string) string {
	if subpath == "" {
		subpath = src.Path
	}
	return filepath.Join(src.Root, subpath)
}

func (src localSourceContext) ArgRef(subpath string) string {
	return src.ModRef(subpath)
}

func (src localSourceContext) Subpath() string {
	return src.Path
}

func (src localSourceContext) Directory(dag *dagger.Client, subpath string) *dagger.Directory {
	// Don't recursively include every subdir, we're just interested in listing the entries
	// or checking if the directory exists
	return dag.Host().Directory(filepath.Join(src.Root, subpath), dagger.HostDirectoryOpts{Exclude: []string{"*/**", "!*"}})
}

func (src localSourceContext) File(dag *dagger.Client, subpath string) *dagger.File {
	return dag.Host().File(filepath.Join(src.Root, subpath))
}

type gitSourceContext struct {
	Root    string
	Path    string
	Version string
	Pin     string
}

func (src gitSourceContext) ModRef(subpath string) string {
	if subpath == "" {
		subpath = src.Path
	}
	refPath := src.Root
	subPath := filepath.Join("/", subpath)
	if subPath != "/" {
		refPath += subPath
	}
	if src.Version != "" {
		refPath += "@" + src.Version
	}
	return refPath
}

func (src gitSourceContext) ArgRef(subpath string) string {
	if subpath == "" {
		subpath = src.Path
	}
	refPath := src.Root
	// Won't work without a scheme, except if there's a user.
	// For example: git@github.com/dagger/dagger
	if !strings.Contains(refPath, "://") && !strings.Contains(refPath, "@") {
		// Default to https, but need to convert this kind of URL:
		// `github.com:dagger/dagger` into `github.com/dagger/dagger`
		refPath = "https://" + strings.Replace(refPath, ":", "/", 1)
	}
	frag := src.Version
	if frag == "" {
		frag = src.Pin
	}
	subPath := filepath.Join("/", subpath)
	if subPath != "/" {
		frag += ":" + subPath
	}
	if frag != "" {
		refPath += "#" + frag
	}
	return refPath
}

func (src gitSourceContext) Subpath() string {
	return src.Path
}

func (src gitSourceContext) context(dag *dagger.Client) *dagger.Directory {
	gitOpts := dagger.GitOpts{
		KeepGitDir: true,
	}
	git := dag.Git(src.Root, gitOpts)
	var gitRef *dagger.GitRef
	if src.Pin != "" {
		gitRef = git.Ref(src.Pin)
	} else if src.Version != "" {
		gitRef = git.Ref(src.Version)
	} else {
		gitRef = git.Head()
	}
	return gitRef.Tree()
}

func (src gitSourceContext) Directory(dag *dagger.Client, subpath string) *dagger.Directory {
	return src.context(dag).Directory(subpath)
}

func (src gitSourceContext) File(dag *dagger.Client, subpath string) *dagger.File {
	return src.context(dag).File(subpath)
}

func (h *shellCallHandler) maybeLoadModule(ctx context.Context, path string) (*moduleDef, *configuredModule, error) {
	cfg, err := h.parseModRef(ctx, path)
	if err != nil {
		return nil, cfg, fmt.Errorf("find module %q: %w", path, err)
	}
	if cfg == nil {
		return nil, nil, nil
	}
	def, err := h.getOrInitDef(cfg.Digest, func() (*moduleDef, error) {
		return initializeModule(ctx, h.dag, cfg.Ref, cfg.Source)
	})

	return def, cfg, err
}

// parseModRef transforms user input into a full module reference that can be used with
// dag.ModuleSource().
func (h *shellCallHandler) parseModRef(ctx context.Context, path string) (rcfg *configuredModule, rerr error) {
	if h.Debug() {
		shellDebug(ctx, "parseModRef (before)", path)

		defer func() {
			shellDebug(ctx, "parseModRef (after)", path, rcfg)
		}()
	}

	h.mu.RLock()
	context := h.wd.Context
	h.mu.RUnlock()

	// If no module loaded, let API handle it
	if context == nil {
		return h.getModuleConfig(ctx, path)
	}

	// Let's see if it's a relative path within the current context first
	apath, err := h.contextAbsPath(path)
	if err != nil {
		return nil, err
	}
	ref := context.ModRef(apath)

	if _, ok := context.(localSourceContext); ok {
		// For local sources, there's no sense in requesting the API
		// if the resolved ref doesn't exist
		if _, err := os.Stat(ref); err != nil {
			ref = ""
		}
	}

	// best effort for use case of user providing a path within the current context
	if ref != "" {
		if cfg, _ := h.getModuleConfig(ctx, ref); cfg != nil {
			return cfg, nil
		}
	}

	// fallback to original path, which may be an absolute ref
	return h.getModuleConfig(ctx, path)
}

type configuredModule struct {
	Source  *dagger.ModuleSource
	Ref     string
	Subpath string
	Digest  string
}

func (h *shellCallHandler) getModuleConfig(ctx context.Context, ref string) (rcfg *configuredModule, rerr error) {
	if h.Debug() {
		defer func() {
			shellDebug(ctx, "getModuleConfig", ref, rcfg)
		}()
	}
	ctx, span := Tracer().Start(ctx, "detect module: "+ref)
	defer telemetry.End(span, func() error { return rerr })

	src := h.dag.ModuleSource(ref)

	// could be a git repo without a dagger.json in a parent directory
	// (i.e., doesn't return an error)
	exists, err := src.ConfigExists(ctx)
	if !exists {
		return nil, err
	}

	var srcRef string
	var srcPin string
	var subpath string
	var digest string

	eg, gctx := errgroup.WithContext(ctx)

	// ref could be a subpath so we get the right module root ref
	eg.Go(func() error {
		v, err := src.AsString(gctx)
		if err != nil {
			return err
		}
		srcRef = v
		return nil
	})

	// ref could be a dependency name
	eg.Go(func() error {
		v, err := src.Pin(gctx)
		if err != nil {
			return err
		}
		srcPin = v
		return nil
	})

	eg.Go(func() error {
		v, err := src.OriginalSubpath(gctx)
		if err != nil {
			return err
		}
		subpath = v
		return nil
	})

	eg.Go(func() error {
		v, err := src.Digest(gctx)
		if err != nil {
			return err
		}
		digest = v
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return &configuredModule{
		Digest:  digest,
		Ref:     srcRef,
		Subpath: filepath.Join("/", subpath),
		Source: h.dag.ModuleSource(srcRef, dagger.ModuleSourceOpts{
			RefPin: srcPin,
		}),
	}, nil
}

func (h *shellCallHandler) newWorkdir(ctx context.Context, def *moduleDef, subpath string) (rwd *shellWorkdir, rerr error) {
	var context moduleContext
	var digest string
	var err error

	if def != nil && def.SourceDigest != h.modDigest() {
		context, err = newModuleContext(ctx, def)
		if err != nil {
			return nil, err
		}
		digest = def.SourceDigest
	} else {
		h.mu.RLock()
		context = h.wd.Context
		digest = h.wd.Module
		h.mu.RUnlock()
	}

	// initial context, without a loaded module (core API only)
	if context == nil {
		// a few quick checks first
		root, err := pathutil.Abs(subpath)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%q is not a directory", root)
		}

		if !h.noModule {
			// ask API where the context dir is (.git)
			ctx, span := Tracer().Start(ctx, "looking for context directory", telemetry.Internal())
			defer telemetry.End(span, func() error { return rerr })

			src := h.dag.ModuleSource(root, dagger.ModuleSourceOpts{
				DisableFindUp:  true,
				AllowNotExists: true,
			})
			root, err = src.LocalContextDirectoryPath(ctx)
			if err != nil {
				return nil, err
			}
			subpath, err = src.SourceRootSubpath(ctx)
			if err != nil {
				return nil, err
			}
		}
		return &shellWorkdir{
			Context: localSourceContext{
				Root: root,
			},
			Path: filepath.Join("/", subpath),
		}, nil
	}

	// Allow navigation without a module, just check path without ModuleSource
	if def == nil {
		apath, err := h.contextAbsPath(subpath)
		if err != nil {
			return nil, err
		}
		if _, err := context.Directory(h.dag, apath).Sync(ctx); err != nil {
			return nil, err
		}
		subpath = apath
	}

	return &shellWorkdir{
		Context: context,
		Module:  digest,
		Path:    filepath.Join("/", subpath),
	}, nil
}

func newModuleContext(ctx context.Context, def *moduleDef) (rctx moduleContext, rerr error) {
	ctx, span := Tracer().Start(ctx, "getting more information from module source", telemetry.Internal())
	defer telemetry.End(span, func() error { return rerr })

	if def.SourceKind == dagger.ModuleSourceKindLocalSource {
		root, err := def.Source.LocalContextDirectoryPath(ctx)
		if err != nil {
			return nil, err
		}
		return localSourceContext{
			Root: root,
			Path: def.SourceRootSubpath,
		}, nil
	}

	eg, gctx := errgroup.WithContext(ctx)

	var root string
	var version string
	var pin string

	eg.Go(func() error {
		v, err := def.Source.CloneRef(gctx)
		if err != nil {
			return err
		}
		root = v
		return nil
	})

	eg.Go(func() error {
		v, err := def.Source.Version(gctx)
		if err != nil {
			return err
		}
		version = v
		return nil
	})

	eg.Go(func() error {
		v, err := def.Source.Commit(gctx)
		if err != nil {
			return err
		}
		pin = v
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return gitSourceContext{
		Root:    root,
		Version: version,
		Pin:     pin,
		Path:    def.SourceRootSubpath,
	}, nil
}

func (h *shellCallHandler) Directory(subpath string) (*dagger.Directory, error) {
	apath, err := h.contextAbsPath(subpath)
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.Directory(h.dag, apath), nil
}

func (h *shellCallHandler) File(subpath string) (*dagger.File, error) {
	apath, err := h.contextAbsPath(subpath)
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wd.Context.File(h.dag, apath), nil
}

func (h *shellCallHandler) contextAbsPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.workdirPath(), path)

		// path could be empty if input is empty and workdir is "/"
		if path != "" && !filepath.IsLocal(path) {
			return "", fmt.Errorf("can't escape context root: %s", h.contextRoot())
		}

		path = filepath.Join("/", path)
	}
	return path, nil
}

func (h *shellCallHandler) workdirPath() string {
	return strings.TrimPrefix(h.workdirAbsPath(), "/")
}

// modRelPath returns the relative path to a module's root dir from workdir
//
// If the target module is on a different source/context, the absolute ref
// is returned instead.
func (h *shellCallHandler) modRelPath(def *moduleDef) string {
	if srcRoot, _ := h.contextModRef(def.SourceRootSubpath); srcRoot == def.SourceRoot {
		// use relative path if it's shorter
		path, err := filepath.Rel(h.workdirAbsPath(), def.SourceRootSubpath)
		if err == nil {
			if len(path) > len(def.SourceRootSubpath) {
				path = def.SourceRootSubpath
			}
			return path
		}
	}
	return def.SourceRoot
}

// IsDefaultModule returns true if the given module reference points to
// the current context's module
func (h *shellCallHandler) IsDefaultModule(ref string) bool {
	return ref == "" || ref == h.modDigest()
}

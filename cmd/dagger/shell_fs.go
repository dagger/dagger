package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"golang.org/x/sync/errgroup"
)

// returnToModuleRoot is an argument for `.cd` to return to the current module's root directory
//
// Not to be confused with empty `.cd` which changes to the initial context.
// TODO: bikeshed
const returnToModuleRoot = "-"

type shellWorkdir struct {
	// PathResolver is an in-memory representation of ModuleSource, used to produce paths
	PathResolver shellModuleSource

	// Path is an absolute file path, rooted at the context
	Path string
}

type shellModuleSource interface {
	Ref(subpath string) string
	Subpath() string
	Directory(subpath string, dag *dagger.Client) *dagger.Directory
	File(subpath string, dag *dagger.Client) *dagger.File
}

type localSourceResolver struct {
	Root string
	Path string
}

func (src localSourceResolver) Ref(subpath string) string {
	if subpath == "" {
		subpath = src.Path
	}
	return filepath.Join(src.Root, subpath)
}

func (src localSourceResolver) Subpath() string {
	return src.Path
}

func (src localSourceResolver) Directory(path string, dag *dagger.Client) *dagger.Directory {
	return dag.Host().Directory(filepath.Join(src.Root, path))
}

func (src localSourceResolver) File(path string, dag *dagger.Client) *dagger.File {
	return dag.Host().File(filepath.Join(src.Root, path))
}

type gitSourceResolver struct {
	Root    string
	Path    string
	Version string
	Pin     string
}

func (src gitSourceResolver) Ref(subpath string) string {
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

func (src gitSourceResolver) Subpath() string {
	return src.Path
}

func (src gitSourceResolver) context(dag *dagger.Client) *dagger.Directory {
	gitOpts := dagger.GitOpts{
		KeepGitDir: true,
	}
	if authSock, ok := os.LookupEnv("SSH_AUTH_SOCK"); ok {
		gitOpts.SSHAuthSocket = dag.Host().UnixSocket(authSock)
	}
	git := dag.Git(src.Root, gitOpts)
	var gitRef *dagger.GitRef
	if src.Pin != "" {
		gitRef = git.Commit(src.Pin)
	} else if src.Version != "" {
		gitRef = git.Ref(src.Version)
	} else {
		gitRef = git.Head()
	}
	return gitRef.Tree()
}

func (src gitSourceResolver) Directory(subpath string, dag *dagger.Client) *dagger.Directory {
	return src.context(dag).Directory(subpath)
}

func (src gitSourceResolver) File(subpath string, dag *dagger.Client) *dagger.File {
	return src.context(dag).File(subpath)
}

// parseModRef transforms user input into a full module reference that can be used with
// dag.ModuleSource().
func (h *shellCallHandler) parseModRef(ctx context.Context, path string) (rpath string, rerr error) {
	if h.debug {
		shellDebug(ctx, "parseModRef (before)", path)

		defer func() {
			shellDebug(ctx, "parseModRef (after)", rpath)
		}()
	}

	h.mu.RLock()
	resolver := h.workdir.PathResolver
	h.mu.RUnlock()

	if resolver == nil {
		return path, nil
	}

	fastKind := fastModuleSourceKindCheck(path, "")
	if fastKind == dagger.ModuleSourceKindGitSource {
		return path, nil
	}

	// Could be an absolute path, so remove until context root
	if _, ok := resolver.(localSourceResolver); ok {
		path = strings.TrimPrefix(path, resolver.Ref("/"))
	}

	apath, err := h.contextAbsPath(path)
	if err != nil {
		return apath, err
	}

	ref := resolver.Ref(apath)

	// If a local path and it doesn't exist, use original path and let the API handle it.
	// For example, it could be the name of a dependency.
	if _, ok := resolver.(localSourceResolver); ok {
		if h.debug {
			shellDebug(ctx, "parseModRef (pre-stat)", ref)
		}
		if _, err := os.Stat(ref); os.IsNotExist(err) {
			if h.debug {
				shellDebug(ctx, "parseModRef (stat)", err)
			}
			return path, nil
		}
	}

	return ref, nil
}

func (h *shellCallHandler) ChangeWorkdir(ctx context.Context, path string) error {
	if h.debug {
		shellDebug(ctx, "ChangeWorkdir (before)", path, h.workdir, h.LoadedModulesList())

		defer func() {
			shellDebug(ctx, "ChangeWorkdir (after)", path, h.workdir, h.LoadedModulesList())
		}()
	}

	var ref string
	var def *moduleDef
	var err error

	switch path {
	case "":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.workdir = h.initContext
		return nil
	case returnToModuleRoot:
		// TODO: bikeshed
		def, err = h.GetModuleDef(nil)
		if err != nil {
			return err
		}
	default:
		ref, err = h.parseModRef(ctx, path)
		if err != nil {
			return err
		}
		def, err = h.loadModule(ctx, ref)
		if err != nil {
			return fmt.Errorf("load module: %w", err)
		}
	}

	wd, err := h.newWorkdir(ctx, def, ref)
	if err != nil {
		return fmt.Errorf("set workdir: %w", err)
	}

	h.mu.Lock()
	h.workdir = wd
	h.mu.Unlock()

	return nil
}

func (h *shellCallHandler) loadModule(ctx context.Context, ref string) (rdef *moduleDef, rerr error) {
	if h.debug {
		shellDebug(ctx, "loadModule (before)", ref)

		defer func() {
			var r string
			if rdef != nil {
				r = rdef.SourceRoot
			}
			shellDebug(ctx, "loadModule (after)", r)
		}()
	}

	modSrc := h.dag.ModuleSource(ref)

	// could be a git repo without a dagger.json in a parent directory
	exists, err := modSrc.ConfigExists(ctx)
	if err != nil || !exists {
		return nil, err
	}

	// ref could be a subpath so we get the right module root ref
	srcRef, err := modSrc.AsString(ctx)
	if err != nil {
		return nil, err
	}

	if h.debug {
		shellDebug(ctx, "loadModule (asString)", srcRef)
	}

	// ref could be a dependency name
	srcPin, err := modSrc.Pin(ctx)
	if err != nil {
		return nil, err
	}

	return h.getOrInitDef(srcRef, func() (*moduleDef, error) {
		return initializeModule(ctx, h.dag, h.dag.ModuleSource(srcRef, dagger.ModuleSourceOpts{
			RefPin: srcPin,
		}))
	})
}

func (h *shellCallHandler) newWorkdir(ctx context.Context, def *moduleDef, ref string) (shellWorkdir, error) {
	wd := shellWorkdir{}

	if def != nil && def.SourceRoot != h.ModuleRoot() {
		r, err := newShellModuleSource(ctx, def)
		if err != nil {
			return wd, err
		}
		wd.PathResolver = r
	} else {
		h.mu.RLock()
		wd.PathResolver = h.workdir.PathResolver
		h.mu.RUnlock()
	}

	var subpath string

	if ref != "" {
		path, err := h.dag.ModuleSource(ref).OriginalSubpath(ctx)
		if err != nil {
			return wd, err
		}
		subpath = path
	}

	if subpath == "" {
		subpath = wd.PathResolver.Subpath()
	}

	wd.Path = filepath.Join("/", subpath)

	return wd, nil
}

func newShellModuleSource(ctx context.Context, def *moduleDef) (shellModuleSource, error) {
	if def.SourceKind == dagger.ModuleSourceKindLocalSource {
		root, err := def.Source.LocalContextDirectoryPath(ctx)
		if err != nil {
			return nil, err
		}

		return localSourceResolver{
			Root: root,
			Path: def.SourceRootSubpath,
		}, nil
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, gctx := errgroup.WithContext(cancelCtx)

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

	return gitSourceResolver{
		Root:    root,
		Version: version,
		Pin:     pin,
		Path:    def.SourceRootSubpath,
	}, nil
}

func (h *shellCallHandler) contextAbsPath(path string) (string, error) {
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
	return h.workdir.PathResolver != nil
}

func (h *shellCallHandler) ContextRoot() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return ""
	}
	return h.workdir.PathResolver.Ref("/")
}

func (h *shellCallHandler) Directory(subpath string) (*dagger.Directory, error) {
	apath, err := h.contextAbsPath(subpath)
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return nil, fmt.Errorf("no context")
	}
	return h.workdir.PathResolver.Directory(apath, h.dag), nil
}

func (h *shellCallHandler) File(subpath string) (*dagger.File, error) {
	apath, err := h.contextAbsPath(subpath)
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return nil, fmt.Errorf("no context")
	}
	return h.workdir.PathResolver.File(apath, h.dag), nil
}

func (h *shellCallHandler) ContextPath(path string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return ""
	}
	return h.workdir.PathResolver.Ref(path)
}

func (h *shellCallHandler) ModuleRoot() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return ""
	}
	return h.workdir.PathResolver.Ref("")
}

func (h *shellCallHandler) InitialPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.workdir.PathResolver == nil {
		return ""
	}
	return filepath.Join("/", h.workdir.PathResolver.Subpath())
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
	return h.workdir.PathResolver.Ref(h.workdir.Path)
}

// ModRel returns the relative path to a module's root dir from workdir, if
// target module is the one currently loaded
func (h *shellCallHandler) ModRel(def *moduleDef) string {
	if def != nil && def.SourceRoot == h.ModuleRoot() {
		path, err := filepath.Rel(h.WorkdirAbsPath(), def.SourceRootSubpath)
		if err == nil {
			if len(path) > len(def.SourceRootSubpath) {
				path = def.SourceRootSubpath
			}
			return path
		}
	}
	return def.SourceRoot
}

// IsDefaultModule returns true if the given module reference is the default loaded module
func (h *shellCallHandler) IsDefaultModule(ref string) bool {
	return ref == "" || ref == h.ModuleRoot()
}

// fastModuleSourceKindCheck does a simple lexographical analysis on a path
// to cehck if it's a local path or a git URL.
//
// TODO: This is duplicated from core/schema/modulerefs.go. Ideally we could reuse it.
func fastModuleSourceKindCheck(
	refString string,
	refPin string,
) dagger.ModuleSourceKind {
	switch {
	case refPin != "":
		return dagger.ModuleSourceKindGitSource
	case len(refString) > 0 && (refString[0] == '/' || refString[0] == '.'):
		return dagger.ModuleSourceKindLocalSource
	case len(refString) > 1 && refString[0:2] == "..":
		return dagger.ModuleSourceKindLocalSource
	case strings.HasPrefix(refString, core.SchemeHTTP.Prefix()):
		return dagger.ModuleSourceKindGitSource
	case strings.HasPrefix(refString, core.SchemeHTTPS.Prefix()):
		return dagger.ModuleSourceKindGitSource
	case strings.HasPrefix(refString, core.SchemeSSH.Prefix()):
		return dagger.ModuleSourceKindGitSource
	case strings.HasPrefix(refString, "github.com"):
		return dagger.ModuleSourceKindGitSource
	case !strings.Contains(refString, "."):
		// technically host names can not have any dot, but we can save a lot of work
		// by assuming a dot-free ref string is a local path. Users can prefix
		// args with a scheme:// to disambiguate these obscure corner cases.
		return dagger.ModuleSourceKindLocalSource
	default:
		return ""
	}
}

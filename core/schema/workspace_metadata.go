package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/fsutil"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workspaceMetadataLink struct{ name, target string }

type workspaceMetadataPath struct {
	file  string
	links []workspaceMetadataLink
	dirs  []string
}

// workspaceMetadataRootfs reads one metadata file and its links. Resolve each
// component against the effective workspace before uploading file contents:
// a link or target can exist only in the overlay, or be deleted there.
func (s *workspaceSchema) workspaceMetadataRootfs(ctx context.Context, ws *core.Workspace, file string) (dagql.ObjectResult[*core.Directory], error) {
	if !ws.ClientLocalBase() {
		return s.workspaceOverlayRootfs(ctx, ws)
	}
	stat, readFile, err := s.workspaceMetadataFS(ctx, ws)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	resolved, err := resolveWorkspaceMetadataPath(file, stat)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	// Read the canonical file directly. Include filters use glob syntax, so
	// they cannot represent arbitrary literal names on every client platform.
	data, err := readFile(resolved.file)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	info, err := stat(resolved.file)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	var root dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &root, dagql.Selector{Field: "directory"}, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(resolved.file)},
			{Name: "contents", Value: dagql.String(data)},
			{Name: "permissions", Value: dagql.Int(os.FileMode(info.Mode).Perm())},
		},
	}); err != nil {
		return root, err
	}
	// A directory traversed before ".." is still required by filesystem link
	// resolution, even when the final file lives elsewhere.
	for _, dir := range resolved.dirs {
		root, err = workspaceMigrationSelectDirectory(ctx, root, "withNewDirectory", []dagql.NamedInput{
			{Name: "path", Value: dagql.String(dir)},
		})
		if err != nil {
			return root, err
		}
	}
	restored := map[string]bool{}
	for _, link := range resolved.links {
		if restored[link.name] {
			continue
		}
		restored[link.name] = true
		root, err = workspaceMigrationSelectDirectory(ctx, root, "withSymlink", []dagql.NamedInput{
			{Name: "target", Value: dagql.String(link.target)},
			{Name: "linkName", Value: dagql.String(link.name)},
		})
		if err != nil {
			return root, err
		}
	}
	return root, nil
}

func (s *workspaceSchema) workspaceReadMetadataFile(ctx context.Context, ws *core.Workspace, file string) ([]byte, error) {
	root, err := s.workspaceMetadataRootfs(ctx, ws, file)
	if err != nil {
		return nil, err
	}
	return core.DirectoryReadFile(ctx, root, file)
}

// workspaceMetadataFS reads exact metadata paths, never a directory import.
// Host absolute links are normalized to workspace-root-relative targets.
func (s *workspaceSchema) workspaceMetadataFS(ctx context.Context, ws *core.Workspace) (func(string) (*fstypes.Stat, error), func(string) ([]byte, error), error) {
	hostCtx, err := s.withWorkspaceHostReadContext(ctx, ws)
	if err != nil {
		return nil, nil, err
	}
	query, err := core.CurrentQuery(hostCtx)
	if err != nil {
		return nil, nil, err
	}
	bk, err := query.Engine(hostCtx)
	if err != nil {
		return nil, nil, err
	}
	realRoot, err := bk.RealCallerHostPath(hostCtx, ws.HostPath())
	if err != nil {
		return nil, nil, err
	}
	stat := func(p string) (*fstypes.Stat, error) {
		if !filepath.IsLocal(p) {
			return nil, fmt.Errorf("metadata path %q escapes workspace root", p)
		}
		if delta, ok := ws.OverlayDeltaRoot(); ok {
			var st *fstypes.Stat
			err := delta.Self().Mount(ctx, delta, func(root string) error {
				var err error
				st, err = fsutil.Stat(filepath.Join(root, filepath.FromSlash(p)))
				return err
			})
			if err == nil {
				return st, nil
			}
			if !errors.Is(err, os.ErrNotExist) || ws.OverlayPathTouched(p) {
				return nil, err
			}
		}
		st, err := bk.StatCallerHostPath(hostCtx, filepath.Join(ws.HostPath(), filepath.FromSlash(p)), true)
		if status.Code(err) == codes.NotFound {
			return nil, os.ErrNotExist
		}
		if err != nil {
			return nil, err
		}
		if filepath.IsAbs(st.Linkname) {
			// Strip the workspace prefix without cleaning the target. A ".."
			// after a symlink must be evaluated after that link is resolved.
			inside := false
			for _, root := range []string{realRoot, filepath.Clean(ws.HostPath())} {
				if st.Linkname == root {
					st.Linkname = "/"
					inside = true
					break
				}
				prefix := strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator)
				if suffix, ok := strings.CutPrefix(st.Linkname, prefix); ok {
					st.Linkname = "/" + filepath.ToSlash(suffix)
					inside = true
					break
				}
			}
			if !inside {
				return nil, fmt.Errorf("metadata link %q escapes workspace root", p)
			}
		}
		return st, nil
	}
	readFile := func(p string) ([]byte, error) {
		if !filepath.IsLocal(p) {
			return nil, fmt.Errorf("metadata path %q escapes workspace root", p)
		}
		if delta, ok := ws.OverlayDeltaRoot(); ok {
			var data []byte
			err := delta.Self().Mount(ctx, delta, func(root string) error {
				var err error
				data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
				return err
			})
			if err == nil || !errors.Is(err, os.ErrNotExist) || ws.OverlayPathTouched(p) {
				return data, err
			}
		}
		return bk.ReadCallerHostFile(hostCtx, filepath.Join(ws.HostPath(), filepath.FromSlash(p)))
	}
	return stat, readFile, nil
}

func resolveWorkspaceMetadataPath(file string, stat func(string) (*fstypes.Stat, error)) (workspaceMetadataPath, error) {
	var result workspaceMetadataPath
	if !filepath.IsLocal(file) {
		return result, fmt.Errorf("metadata path %q escapes workspace root", file)
	}
	seenDirs := map[string]bool{}
	var resolve func(string, []string) (string, error)
	resolve = func(resolved string, pending []string) (string, error) {
		for len(pending) > 0 {
			part := pending[0]
			pending = pending[1:]
			switch part {
			case "", ".":
				continue
			case "..":
				if resolved == "." {
					return "", fmt.Errorf("metadata path %q escapes workspace root", file)
				}
				resolved = path.Dir(resolved)
				continue
			}
			candidate := path.Join(resolved, part)
			st, err := stat(candidate)
			if err != nil {
				return "", err
			}
			mode := os.FileMode(st.Mode)
			if mode&os.ModeSymlink != 0 {
				if len(result.links) == 40 {
					return "", fmt.Errorf("too many symlinks in metadata path %q", file)
				}
				index := len(result.links)
				result.links = append(result.links, workspaceMetadataLink{name: candidate})
				target := filepath.ToSlash(st.Linkname)
				parent := resolved
				if path.IsAbs(target) {
					parent = "."
					target = strings.TrimLeft(target, "/")
				}
				// Resolve the target before the remaining components, including
				// any ".." after it. Do not clean the raw target with path.Join.
				resolved, err = resolve(parent, strings.Split(target, "/"))
				if err != nil {
					return "", err
				}
				st, err = stat(resolved)
				if err != nil {
					return "", err
				}
				mode = os.FileMode(st.Mode)
				// Store a canonical target: directory consumers also clean link
				// targets, so retaining the raw target would undo this resolution.
				relative, err := filepath.Rel(filepath.FromSlash(path.Dir(candidate)), filepath.FromSlash(resolved))
				if err != nil {
					return "", err
				}
				result.links[index].target = filepath.ToSlash(relative)
			} else {
				resolved = candidate
			}
			if len(pending) > 0 && !mode.IsDir() {
				return "", fmt.Errorf("metadata path %q: %w", candidate, syscall.ENOTDIR)
			}
			if mode.IsDir() && !seenDirs[resolved] {
				result.dirs = append(result.dirs, resolved)
				seenDirs[resolved] = true
			}
		}
		return resolved, nil
	}
	resolved, err := resolve(".", strings.Split(filepath.ToSlash(file), "/"))
	if err != nil {
		return result, err
	}
	st, err := stat(resolved)
	if err != nil {
		return result, err
	}
	if !os.FileMode(st.Mode).IsRegular() {
		return result, fmt.Errorf("metadata path %q is not a regular file", file)
	}
	result.file = resolved
	return result, nil
}

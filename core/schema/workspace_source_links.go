package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/fsutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// workspaceModuleReadlink reads only path metadata. Overlay entries take
// precedence over the host, including deletion of a touched subtree.
func (s *workspaceSchema) workspaceModuleReadlink(ctx context.Context, ws *core.Workspace) (func(string) (string, error), error) {
	hostCtx, err := s.withWorkspaceHostReadContext(ctx, ws)
	if err != nil {
		return nil, err
	}
	query, err := core.CurrentQuery(hostCtx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Engine(hostCtx)
	if err != nil {
		return nil, err
	}
	realRoot, err := bk.RealCallerHostPath(hostCtx, ws.HostPath())
	if err != nil {
		return nil, err
	}
	type entry struct {
		link string
		err  error
	}
	cache := map[string]entry{}
	return func(p string) (string, error) {
		if cached, ok := cache[p]; ok {
			return cached.link, cached.err
		}
		lookup := func() (string, error) {
			if delta, ok := ws.OverlayDeltaRoot(); ok {
				var link string
				err := delta.Self().Mount(ctx, delta, func(root string) error {
					st, err := fsutil.Stat(filepath.Join(root, filepath.FromSlash(p)))
					if err == nil {
						link = st.Linkname
					}
					return err
				})
				if err == nil {
					return link, nil
				}
				if !errors.Is(err, os.ErrNotExist) {
					return "", err
				}
				if ws.OverlayPathTouched(p) {
					return "", nil
				}
			}
			st, err := bk.StatCallerHostPath(hostCtx, filepath.Join(ws.HostPath(), filepath.FromSlash(p)), true)
			if status.Code(err) == codes.NotFound {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			link := filepath.ToSlash(st.Linkname)
			if filepath.IsAbs(link) {
				rel, err := filepath.Rel(realRoot, link)
				if err != nil || !filepath.IsLocal(rel) {
					return "", fmt.Errorf("module path %q leaves workspace through symlink", p)
				}
				link = "/" + filepath.ToSlash(rel)
			}
			return link, nil
		}
		link, err := lookup()
		cache[p] = entry{link, err}
		return link, err
	}, nil
}

// resolveWorkspaceModuleLinks keeps every link in a chain available as well
// as the final target. Each target must stay within the workspace.
func resolveWorkspaceModuleLinks(p string, readlink func(string) (string, error)) (string, []string, error) {
	pending := strings.Split(path.Clean(p), "/")
	resolved := "."
	var links []string
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == "." || part == "" {
			continue
		}
		candidate := path.Join(resolved, part)
		if !filepath.IsLocal(candidate) {
			return "", nil, fmt.Errorf("module path %q leaves workspace through symlink", p)
		}
		link, err := readlink(candidate)
		if err != nil {
			return "", nil, err
		}
		if link == "" {
			resolved = candidate
			continue
		}
		if len(links) >= 40 {
			return "", nil, fmt.Errorf("too many symlinks in module path %q", p)
		}
		links = append(links, candidate)
		if path.IsAbs(link) {
			resolved = "."
			link = strings.TrimPrefix(link, "/")
		}
		pending = append(strings.Split(link, "/"), pending...)
	}
	return resolved, links, nil
}

package core

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
)

// boundDirectoryIgnoreRead limits a root copy with gitignore and literal
// includes to the selected regions and possible ignore files. The copier can
// inspect ignore files even in directories whose contents it skips.
func (c *workspaceInputCollector) boundDirectoryIgnoreRead(ws *Workspace, selector provenance.Selector) (provenance.Selector, bool) {
	if selector.Path != "/" || len(selector.Include) == 0 || len(selector.Exclude) != 0 {
		return selector, false
	}
	source, ok := ws.Source().(*WorkspaceSourceDirectory)
	if !ok || source.Root.Self() == nil {
		return selector, false
	}
	if _, mounted := ws.MountsDir(); mounted {
		return selector, false
	}
	root := source.Root
	dirPath, err := root.Self().Dir.GetOrEval(c.ctx, root.Result)
	if err != nil || (path.Clean(dirPath) != "/" && path.Clean(dirPath) != ".") {
		return selector, false
	}

	var regions []string
	for _, include := range selector.Include {
		if include != strings.TrimSpace(include) || strings.ContainsAny(include, "*[]?^!\\") || !filepath.IsLocal(include) {
			return selector, false
		}
		region := path.Clean(include)
		if region == "." {
			return selector, false
		}
		regions = append(regions, region)
	}
	if !c.directoryIgnoreRulesUnaliased(root) {
		return selector, false
	}
	err = root.Self().Mount(c.ctx, root, func(dir string) error {
		for _, region := range regions {
			prefix := dir
			for _, component := range strings.Split(region, "/") {
				prefix = filepath.Join(prefix, component)
				info, err := os.Lstat(prefix)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("source path %q is a symlink", region)
				}
			}
		}
		return nil
	})
	if err != nil {
		return selector, false
	}
	// Any subtree can gain an ignore file. Exact file queries still match
	// only the selected regions or the ignore file name.
	selector.Include = append(regions, "**/.gitignore")
	selector.Conservative = true
	return selector, true
}

func (c *workspaceInputCollector) directoryIgnoreRulesUnaliased(root dagql.ObjectResult[*Directory]) bool {
	snapshot, err := root.Self().Snapshot.GetOrEval(c.ctx, root.Result)
	if err != nil || snapshot == nil || snapshot.ID() == "" {
		return false
	}
	// Many module copies share this original immutable snapshot. Check it
	// once, even if their filtered outputs differ.
	res, err := c.cache.GetOrInitArbitrary(c.ctx, c.session, "workspace-ignore-rules:"+snapshot.ID(), func(ctx context.Context) (_ any, rerr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				rerr = fmt.Errorf("inspect workspace ignore rules: %v", recovered)
			}
		}()
		unaliased := true
		err := root.Self().Mount(ctx, root, func(dir string) error {
			return filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if err := context.Cause(ctx); err != nil {
					return err
				}
				if entry.Name() == ".gitignore" && !entry.Type().IsRegular() {
					unaliased = false
					return fs.SkipAll
				}
				return nil
			})
		})
		if err != nil {
			return nil, err
		}
		return unaliased, context.Cause(ctx)
	})
	if err != nil || res == nil {
		return false
	}
	unaliased, _ := res.Value().(bool)
	return unaliased
}

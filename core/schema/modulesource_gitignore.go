package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/util/fsxutil"
)

// moduleIgnoreFiles selects only the rule files that can affect these paths.
func moduleIgnoreFiles(paths ...string) []string {
	seen := map[string]bool{}
	var files []string
	for _, p := range paths {
		p = path.Clean(filepath.ToSlash(p))
		if !filepath.IsLocal(p) {
			continue
		}
		for {
			file := path.Join(p, ".gitignore")
			if !seen[file] {
				files = append(files, file)
				seen[file] = true
			}
			if p == "." {
				break
			}
			p = path.Dir(p)
		}
	}
	return files
}

func rejectGitignoredModulePaths(ctx context.Context, rules dagql.ObjectResult[*core.Directory], paths ...string) error {
	return rules.Self().Mount(ctx, rules, func(root string) error {
		fs, err := fsutil.NewFS(root)
		if err != nil {
			return err
		}
		matcher := fsxutil.NewGitIgnoreMatcher(fs)
		for _, p := range paths {
			p = path.Clean(filepath.ToSlash(p))
			if p == "." || !filepath.IsLocal(p) {
				continue
			}
			ignored, err := matcher.Matches(p, true)
			if err != nil {
				return err
			}
			if ignored {
				return fmt.Errorf("module path %q is gitignored; remove its .gitignore exclusion to use this module", p)
			}
		}
		return nil
	})
}

// checkModuleSourceGitignore diagnoses missing paths in an already loaded
// context. Read host ignore rules only when a source directory is absent.
func checkModuleSourceGitignore(ctx context.Context, src *core.ModuleSource) error {
	paths := []string{src.SourceRootSubpath, src.SourceSubpath}
	// Value and Git snapshots are authoritative. For directory sources, only
	// diagnose missing paths; files supplied explicitly remain usable.
	local := src.Kind == core.ModuleSourceKindLocal
	if ws := src.Workspace.Self(); ws != nil {
		local = ws.ClientLocalBase()
	}
	if !local && src.Kind != core.ModuleSourceKindDir {
		return nil
	}

	var missing []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		_, exists, err := core.StatFSExists(ctx, &core.DirectoryStatFS{Dir: src.ContextDirectory}, p)
		if err != nil {
			return err
		}
		if !exists {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !local {
		return rejectGitignoredModulePaths(ctx, src.ContextDirectory, missing...)
	}

	rules, err := core.NewModuleSourceFS(nil, src).ContextDirectory(ctx, core.CopyFilter{Include: moduleIgnoreFiles(missing...)})
	if err != nil {
		return err
	}
	return rejectGitignoredModulePaths(ctx, rules, missing...)
}

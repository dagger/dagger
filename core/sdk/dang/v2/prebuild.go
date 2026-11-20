package dangv2

import (
	"context"
	"path/filepath"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
)

// PrebuildRuntime parses src's Dang source. The source doesn't depend on
// the module's dependencies, so the engine parses it while they load (see
// core.RuntimePrebuilder). Reading the module's types and running its calls
// both start from the parsed source: the syntax cache holds it, and the
// declared type names that ModuleTypes reads first are kept with it.
func (Impl) PrebuildRuntime(ctx context.Context, src dagql.ObjectResult[*core.ModuleSource]) error {
	self := src.Self()
	modCtx := self.ContextDirectory
	// A module is loaded again in every session that uses it, and its load
	// waits for this prebuild to end. Once this engine has parsed a source,
	// later loads of it don't mount it again only to find it parsed.
	contextDigest, err := modCtx.ContentPreferredDigest(ctx)
	if err != nil {
		return err
	}
	key := hashutil.HashStrings(
		contextDigest.String(),
		self.SourceRootSubpath,
		self.SourceSubpath,
		moduleDangVersion(self),
		self.ModuleOriginalName,
	).String()
	if prebuiltSources.Contains(key) {
		return nil
	}

	ctx = withDangSyntaxCache(ctx)
	ctx, err = ContextWithDangVersion(ctx, moduleDangVersion(self))
	if err != nil {
		return err
	}
	err = modCtx.Self().Mount(ctx, modCtx, func(path string) error {
		dir, cleanup, err := dangSourceDir(
			filepath.Join(path, self.SourceSubpath),
			filepath.Join(path, self.SourceRootSubpath),
		)
		if err != nil {
			return err
		}
		defer cleanup()
		// ModuleTypes reads the types under the module's original name.
		moduleDeclaredTypeNames(ctx, dir, self.ModuleOriginalName)
		return nil
	})
	if err != nil {
		return err
	}
	prebuiltSources.Add(key, struct{}{})
	return nil
}

// prebuiltSources holds the sources PrebuildRuntime has parsed, by their
// content, up to 256 of them.
var prebuiltSources, _ = lru.New[string, struct{}](256) // error is impossible on positive size

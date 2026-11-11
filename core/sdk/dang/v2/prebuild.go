package dangv2

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// PrebuildRuntime parses src's Dang source. The source doesn't depend on
// the module's dependencies, so the engine parses it while they load (see
// core.RuntimePrebuilder). Reading the module's types and running its calls
// both start from the parsed source: the syntax cache holds it, and the
// declared type names that ModuleTypes reads first are kept with it.
func (Impl) PrebuildRuntime(ctx context.Context, src dagql.ObjectResult[*core.ModuleSource]) error {
	self := src.Self()
	ctx = withDangSyntaxCache(ctx)
	ctx, err := ContextWithDangVersion(ctx, moduleDangVersion(self))
	if err != nil {
		return err
	}
	modCtx := self.ContextDirectory
	return modCtx.Self().Mount(ctx, modCtx, func(path string) error {
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
}

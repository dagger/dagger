package core

import (
	"errors"
	"fmt"
)

var ErrForeignModuleContext = errors.New("local module context belongs to another engine")

// LocalContextDirectoryPath is the checked host-path accessor. Formatting and
// identity comparisons may still describe the recorded path without using it.
func (src *ModuleSource) LocalContextDirectoryPath() (string, error) {
	if src == nil || src.Kind != ModuleSourceKindLocal || src.Local == nil {
		return "", fmt.Errorf("module source has no local context")
	}
	if src.Local.Foreign {
		return "", fmt.Errorf("%w: module %q at %q", ErrForeignModuleContext, src.ModuleName, src.AsString())
	}
	return src.Local.ContextDirectoryPath, nil
}

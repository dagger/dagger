package core

import (
	"path"
	"path/filepath"
	"strings"
)

type ModuleLoadFailure struct {
	// Name is the module's workspace name (what the skipped-module span is
	// called).
	Name string `json:"name"`
	// Dir is the module's workspace-root-relative directory, or "" when it
	// has none (a git source). Changes tells whether the run regenerated
	// files under it, which is often exactly what repairs a failed load.
	Dir string `json:"dir,omitempty"`
	// Message is the described load error (see the engine's
	// describeLoadFailure).
	Message string `json:"message"`
}

// Regenerated reports whether any of the run's changed paths (workspace-root
// relative) fall under the module's directory — i.e. whether this run
// rewrote (part of) the module, so its load failure may already be fixed.
func (f ModuleLoadFailure) Regenerated(changed []string) bool {
	if f.Dir == "" {
		return false
	}
	dir := path.Clean(filepath.ToSlash(f.Dir))
	for _, p := range changed {
		p = path.Clean(filepath.ToSlash(p))
		if dir == "." || p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

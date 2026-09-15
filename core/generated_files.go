package core

import "fmt"

// MissingGeneratedFileError reports a module whose config format builds from
// committed generated files (dagger-module.toml) but is missing one, so its
// runtime cannot be built from source. SDK runtimes return it from their load
// path so callers can tell "needs generation" apart from a genuine build
// failure — best-effort `dagger generate` in particular, which is often the fix
// and must not parrot the "run `dagger generate`" hint back at the user.
type MissingGeneratedFileError struct {
	Module string
	Path   string
}

func (e *MissingGeneratedFileError) Error() string {
	return e.Reason() + "; run `dagger generate` and commit the generated files"
}

// Reason is the hint-free message: what is missing, without the advice to run
// `dagger generate`, for contexts where that is exactly what is running.
func (e *MissingGeneratedFileError) Reason() string {
	return fmt.Sprintf("module %q: generated file %q is missing", e.Module, e.Path)
}

package workspace

import "strings"

// IsShortFormModuleRef reports whether value has the shape of a short-form
// entrypoint function reference ("<function>" rather than "<module>:<function>"):
// no ":" or "/", and not a relative path. Shape only; callers check the function exists.
func IsShortFormModuleRef(value string) bool {
	return value != "" &&
		!strings.ContainsAny(value, ":/") &&
		!strings.HasPrefix(value, ".")
}

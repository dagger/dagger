package workspace

import "strings"

// IsBareModuleFunctionRef reports whether a settings value has the shape of a
// short-form module reference: a bare "<function>" naming a function of the
// workspace entrypoint module, as opposed to the long form
// "<module>:<function>". It is a shape check only. Whether the entrypoint
// actually has such a function is decided by whoever resolves or rewrites the
// value.
//
// A bare name has no ":" (that would be the long form, an image tag, or a
// URL), no "/" (a path or a registry ref), and does not start with "." (a
// relative path).
func IsBareModuleFunctionRef(value string) bool {
	return value != "" &&
		!strings.ContainsAny(value, ":/") &&
		!strings.HasPrefix(value, ".")
}

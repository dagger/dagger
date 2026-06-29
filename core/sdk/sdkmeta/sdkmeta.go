// Package sdkmeta holds metadata about the SDK runtimes bundled in the engine.
// It carries no dependencies so both core/sdk and core/workspace can share the
// canonical builtin SDK list without an import cycle.
package sdkmeta

import "slices"

// Builtin SDK runtime short names bundled in the engine.
const (
	Go         = "go"
	Dang       = "dang"
	Python     = "python"
	Typescript = "typescript"
	PHP        = "php"
	Elixir     = "elixir"
	Java       = "java"
)

// Builtins lists every SDK runtime short name bundled in the engine.
var Builtins = []string{Go, Dang, Python, Typescript, PHP, Elixir, Java}

// IsBuiltin reports whether name (without any "@version" suffix) is a builtin
// SDK runtime short name.
func IsBuiltin(name string) bool {
	return slices.Contains(Builtins, name)
}

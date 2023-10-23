package dagger

import "embed"

// These are exported so that they can be used by codegen.

//go:embed querybuilder/marshal.go querybuilder/querybuilder.go
var QueryBuilder embed.FS

//go:embed go.mod
var GoMod []byte

//go:embed go.sum
var GoSum []byte

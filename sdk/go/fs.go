package dagger

import (
	"embed"
)

// These are exported so that they can be used by codegen.

//go:embed engineconn/*.go
var EngineConn embed.FS

//go:embed go.mod
var GoMod []byte

//go:embed go.sum
var GoSum []byte

//go:embed engineconn/*.go go.mod go.sum client.go dagger.gen.go
var GoSDK embed.FS

//go:embed dagger.gen.go
var GoDagGen []byte

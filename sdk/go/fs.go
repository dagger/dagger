package dagger

import (
	"embed"

	"dagger.io/dagger/sdkfs"
)

// These are exported so that they can be used by codegen.

//go:embed engineconn/*.go
var EngineConn embed.FS

var GoMod = sdkfs.GoMod

var GoSum = sdkfs.GoSum

//go:embed engineconn/*.go go.mod go.sum client.go dagger.gen.go
var GoSDK embed.FS

//go:embed dagger.gen.go
var GoDagGen []byte

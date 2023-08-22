package dagger

import (
	_ "embed"
)

var dag *Client

//go:embed env.go
var EnvironmentCode string

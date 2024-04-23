package consts

import (
	"github.com/dagger/dagger/engine/distconsts"
)

const (
	EngineServerPath = "/usr/local/bin/dagger-engine"
	EngineShimPath   = distconsts.EngineShimPath
)

const (
	GolangVersion = "1.22.2"
	// GolangVersionRuncHack needs to be 1.21, since 1.22 is not yet
	// supported, and can cause crashes: opencontainers/runc#4233
	GolangVersionRuncHack = "1.21.7"

	GolangLintVersion = "v1.57"

	AlpineVersion = "3.18"
	AlpineImage   = "alpine:" + AlpineVersion
	WolfiImage    = "cgr.dev/chainguard/wolfi-base"

	GolangLintImage = "golangci/golangci-lint:" + GolangLintVersion + "-alpine"

	UbuntuVersion = "22.04"
	RuncVersion   = "v1.1.12"
	CniVersion    = "v1.3.0"
	QemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
)

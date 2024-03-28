package consts

import (
	"github.com/dagger/dagger/engine/distconsts"
)

const (
	EngineServerPath    = "/usr/local/bin/dagger-engine"
	EngineDialStdioPath = "/usr/local/bin/dial-stdio" // XXX: this isn't used!
	EngineShimPath      = distconsts.EngineShimPath
)

const (
	GolangVersion = "1.21.7"

	AlpineVersion = "3.18"
	AlpineImage   = "alpine:" + AlpineVersion

	UbuntuVersion = "22.04"
	RuncVersion   = "v1.1.12"
	CniVersion    = "v1.3.0"
	QemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
)

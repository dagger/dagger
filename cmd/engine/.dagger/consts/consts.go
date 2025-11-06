package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath = "/usr/local/bin/dagger-engine"
	RuncPath         = distconsts.RuncPath
	DaggerInitPath   = distconsts.DaggerInitPath
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	UbuntuVersion = "22.04"

	RuncVersion  = "v1.3.3"
	CniVersion   = "v1.5.0"
	QemuBinImage = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"

	XxImage = "tonistiigi/xx:1.2.1"
)

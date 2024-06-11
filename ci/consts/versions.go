package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath = "/usr/local/bin/dagger-engine"
	RuncPath         = distconsts.RuncPath
	DumbInitPath     = distconsts.DumbInitPath
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	AlpineImage   = distconsts.AlpineImage

	WolfiImage   = "cgr.dev/chainguard/wolfi-base"
	WolfiVersion = "latest" // Wolfi is a rolling distro; no release to pin to

	UbuntuVersion   = "22.04"
	RuncVersion     = "v1.1.12"
	CniVersion      = "v1.3.0"
	QemuBinImage    = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
	DumbInitVersion = "v1.2.5"
)

package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath = "/usr/local/bin/dagger-engine"
	RuncPath         = distconsts.RuncPath
	DaggerInitPath   = distconsts.DaggerInitPath
	TiniPath         = distconsts.TiniPath
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	UbuntuVersion = "22.04"

	RuncVersion  = "v1.4.2"
	CniVersion   = "v1.9.0"
	QemuBinImage = "tonistiigi/binfmt@sha256:6014c1e52b8e51a67fbf76f691ffbe20ac0204c31c2f086df3e8ef3ce134b488"

	XxImage = "tonistiigi/xx:1.2.1"
)

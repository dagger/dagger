package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath = "/usr/local/bin/dagger-engine"
	RuncPath         = distconsts.RuncPath
	DaggerInitPath   = distconsts.DaggerInitPath
	DaggerCLIPath    = distconsts.DaggerCLIPath
	TiniPath         = distconsts.TiniPath
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	AlpineImage   = distconsts.AlpineImage
	UbuntuVersion = "22.04"

	RuncVersion  = "v1.4.2"
	CniVersion   = "v1.9.0"
	SSHFSVersion = "3.7.6"

	QemuVersion  = "11.0.2"
	QemuChecksum = "sha256:3745f6ea88e2e87fe0dc838b2b1d4e0a770bf48e01a1d5a186842a1fff76ccf5"
	// tonistiigi/binfmt commit providing QEMU patches, and the version of its
	// buildkit-direct-execve patch series matching QemuVersion.
	BinfmtCommit    = "e41434fefad6aee7b01fbe5efc1c4b6f6c735fff"
	QemuPatchSeries = "11.0"

	XxImage = "tonistiigi/xx:1.2.1"
)

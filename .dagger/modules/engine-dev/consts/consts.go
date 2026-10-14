package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath      = "/usr/local/bin/dagger-engine"
	RuntimePath           = distconsts.RuntimePath
	DaggerInitPath        = distconsts.DaggerInitPath
	DaggerInitSessionPath = distconsts.DaggerInitSessionPath
	DaggerCLIPath         = distconsts.DaggerCLIPath
	TiniPath              = distconsts.TiniPath
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	AlpineImage   = distconsts.AlpineImage
	UbuntuVersion = "22.04"

	// The container runtime is crun, built from the release tarball with a
	// static json-c (Alpine has no static json-c package).
	CrunVersion   = "1.30.1"
	CrunChecksum  = "sha256:99d036d95113552b6df66c7c879432835504bf0e525fc0372745071df129527b"
	JSONCVersion  = "0.18-20240915"
	JSONCChecksum = "sha256:3112c1f25d39eca661fe3fc663431e130cc6e2f900c081738317fba49d29e298"
	CniVersion    = "v1.9.0"
	SSHFSVersion  = "3.7.6"

	QemuVersion  = "11.0.2"
	QemuChecksum = "sha256:3745f6ea88e2e87fe0dc838b2b1d4e0a770bf48e01a1d5a186842a1fff76ccf5"
	// tonistiigi/binfmt commit providing QEMU patches, and the version of its
	// buildkit-direct-execve patch series matching QemuVersion.
	BinfmtCommit    = "e41434fefad6aee7b01fbe5efc1c4b6f6c735fff"
	QemuPatchSeries = "11.0"

	XxImage = "tonistiigi/xx:1.2.1"
)

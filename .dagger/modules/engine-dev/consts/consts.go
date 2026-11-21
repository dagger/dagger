package consts

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineServerPath      = "/usr/local/bin/dagger-engine"
	RuntimePath           = distconsts.RuntimePath
	DaggerInitPath        = distconsts.DaggerInitPath
	DaggerInitSessionPath = distconsts.DaggerInitSessionPath
	DaggerCLIPath         = distconsts.DaggerCLIPath
	TiniPath              = distconsts.TiniPath
	GitRemoteEntirePath   = "/usr/local/bin/git-remote-entire"
)

const (
	GolangVersion = distconsts.GolangVersion
	GolangImage   = distconsts.GolangImage

	AlpineVersion = distconsts.AlpineVersion
	AlpineImage   = distconsts.AlpineImage
	UbuntuVersion = "22.04"

	CniVersion   = "v1.9.0"
	SSHFSVersion = "3.7.6"

	QemuVersion  = "11.0.2"
	QemuChecksum = "sha256:3745f6ea88e2e87fe0dc838b2b1d4e0a770bf48e01a1d5a186842a1fff76ccf5"
	// tonistiigi/binfmt commit providing QEMU patches, and the version of its
	// buildkit-direct-execve patch series matching QemuVersion.
	BinfmtCommit    = "e41434fefad6aee7b01fbe5efc1c4b6f6c735fff"
	QemuPatchSeries = "11.0"

	XxImage = "tonistiigi/xx:1.2.1"

	// dagger/golangci-lint commit whose export driver the Go SDK's codegen
	// loads a module's imports through, from dagger/go's archives.
	GolangciLintCommit = "124019e8bb1fdc33a818759ba78e32ab429e28d5"

	EntireCLIVersion = "0.12.2"
)

var EntireCLIChecksums = map[string]string{
	"amd64": "sha256:bfc5c8d5b599f328775b89b735db020c963e307b6a324fc30ce371fe8fa789e8",
	"arm64": "sha256:f08a1b8c6352d3f78076268dc67b0b273d7f62dbab94b15aec028e7b4257ad0c",
}
